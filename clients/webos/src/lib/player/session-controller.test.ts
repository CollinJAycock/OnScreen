import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  OPEN_RETRY_DELAY_MS,
  PlayIntent,
  SessionController,
  type OpenRequest,
  type SuspendSnapshot,
} from './session-controller';
import { PendingScrub } from './scrub';
import {
  RELATIVE_SEEK_EDGE_MARGIN_MS,
  SCRUB_COMMIT_MS,
  classifySeek,
  planContentSeek,
  type StreamWindow,
} from './session';

interface FakeSession {
  id: number;
  req: OpenRequest;
}

/** A fake server: every start is answered by hand (ok / fail), in any order. */
function harness(opts: { canOpen?: () => boolean; isRefusal?: (e: unknown) => boolean } = {}) {
  let next = 1;
  const pending: Array<{ req: OpenRequest; ok: () => void; fail: (e: unknown) => void }> = [];
  const attached: FakeSession[] = [];
  const retired: number[] = [];
  const failures: Array<{ err: unknown; req: OpenRequest }> = [];
  const c = new SessionController<FakeSession>({
    start: (req) =>
      new Promise<FakeSession>((resolve, reject) => {
        const s = { id: next++, req };
        pending.push({ req, ok: () => resolve(s), fail: reject });
      }),
    attach: (s) => attached.push(s),
    retire: (s) => retired.push(s.id),
    canOpen: opts.canOpen ?? (() => true),
    isRefusal: opts.isRefusal ?? ((e) => (e as { code?: string })?.code === 'PLAYBACK_STOPPED'),
    failed: (err, req) => failures.push({ err, req }),
  });
  return { c, pending, attached, retired, failures };
}

const req = (positionMs: number, extra: Partial<OpenRequest> = {}): OpenRequest => ({
  positionMs,
  videoCopy: true,
  audioOrdinal: 0,
  autoplay: true,
  ...extra,
});

const snap = (extra: Partial<SuspendSnapshot> = {}): SuspendSnapshot => ({
  live: true,
  positionMs: 0,
  scrubTargetMs: null,
  wantPlaying: true,
  videoCopy: true,
  audioOrdinal: 0,
  ...extra,
});

describe('SessionController — one start in flight', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('queues requests behind the one in flight, keeping only the latest', async () => {
    const h = harness();
    const a = h.c.request(req(60_000)); // a seek re-issue
    const b = h.c.request(req(60_000, { audioOrdinal: 1 })); // an audio switch
    const c = h.c.request(req(60_000, { audioOrdinal: 2 })); // another one
    expect(h.pending).toHaveLength(1); // never two starts side by side
    expect(h.c.opening).toBe(true);
    expect(h.c.intent?.audioOrdinal).toBe(2);
    await expect(b).resolves.toBe('superseded');
    h.pending[0].ok();
    // The seek's session is no longer wanted: retired, not bound.
    await expect(a).resolves.toBe('superseded');
    expect(h.retired).toEqual([1]);
    expect(h.pending).toHaveLength(2);
    expect(h.pending[1].req.audioOrdinal).toBe(2);
    h.pending[1].ok();
    await expect(c).resolves.toBe('attached');
    expect(h.attached.map((s) => s.req.audioOrdinal)).toEqual([2]);
    expect(h.c.opening).toBe(false);
  });

  it('retries a failed start once at the same point, then binds it', async () => {
    const h = harness();
    const p = h.c.request(req(1_800_000, { autoplay: false }));
    h.pending[0].fail(new Error('500'));
    await vi.advanceTimersByTimeAsync(OPEN_RETRY_DELAY_MS);
    expect(h.pending).toHaveLength(2);
    expect(h.pending[1].req).toEqual(req(1_800_000, { autoplay: false }));
    h.pending[1].ok();
    await expect(p).resolves.toBe('attached');
    expect(h.failures).toEqual([]);
  });

  it('reports a start that fails twice (the page shows its error)', async () => {
    const h = harness();
    const p = h.c.request(req(10_000));
    h.pending[0].fail(new Error('timeout'));
    await vi.advanceTimersByTimeAsync(OPEN_RETRY_DELAY_MS);
    h.pending[1].fail(new Error('timeout again'));
    await expect(p).resolves.toBe('failed');
    expect(h.failures).toHaveLength(1);
    expect((h.failures[0].err as Error).message).toBe('timeout again');
    expect(h.attached).toEqual([]);
  });

  it('reports a refusal at once, without a retry', async () => {
    const h = harness();
    const p = h.c.request(req(10_000));
    h.pending[0].fail({ code: 'PLAYBACK_STOPPED' });
    await expect(p).resolves.toBe('failed');
    await vi.advanceTimersByTimeAsync(OPEN_RETRY_DELAY_MS * 3);
    expect(h.pending).toHaveLength(1);
    expect(h.failures).toHaveLength(1);
  });

  it('lets a newer request stand in for the retry of a failed one', async () => {
    const h = harness();
    const a = h.c.request(req(10_000));
    const b = h.c.request(req(20_000));
    h.pending[0].fail(new Error('500'));
    await expect(a).resolves.toBe('superseded');
    expect(h.pending).toHaveLength(2);
    expect(h.pending[1].req.positionMs).toBe(20_000);
    h.pending[1].ok();
    await expect(b).resolves.toBe('attached');
    expect(h.failures).toEqual([]);
  });

  it('opens nothing while an error is on screen, and binds nothing that comes back to one', async () => {
    let canOpen = true;
    const h = harness({ canOpen: () => canOpen });
    const a = h.c.request(req(10_000));
    canOpen = false; // the fatal-error overlay went up meanwhile
    h.pending[0].ok();
    await expect(a).resolves.toBe('closed');
    expect(h.retired).toEqual([1]);
    await expect(h.c.request(req(20_000))).resolves.toBe('closed');
    expect(h.pending).toHaveLength(1);
    expect(h.attached).toEqual([]);
  });

  it('retires a start that comes back after the player left', async () => {
    const h = harness();
    const a = h.c.request(req(10_000));
    const b = h.c.request(req(20_000));
    h.c.close();
    await expect(b).resolves.toBe('closed');
    h.pending[0].ok();
    await expect(a).resolves.toBe('closed');
    expect(h.retired).toEqual([1]);
    await expect(h.c.request(req(1))).resolves.toBe('closed');
  });
});

describe('SessionController — suspend / resume', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('Home during the item load: the start point, not 0:00, is what resume opens', async () => {
    const h = harness();
    // Pressed while items.get / preferences were still pending: no request
    // yet, and the player's position is still its initial 0.
    h.c.suspend(snap({ live: false, positionMs: 0 }));
    // The start then asks for the resume point (45:00): deferred.
    await expect(h.c.request(req(2_700_000))).resolves.toBe('deferred');
    expect(h.pending).toHaveLength(0);
    const r = h.c.resume();
    expect(h.pending[0].req.positionMs).toBe(2_700_000);
    expect(h.pending[0].req.autoplay).toBe(true);
    h.pending[0].ok();
    await expect(r).resolves.toBe('attached');
  });

  it('Home while the start is in flight: it is retired, and resume opens the same request', async () => {
    const h = harness();
    const first = h.c.request(req(2_700_000));
    h.c.suspend(snap({ live: false, positionMs: 0 }));
    h.pending[0].ok();
    await expect(first).resolves.toBe('deferred');
    expect(h.retired).toEqual([1]);
    const r = h.c.resume();
    expect(h.pending[1].req.positionMs).toBe(2_700_000);
    h.pending[1].ok();
    await expect(r).resolves.toBe('attached');
    expect(h.attached).toHaveLength(1);
  });

  it('a quick return queues the re-open behind the stale start instead of racing it', async () => {
    const h = harness();
    const first = h.c.request(req(600_000));
    h.c.suspend(snap({ live: false }));
    const r = h.c.resume();
    expect(h.pending).toHaveLength(1); // still only the first start out
    h.pending[0].ok();
    await expect(first).resolves.toBe('superseded');
    expect(h.retired).toEqual([1]);
    expect(h.pending).toHaveLength(2);
    h.pending[1].ok();
    await expect(r).resolves.toBe('attached');
    expect(h.attached.map((s) => s.id)).toEqual([2]);
  });

  it('captures a pending scrub target and the intended play state of a live stream', async () => {
    const h = harness();
    h.c.suspend(snap({ positionMs: 1_000_000, scrubTargetMs: 1_300_000, wantPlaying: true, videoCopy: false, audioOrdinal: 1 }));
    const r = h.c.resume();
    expect(h.pending[0].req).toEqual({ positionMs: 1_300_000, videoCopy: false, audioOrdinal: 1, autoplay: true });
    h.pending[0].ok();
    await expect(r).resolves.toBe('attached');
  });

  it('keeps a paused player paused across the background', async () => {
    const h = harness();
    h.c.suspend(snap({ positionMs: 42_000, wantPlaying: false }));
    void h.c.resume();
    expect(h.pending[0].req.autoplay).toBe(false);
  });

  it('has nothing to open when suspended before anything was asked for, and nothing came', async () => {
    const h = harness();
    h.c.suspend(snap({ live: false }));
    await expect(h.c.resume()).resolves.toBeNull();
    expect(h.pending).toHaveLength(0);
  });
});

describe('park and land (the page wiring, with fake timers)', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  // The page's seek path: the scrub commits through seekToContent, whose
  // decision is planContentSeek (the page's own function), re-issuing through
  // the controller.
  function page() {
    const h = harness();
    let streamReady = true;
    // A remux resumed at 45:00: stream 0 = 2695 s; 8 s produced so far.
    let offsetMs = 2_695_000;
    let win: StreamWindow = { producedEndMs: 8_000, ended: false };
    const durationMs = 7_200_000;
    const seeks: number[] = [];
    const parked: boolean[] = [];
    const scrub: PendingScrub = new PendingScrub({
      commit: (t, relative) => seekToContent(t, relative),
      ready: () => streamReady && !h.c.opening,
      changed: () => {},
    });
    function seekToContent(t: number, relative: boolean) {
      const a = planContentSeek(t, relative, {
        streamReady,
        opening: h.c.opening,
        serverStream: true,
        durationMs,
        offsetMs,
        window: win,
      });
      if (a.kind === 'park') {
        parked.push(a.relative);
        scrub.park(a.targetMs, durationMs, a.relative);
      } else if (a.kind === 'reissue') {
        streamReady = false;
        void h.c.request(req(a.positionMs));
      } else {
        seeks.push(a.streamSec);
      }
    }
    return {
      h,
      scrub,
      seeks,
      parked,
      seekToContent,
      setSession: (offset: number, w: StreamWindow) => {
        offsetMs = offset;
        win = w;
      },
      // loadedmetadata of the newly bound stream: lands a parked target.
      metadata: () => {
        streamReady = true;
        scrub.flush();
      },
    };
  }

  it('▶▶ during a re-issue lands on the new stream without a second re-issue', async () => {
    const p = page();
    p.seekToContent(2_600_000, false); // a chapter before the head: re-issue
    expect(p.h.pending).toHaveLength(1);
    // ▶▶ pressed twice while the new session is on its way.
    p.scrub.nudge(30_000, 2_600_000, 7_200_000);
    p.scrub.nudge(30_000, 2_600_000, 7_200_000);
    await vi.advanceTimersByTimeAsync(SCRUB_COMMIT_MS * 2);
    expect(p.scrub.value).toBe(2_660_000); // waiting: the stream isn't ready
    // The new session opens at 2600 s with only a few segments listed.
    p.setSession(2_598_000, { producedEndMs: 6_000, ended: false });
    p.h.pending[0].ok();
    await vi.advanceTimersByTimeAsync(0);
    p.metadata();
    // 60 s ahead of a fresh session's edge: it lands at the edge, ffmpeg's
    // burst fills the rest; no second session.
    expect(p.h.pending).toHaveLength(1);
    expect(p.seeks).toEqual([6]);
  });

  it('a chapter jump parked during a re-issue still re-issues if it is far outside', async () => {
    const p = page();
    p.seekToContent(2_600_000, false);
    p.seekToContent(5_000_000, false); // parked: a start is out
    expect(p.scrub.value).toBe(5_000_000);
    expect(p.parked).toEqual([false]); // a chapter is absolute
    p.setSession(2_598_000, { producedEndMs: 6_000, ended: false });
    p.h.pending[0].ok();
    await vi.advanceTimersByTimeAsync(0);
    p.metadata();
    expect(p.h.pending).toHaveLength(2);
    expect(p.h.pending[1].req.positionMs).toBe(5_000_000);
  });

  it('a relative seek far past the edge, or before the head, still re-issues', () => {
    const w = { offsetMs: 2_695_000, producedEndMs: 8_000, ended: false, durationMs: 7_200_000 };
    const margin = { forwardMarginMs: RELATIVE_SEEK_EDGE_MARGIN_MS };
    expect(classifySeek(2_695_000 + 8_000 + 60_000, w, margin).kind).toBe('clamp');
    expect(classifySeek(2_695_000 + 8_000 + 60_001, w, margin)).toEqual({ kind: 'reissue', positionMs: 2_763_001 });
    expect(classifySeek(2_690_000, w, margin)).toEqual({ kind: 'reissue', positionMs: 2_690_000 });
    // The same point as an absolute jump re-issues.
    expect(classifySeek(2_720_000, w).kind).toBe('reissue');
  });
});

describe('PlayIntent (what an attaching stream does about play / pause)', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('a pause pressed while a re-issue is out survives the attach', async () => {
    const intent = new PlayIntent(true);
    let bound: boolean | null = null;
    const c = new SessionController<number>({
      start: () => Promise.resolve(1),
      attach: (_s, r) => {
        bound = intent.forAttach(r);
      },
      retire: () => {},
      canOpen: () => true,
      isRefusal: () => false,
      failed: () => {},
    });
    // An audio switch made while playing...
    const p = c.request({ positionMs: 60_000, videoCopy: true, audioOrdinal: 1, ...intent.stamp() });
    // ...and OK pressed to pause before the new session lands.
    intent.set(false);
    await expect(p).resolves.toBe('attached');
    expect(bound).toBe(false);
    expect(intent.playing).toBe(false);
  });

  it('takes the request’s play state when nothing changed since it was made', () => {
    const intent = new PlayIntent(true);
    intent.set(false);
    const stamp = intent.stamp();
    expect(stamp).toEqual({ autoplay: false, intentSeq: 1 });
    expect(intent.forAttach(stamp)).toBe(false);
    // A pause, then play again, during the start: the latest word wins.
    const req2 = intent.stamp();
    intent.set(true);
    expect(intent.forAttach(req2)).toBe(true);
  });

  it('the first start plays unless the user paused during "Starting playback…"', () => {
    const intent = new PlayIntent();
    const first = intent.stamp();
    expect(first.autoplay).toBe(true);
    intent.set(false); // Stop pressed while loading
    expect(intent.forAttach(first)).toBe(false);
  });

  it('an unstamped request (a retry built elsewhere) sets the intent', () => {
    const intent = new PlayIntent(false);
    expect(intent.forAttach({ autoplay: true })).toBe(true);
    expect(intent.playing).toBe(true);
  });

  it('carries the stamp through a suspend capture of a live stream', async () => {
    const h = harness();
    const intent = new PlayIntent(true);
    intent.set(false);
    h.c.suspend(snap({ positionMs: 42_000, wantPlaying: intent.playing, intentSeq: intent.seq }));
    void h.c.resume();
    expect(h.pending[0].req.autoplay).toBe(false);
    expect(intent.forAttach(h.pending[0].req)).toBe(false);
  });
});

describe('planContentSeek parks key seeks as relative', () => {
  it('keeps `relative` on a target parked while a session is on its way', () => {
    const st = {
      streamReady: true,
      opening: true,
      serverStream: true,
      durationMs: 7_200_000,
      offsetMs: 0,
      window: { producedEndMs: 0, ended: false },
    };
    expect(planContentSeek(100_000, true, st)).toEqual({ kind: 'park', targetMs: 100_000, relative: true });
    expect(planContentSeek(100_000, false, { ...st, opening: false, streamReady: false })).toEqual({
      kind: 'park',
      targetMs: 100_000,
      relative: false,
    });
  });
});
