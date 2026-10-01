import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CONTENT_REVOKED_TEXT,
  GRACE_MS,
  HEARTBEAT_MS,
  ProgressReporter,
  STOP_WAIT_MS,
  classifyHeartbeatError,
  settleWithin,
  type HeartbeatRefusal,
  type ProgressReport,
} from './progress-reporter';

function apiError(status: number, code: string, message = '') {
  return Object.assign(new Error(message), { status, code });
}

/** A sender that records each report when it goes out and settles it after
 *  `delays[state]` ms (never, for Infinity), or fails it with `fail`. */
function fakeSender(delays: Partial<Record<string, number>> = {}, fail?: (r: ProgressReport) => unknown) {
  const sent: ProgressReport[] = [];
  const done: string[] = [];
  const send = (r: ProgressReport) => {
    sent.push(r);
    const ms = delays[r.state] ?? 0;
    return new Promise<void>((resolve, reject) => {
      if (ms === Infinity) return;
      setTimeout(() => {
        done.push(r.state);
        const e = fail?.(r);
        if (e) reject(e);
        else resolve();
      }, ms);
    });
  };
  return { send, sent, done };
}

describe('ProgressReporter', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('sends exactly one stopped at EOS, after the slow playing and the paused, in order', async () => {
    const f = fakeSender({ playing: 1_500, paused: 200 });
    const r = new ProgressReporter({ itemId: 'ep1', send: f.send });
    r.arm();
    r.start(() => ({ positionMs: 120_000, durationMs: 1_200_000 }));
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS); // the beat goes out, slow
    expect(f.sent.map((x) => x.state)).toEqual(['playing']);
    r.paused(1_199_000, 1_200_000);
    // The 'ended' handler, goToNext and the page's cleanup all call it.
    const a = r.stopped(1_200_000, 1_200_000);
    const b = r.stopped(1_200_000, 1_200_000);
    const c = r.stopped(400, 1_200_000);
    expect(b).toBe(a);
    expect(c).toBe(a);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(f.sent.map((x) => x.state)).toEqual(['playing', 'paused', 'stopped']);
    expect(f.done).toEqual(['playing', 'paused', 'stopped']);
    expect(f.sent[2].positionMs).toBe(1_200_000);
    // Nothing after the terminal report: no beat, no pause.
    r.paused(5, 1_200_000);
    r.start(() => ({ positionMs: 9_000, durationMs: 1_200_000 }));
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS * 3);
    expect(f.sent.filter((x) => x.state === 'stopped')).toHaveLength(1);
    expect(f.sent).toHaveLength(3);
  });

  it('does not let a hung heartbeat hold the final report past the grace period', async () => {
    const f = fakeSender({ playing: Infinity });
    const r = new ProgressReporter({ itemId: 'm', send: f.send });
    r.arm();
    r.start(() => ({ positionMs: 60_000, durationMs: 7_200_000 }));
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    let settled = false;
    void r.stopped(61_000, 7_200_000).then(() => (settled = true));
    await vi.advanceTimersByTimeAsync(GRACE_MS - 1);
    expect(f.sent.map((x) => x.state)).toEqual(['playing']);
    await vi.advanceTimersByTimeAsync(1);
    expect(f.sent.map((x) => x.state)).toEqual(['playing', 'stopped']);
    expect(f.sent[1].positionMs).toBe(61_000);
    await vi.advanceTimersByTimeAsync(10);
    expect(settled).toBe(true);
  });

  it("resolves stopped() by the wait cap when the server doesn't answer", async () => {
    const f = fakeSender({ stopped: Infinity });
    const r = new ProgressReporter({ itemId: 'm', send: f.send });
    r.arm();
    let settled = false;
    void r.stopped(10_000, 100_000).then(() => (settled = true));
    await vi.advanceTimersByTimeAsync(STOP_WAIT_MS - 1);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(settled).toBe(true);
  });

  it('sends no terminal report for a title that never started playing', async () => {
    const f = fakeSender();
    const r = new ProgressReporter({ itemId: 'm', send: f.send });
    r.paused(45_000, 100_000);
    await r.stopped(45_000, 100_000);
    await vi.advanceTimersByTimeAsync(STOP_WAIT_MS);
    expect(f.sent).toEqual([]);
  });

  it('beats only while playing and only when the position moved', async () => {
    const f = fakeSender();
    let state: { positionMs: number; durationMs: number } | null = null;
    const r = new ProgressReporter({ itemId: 'm', send: f.send });
    r.start(() => state);
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    expect(f.sent).toEqual([]); // not playing yet
    state = { positionMs: 10_000, durationMs: 100_000 };
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    state = { positionMs: 10_400, durationMs: 100_000 };
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS); // moved < 1 s: skipped
    state = { positionMs: 20_000, durationMs: 0 };
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS); // no length: skipped
    expect(f.sent.map((x) => x.positionMs)).toEqual([10_000]);
    r.hold();
  });

  it('carries the client name, the decision and the file id', async () => {
    const f = fakeSender();
    const r = new ProgressReporter({
      itemId: 'm',
      send: f.send,
      clientName: 'LG webOS TV — OLED55C1 #a1b2',
      fileId: 'file-1',
    });
    r.setDecision('directStream');
    r.arm();
    r.paused(5_000, 100_000);
    r.setDecision('transcode');
    void r.stopped(6_000, 100_000);
    await vi.advanceTimersByTimeAsync(0);
    expect(f.sent).toEqual([
      { itemId: 'm', positionMs: 5_000, durationMs: 100_000, state: 'paused',
        clientName: 'LG webOS TV — OLED55C1 #a1b2', decision: 'directStream', fileId: 'file-1' },
      { itemId: 'm', positionMs: 6_000, durationMs: 100_000, state: 'stopped',
        clientName: 'LG webOS TV — OLED55C1 #a1b2', decision: 'transcode', fileId: 'file-1' },
    ]);
  });

  it('reports a pause once per position, and again after playing on', async () => {
    const f = fakeSender();
    let pos = 30_000;
    const r = new ProgressReporter({ itemId: 'm', send: f.send });
    r.arm();
    r.paused(pos, 100_000);
    r.paused(pos, 100_000);
    r.start(() => ({ positionMs: (pos += 10_000), durationMs: 100_000 }));
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS);
    r.hold();
    r.paused(40_000, 100_000);
    r.paused(40_000, 100_000);
    await vi.advanceTimersByTimeAsync(10);
    expect(f.sent.map((x) => `${x.state}@${x.positionMs}`)).toEqual([
      'paused@30000',
      'playing@40000',
      'paused@40000',
    ]);
  });

  it('stops the heartbeat and says why on any 403 to a playing beat', async () => {
    const refusals: HeartbeatRefusal[] = [];
    const f = fakeSender({}, (x) => (x.state === 'playing' ? apiError(403, 'FORBIDDEN', 'forbidden') : null));
    let pos = 0;
    const r = new ProgressReporter({ itemId: 'm', send: f.send, onRefused: (x) => refusals.push(x) });
    r.arm();
    r.start(() => ({ positionMs: (pos += 10_000), durationMs: 100_000 }));
    await vi.advanceTimersByTimeAsync(HEARTBEAT_MS * 4);
    expect(refusals).toEqual([{ kind: 'revoked' }]);
    expect(f.sent.filter((x) => x.state === 'playing')).toHaveLength(1);
  });

  it('answers a probe with the refusal and stops beating', async () => {
    const f = fakeSender({}, () => apiError(403, 'PLAYBACK_STOPPED', 'Playback was stopped by the server admin: bye'));
    const r = new ProgressReporter({ itemId: 'm', send: f.send });
    r.arm();
    const p = r.probe(5_000, 100_000);
    await vi.advanceTimersByTimeAsync(0);
    expect(await p).toEqual({ kind: 'stopped', message: 'Playback was stopped by the server admin: bye' });
  });

  it('sends no probe for a title that never played (no Now Playing entry without a stopped)', async () => {
    const f = fakeSender({}, () => apiError(403, 'PLAYBACK_STOPPED', 'bye'));
    const r = new ProgressReporter({ itemId: 'm', send: f.send });
    const p = r.probe(5_000, 100_000);
    await vi.advanceTimersByTimeAsync(0);
    expect(await p).toBeNull();
    expect(f.sent).toEqual([]);
    expect(r.isArmed).toBe(false);
  });
});

describe('classifyHeartbeatError', () => {
  it('maps a 403 PARENTAL_LIMIT to the parental block with its reason', () => {
    expect(classifyHeartbeatError('playing', apiError(403, 'PARENTAL_LIMIT', 'daily_limit_reached'))).toEqual({
      kind: 'parental',
      reason: 'daily_limit_reached',
    });
  });
  it('maps a 403 PLAYBACK_STOPPED to the admin stop', () => {
    expect(classifyHeartbeatError('playing', apiError(403, 'PLAYBACK_STOPPED', 'stopped'))).toEqual({
      kind: 'stopped',
      message: 'stopped',
    });
  });
  it('maps any other 403 to revoked', () => {
    expect(classifyHeartbeatError('playing', apiError(403, 'FORBIDDEN'))).toEqual({ kind: 'revoked' });
    expect(classifyHeartbeatError('playing', apiError(403, ''))).toEqual({ kind: 'revoked' });
    expect(CONTENT_REVOKED_TEXT).toBe('This title is no longer available on this profile.');
  });
  it('ignores other statuses, other states and transport failures', () => {
    expect(classifyHeartbeatError('playing', apiError(500, 'INTERNAL'))).toBeNull();
    expect(classifyHeartbeatError('playing', apiError(401, 'UNAUTHORIZED'))).toBeNull();
    expect(classifyHeartbeatError('paused', apiError(403, 'PARENTAL_LIMIT'))).toBeNull();
    expect(classifyHeartbeatError('stopped', apiError(403, 'FORBIDDEN'))).toBeNull();
    expect(classifyHeartbeatError('playing', new TypeError('Failed to fetch'))).toBeNull();
    expect(classifyHeartbeatError('playing', null)).toBeNull();
  });
});

describe('settleWithin', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('gives the value when it comes in time, undefined otherwise, and never rejects', async () => {
    await expect(settleWithin(Promise.resolve(7), 100)).resolves.toBe(7);
    await expect(settleWithin(Promise.reject(new Error('x')), 100)).resolves.toBeUndefined();
    const slow = settleWithin(new Promise(() => {}), 100);
    await vi.advanceTimersByTimeAsync(100);
    await expect(slow).resolves.toBeUndefined();
  });
});
