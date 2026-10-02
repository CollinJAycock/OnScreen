import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { NotificationEvent } from './api/types';
import type { RefreshOutcome } from './api/client';
import {
  ASSET_TOKEN_EXPIRY_MARGIN_MS,
  ASSET_TOKEN_TTL_MS,
  EventStream,
  PLAYBACK_TRANSFER_EVENT,
  RECONNECT_MAX_MS,
  STABLE_OPEN_MS,
  assetTokenNearExpiry,
  isTransferForMe,
  parseEvent,
  parseTransfer,
  shouldRedialAfterHidden,
  shouldRefreshBeforeRedial,
  streamUrl,
  streamWanted,
  type EventSourceLike,
  type EventStreamDeps,
} from './events';

const ITEM = '0f8fad5b-d9cb-469f-a165-70867728950e';
const ME = 'LG webOS TV — OLED65C1 #a1b2';

const transfer = (data: Record<string, unknown>): NotificationEvent => ({
  id: '',
  type: PLAYBACK_TRANSFER_EVENT,
  item_id: ITEM,
  data,
});

describe('parseEvent', () => {
  it('reads a server event', () => {
    expect(parseEvent('{"id":"","type":"progress.updated","item_id":"x","data":{"position_ms":5}}')).toEqual({
      id: '',
      type: 'progress.updated',
      item_id: 'x',
      data: { position_ms: 5 },
    });
  });

  it('drops what is not one', () => {
    expect(parseEvent('')).toBeNull();
    expect(parseEvent('not json')).toBeNull();
    expect(parseEvent('[1,2]')).toBeNull();
    expect(parseEvent('{"title":"no type"}')).toBeNull();
    expect(parseEvent(undefined)).toBeNull();
  });
});

describe('the transfer matcher', () => {
  it('reads the server payload', () => {
    expect(parseTransfer(transfer({ item_id: ITEM, position_ms: 754_321.6, target_client_name: ME }))).toEqual({
      itemId: ITEM,
      positionMs: 754_321,
      targetClientName: ME,
    });
  });

  it('acts only on a transfer naming this TV, exactly', () => {
    const evt = transfer({ item_id: ITEM, position_ms: 1000, target_client_name: ME });
    expect(isTransferForMe(evt, ME)).toBe(true);
    // The channel is per user: another of the user's devices.
    expect(isTransferForMe(evt, 'LG webOS TV — OLED65C1 #ffff')).toBe(false);
    expect(isTransferForMe(transfer({ item_id: ITEM, target_client_name: ME.toUpperCase() }), ME)).toBe(false);
    // A TV that reports no name never matches.
    expect(isTransferForMe(evt, '')).toBe(false);
  });

  it('starts from the top when the position is missing or nonsense', () => {
    expect(parseTransfer(transfer({ item_id: ITEM, target_client_name: ME }))?.positionMs).toBe(0);
    expect(parseTransfer(transfer({ item_id: ITEM, position_ms: -5, target_client_name: ME }))?.positionMs).toBe(0);
    expect(parseTransfer(transfer({ item_id: ITEM, position_ms: '9', target_client_name: ME }))?.positionMs).toBe(0);
  });

  it('refuses an item id that is not a UUID (it goes into a route) and other event types', () => {
    expect(parseTransfer(transfer({ item_id: '../settings', target_client_name: ME }))).toBeNull();
    expect(parseTransfer(transfer({ item_id: ITEM }))).toBeNull();
    expect(parseTransfer(transfer({ item_id: ITEM, target_client_name: '' }))).toBeNull();
    expect(parseTransfer({ id: '', type: 'progress.updated', data: { item_id: ITEM, target_client_name: ME } })).toBeNull();
    expect(parseTransfer({ id: '', type: PLAYBACK_TRANSFER_EVENT })).toBeNull();
    expect(isTransferForMe(null, ME)).toBe(false);
  });
});

describe('streamWanted', () => {
  it('streams on every signed-in screen, the player included', () => {
    expect(streamWanted('/hub', true)).toBe(true);
    expect(streamWanted('/watch/[id]', true)).toBe(true);
    expect(streamWanted('/settings', true)).toBe(true);
  });

  it('closes on the signed-out screens (sign-out, forget server, any Unauthorized)', () => {
    expect(streamWanted('/login', true)).toBe(false);
    expect(streamWanted('/setup', true)).toBe(false);
    expect(streamWanted('/pair', true)).toBe(false);
    expect(streamWanted('/hub', false)).toBe(false);
  });
});

describe('helpers', () => {
  it('puts the asset token in the query', () => {
    expect(streamUrl('https://tv.example/', 'a+b')).toBe('https://tv.example/api/v1/notifications/stream?token=a%2Bb');
  });

  it('redials after a stay in the background longer than the keepalive interval', () => {
    expect(shouldRedialAfterHidden(5_000)).toBe(false);
    expect(shouldRedialAfterHidden(30_000)).toBe(false);
    expect(shouldRedialAfterHidden(45 * 60_000)).toBe(true);
    expect(shouldRedialAfterHidden(NaN)).toBe(false);
  });
});

describe('when a reconnect refreshes the tokens', () => {
  const state = (over: Partial<Parameters<typeof shouldRefreshBeforeRedial>[0]> = {}) => ({
    openedThisRun: false,
    refreshAttemptedOnThisToken: false,
    tokenAgeKnown: false,
    tokenNearExpiry: false,
    tokenChangedSinceDial: false,
    offline: false,
    ...over,
  });

  it('refreshes a token of unknown age when nothing has opened in this run', () => {
    expect(shouldRefreshBeforeRedial(state())).toBe(true);
  });

  it('not a token known to be fresh: a failed dial then is the network or the server, not expiry (a standby wake)', () => {
    expect(shouldRefreshBeforeRedial(state({ tokenAgeKnown: true }))).toBe(false);
  });

  it('at most once per stored token per run, landed or not', () => {
    expect(shouldRefreshBeforeRedial(state({ refreshAttemptedOnThisToken: true }))).toBe(false);
    expect(
      shouldRefreshBeforeRedial(state({ tokenAgeKnown: true, tokenNearExpiry: true, refreshAttemptedOnThisToken: true })),
    ).toBe(false);
  });

  it('never because an open stream dropped: the stored token worked a moment ago', () => {
    expect(shouldRefreshBeforeRedial(state({ openedThisRun: true }))).toBe(false);
    expect(shouldRefreshBeforeRedial(state({ openedThisRun: true, tokenAgeKnown: true }))).toBe(false);
  });

  it('when the token is known to be near or past its expiry', () => {
    expect(shouldRefreshBeforeRedial(state({ openedThisRun: true, tokenAgeKnown: true, tokenNearExpiry: true }))).toBe(true);
  });

  it('never while offline', () => {
    expect(shouldRefreshBeforeRedial(state({ offline: true }))).toBe(false);
    expect(shouldRefreshBeforeRedial(state({ offline: true, tokenAgeKnown: true, tokenNearExpiry: true }))).toBe(false);
  });

  it("tries a token another caller's refresh stored since the failed dial first", () => {
    expect(shouldRefreshBeforeRedial(state({ tokenChangedSinceDial: true }))).toBe(false);
    expect(shouldRefreshBeforeRedial(state({ tokenChangedSinceDial: true, tokenNearExpiry: true }))).toBe(false);
  });

  it('knows the token is near expiry from when it was minted', () => {
    const t0 = 1_000_000_000_000;
    expect(assetTokenNearExpiry(t0, t0)).toBe(false);
    expect(assetTokenNearExpiry(t0, t0 + ASSET_TOKEN_TTL_MS - ASSET_TOKEN_EXPIRY_MARGIN_MS - 1)).toBe(false);
    expect(assetTokenNearExpiry(t0, t0 + ASSET_TOKEN_TTL_MS - ASSET_TOKEN_EXPIRY_MARGIN_MS)).toBe(true);
    expect(assetTokenNearExpiry(t0, t0 + 3 * ASSET_TOKEN_TTL_MS)).toBe(true);
  });

  it('treats an unknown age as not near: no record, or a clock now behind it', () => {
    const t0 = 1_000_000_000_000;
    expect(assetTokenNearExpiry(null, t0)).toBe(false);
    expect(assetTokenNearExpiry(t0, t0 - 60_000)).toBe(false);
    expect(assetTokenNearExpiry(NaN, t0)).toBe(false);
  });
});

// ── The connection ─────────────────────────────────────────────────────

class FakeSource implements EventSourceLike {
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onopen: ((ev?: unknown) => void) | null = null;
  onerror: ((ev?: unknown) => void) | null = null;
  closed = false;
  constructor(readonly url: string) {}
  close() {
    this.closed = true;
  }
  emit(evt: object) {
    this.onmessage?.({ data: JSON.stringify(evt) });
  }
}

function harness(over: Partial<EventStreamDeps> = {}) {
  const sources: FakeSource[] = [];
  let token = 'tok1';
  let issuedAt: number | null = Date.now();
  let online = true;
  const refresh = vi.fn(async (): Promise<RefreshOutcome> => {
    token = `tok${sources.length + 1}`;
    issuedAt = Date.now();
    return 'ok';
  });
  const stream = new EventStream({
    origin: () => 'https://tv.example',
    assetToken: () => token,
    assetTokenIssuedAt: () => issuedAt,
    refresh,
    online: () => online,
    now: () => Date.now(),
    connect: (url) => {
      const s = new FakeSource(url);
      sources.push(s);
      return s;
    },
    setTimer: (fn, ms) => setTimeout(fn, ms),
    clearTimer: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    ...over,
  });
  return {
    stream,
    sources,
    refresh,
    setToken: (t: string | null) => (token = t as string),
    setIssuedAt: (t: number | null) => (issuedAt = t),
    setOnline: (v: boolean) => (online = v),
  };
}

describe('EventStream', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('holds one connection however often it is started', () => {
    const { stream, sources } = harness();
    stream.start();
    stream.start();
    expect(sources).toHaveLength(1);
    expect(sources[0].url).toContain('?token=tok1');
  });

  it('dispatches by type to every subscriber until they unsubscribe', () => {
    const { stream, sources } = harness();
    const a = vi.fn();
    const b = vi.fn();
    const other = vi.fn();
    const offA = stream.subscribe('progress.updated', a);
    stream.subscribe('progress.updated', b);
    stream.subscribe('playback.stop', other);
    stream.start();
    sources[0].emit({ id: '', type: 'progress.updated', item_id: 'x', data: { position_ms: 9 } });
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(1);
    expect(other).not.toHaveBeenCalled();
    offA();
    sources[0].emit({ id: '', type: 'progress.updated', item_id: 'x' });
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(2);
  });

  it('keeps delivering to the others when one listener throws', () => {
    const { stream, sources } = harness();
    const ok = vi.fn();
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    stream.subscribe('playback.transfer', () => {
      throw new Error('boom');
    });
    stream.subscribe('playback.transfer', ok);
    stream.start();
    sources[0].emit({ id: '', type: 'playback.transfer' });
    expect(ok).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });

  it('a first dial that fails with a token of unknown age: closes, refreshes once, then reconnects with backoff 1 s doubling to 30 s', async () => {
    const { stream, sources, refresh, setIssuedAt } = harness();
    // Stored by an older build: no mint time.
    setIssuedAt(null);
    stream.start();
    sources[0].onerror?.();
    expect(sources[0].closed).toBe(true);
    await vi.advanceTimersByTimeAsync(999);
    expect(sources).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(sources).toHaveLength(2);
    // The reconnect carries the refreshed asset token.
    expect(sources[1].url).toContain('?token=tok2');
    sources[1].onerror?.();
    await vi.advanceTimersByTimeAsync(1_999);
    expect(sources).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(sources).toHaveLength(3);
    // Capped.
    for (let i = 0; i < 8; i++) {
      sources[sources.length - 1].onerror?.();
      await vi.advanceTimersByTimeAsync(RECONNECT_MAX_MS);
    }
    const n = sources.length;
    sources[n - 1].onerror?.();
    await vi.advanceTimersByTimeAsync(RECONNECT_MAX_MS - 1);
    expect(sources).toHaveLength(n);
    await vi.advanceTimersByTimeAsync(1);
    expect(sources).toHaveLength(n + 1);
    // A fresh token that still wouldn't open wasn't the problem: no more
    // rotations, however long it keeps failing.
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('resets the backoff once a connection has stayed open for a keepalive interval', async () => {
    const { stream, sources } = harness();
    stream.start();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    sources[1].onerror?.();
    await vi.advanceTimersByTimeAsync(2_000);
    sources[2].onopen?.();
    await vi.advanceTimersByTimeAsync(STABLE_OPEN_MS);
    sources[2].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sources).toHaveLength(4);
  });

  it('keeps backing off when a connection opens and is closed at once (a proxy ending the stream)', async () => {
    const { stream, sources } = harness();
    stream.start();
    sources[0].onopen?.();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sources).toHaveLength(2);
    sources[1].onopen?.();
    sources[1].onerror?.();
    await vi.advanceTimersByTimeAsync(1_999);
    expect(sources).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(sources).toHaveLength(3);
    sources[2].onopen?.();
    sources[2].onerror?.();
    await vi.advanceTimersByTimeAsync(3_999);
    expect(sources).toHaveLength(3);
    await vi.advanceTimersByTimeAsync(1);
    expect(sources).toHaveLength(4);
  });

  it('a drop of an open stream redials with the stored token, never refreshing', async () => {
    const { stream, sources, refresh } = harness();
    stream.start();
    sources[0].onopen?.();
    // The server restarting: the drop, then redials that fail for a while.
    for (let i = 0; i < 6; i++) {
      sources[sources.length - 1].onerror?.();
      await vi.advanceTimersByTimeAsync(RECONNECT_MAX_MS);
    }
    expect(refresh).not.toHaveBeenCalled();
    expect(sources).toHaveLength(7);
    expect(sources.every((s) => s.url.includes('?token=tok1'))).toBe(true);
  });

  it('refreshes after a drop once the token is near its expiry', async () => {
    const { stream, sources, refresh, setIssuedAt } = harness();
    stream.start();
    sources[0].onopen?.();
    // Open for most of a day on one token.
    setIssuedAt(Date.now() - (ASSET_TOKEN_TTL_MS - ASSET_TOKEN_EXPIRY_MARGIN_MS));
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(sources[1].url).toContain('?token=tok2');
    // The fresh token isn't near its expiry: the next drop just redials.
    sources[1].onerror?.();
    await vi.advanceTimersByTimeAsync(2_000);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(sources).toHaveLength(3);
  });

  it('a refresh with no verdict (503, timeout) keeps the tokens and redials, and is not retried on that token in this run', async () => {
    // Retrying could present a token the server already rotated, if the
    // lost response was the rotation.
    const refresh = vi.fn(async (): Promise<RefreshOutcome> => 'failed');
    const { stream, sources, setIssuedAt } = harness({ refresh });
    setIssuedAt(null);
    stream.start();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(sources).toHaveLength(2);
    expect(sources[1].url).toContain('?token=tok1');
    for (let i = 0; i < 6; i++) {
      sources[sources.length - 1].onerror?.();
      await vi.advanceTimersByTimeAsync(RECONNECT_MAX_MS);
    }
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(stream.active).toBe(true);
    // A new run (the network back, a wake) may try once more.
    stream.restart();
    sources[sources.length - 1].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(refresh).toHaveBeenCalledTimes(2);
  });

  it('never refreshes while the browser says it is offline', async () => {
    const { stream, sources, refresh, setIssuedAt, setOnline } = harness();
    setIssuedAt(null);
    setOnline(false);
    stream.start();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(refresh).not.toHaveBeenCalled();
    expect(sources).toHaveLength(2);
  });

  it('still reconnects when the refresh throws', async () => {
    const { stream, sources, setIssuedAt } = harness({ refresh: async () => Promise.reject(new Error('offline')) });
    setIssuedAt(null);
    stream.start();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sources).toHaveLength(2);
  });

  it('a rejected refresh (the sign-in is dead) stops the stream and says so', async () => {
    const refresh = vi.fn(async (): Promise<RefreshOutcome> => 'rejected');
    const { stream, sources, setIssuedAt } = harness({ refresh });
    setIssuedAt(null);
    const rejected = vi.fn();
    const off = stream.onRejected(rejected);
    stream.start();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(rejected).toHaveBeenCalledTimes(1);
    expect(stream.active).toBe(false);
    await vi.advanceTimersByTimeAsync(10 * RECONNECT_MAX_MS);
    expect(sources).toHaveLength(1);
    expect(refresh).toHaveBeenCalledTimes(1);
    off();
    stream.start();
    sources[1].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(rejected).toHaveBeenCalledTimes(1);
  });

  it('a rejection still reaches the app when clearing the dead tokens stopped the stream first', async () => {
    let stream!: EventStream;
    // The client drops the sign-in before the outcome resolves; the root
    // layout's sign-out listener stops the stream then.
    const refresh = vi.fn(async (): Promise<RefreshOutcome> => {
      stream.stop();
      return 'rejected';
    });
    const h = harness({ refresh });
    stream = h.stream;
    h.setIssuedAt(null);
    const rejected = vi.fn();
    stream.onRejected(rejected);
    stream.start();
    h.sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(rejected).toHaveBeenCalledTimes(1);
  });

  it('ignores a rejection for an older run once a newer sign-in restarted the stream', async () => {
    let settle: (o: RefreshOutcome) => void = () => {};
    const refresh = vi.fn(() => new Promise<RefreshOutcome>((r) => (settle = r)));
    const { stream, sources, setIssuedAt } = harness({ refresh });
    setIssuedAt(null);
    const rejected = vi.fn();
    stream.onRejected(rejected);
    stream.start();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    // Signed in again while the old token's refresh was out.
    stream.restart();
    settle('rejected');
    await vi.advanceTimersByTimeAsync(0);
    expect(rejected).not.toHaveBeenCalled();
    expect(stream.active).toBe(true);
  });

  it("redials with a token an API call's refresh stored meanwhile, rather than rotating again", async () => {
    const { stream, sources, refresh, setToken } = harness();
    stream.start();
    // Launch after a long sleep: the hub's first call 401s and refreshes
    // while the stream's first dial, with the old token, is failing.
    setToken('fromApi');
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(refresh).not.toHaveBeenCalled();
    expect(sources[1].url).toContain('?token=fromApi');
  });

  it('a restart is a new run: its first failed dial may refresh a token of unknown age again', async () => {
    const { stream, sources, refresh, setIssuedAt } = harness();
    setIssuedAt(null);
    stream.start();
    sources[0].onopen?.();
    // Back from standby: the old connection may be half-open; dial afresh.
    stream.restart();
    sources[1].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('a wake whose first dial fails with a token known to be fresh just redials (no rotation)', async () => {
    const { stream, sources, refresh } = harness();
    stream.start();
    sources[0].onopen?.();
    stream.restart();
    // The Wi-Fi isn't back yet.
    for (let i = 0; i < 4; i++) {
      sources[sources.length - 1].onerror?.();
      await vi.advanceTimersByTimeAsync(RECONNECT_MAX_MS);
    }
    expect(refresh).not.toHaveBeenCalled();
    expect(sources.every((s) => s.url.includes('?token=tok1'))).toBe(true);
  });

  it('stop closes the connection and cancels a pending reconnect, even mid-refresh', async () => {
    let release: () => void = () => {};
    const { stream, sources, setIssuedAt } = harness({
      refresh: () => new Promise<RefreshOutcome>((r) => (release = () => r('ok'))),
    });
    setIssuedAt(null);
    const fn = vi.fn();
    stream.subscribe('progress.updated', fn);
    stream.start();
    sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(1_000);
    // The refresh is out; signed out meanwhile.
    stream.stop();
    release();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(sources).toHaveLength(1);
    expect(stream.active).toBe(false);

    stream.start();
    expect(sources).toHaveLength(2);
    stream.stop();
    expect(sources[1].closed).toBe(true);
    // A late message from a closed source is dropped.
    sources[1].emit({ id: '', type: 'progress.updated' });
    expect(fn).not.toHaveBeenCalled();
  });

  it('does not dial without a token (signed out) and starts again on the next sign-in', () => {
    const { stream, sources, setToken } = harness();
    setToken(null);
    stream.start();
    expect(sources).toHaveLength(0);
    expect(stream.active).toBe(false);
    setToken('fresh');
    stream.start();
    expect(sources).toHaveLength(1);
  });

  it('restart dials a fresh connection, only while it is wanted open', () => {
    const { stream, sources } = harness();
    stream.restart();
    expect(sources).toHaveLength(0);
    stream.start();
    stream.restart();
    expect(sources).toHaveLength(2);
    expect(sources[0].closed).toBe(true);
    expect(sources[1].closed).toBe(false);
  });
});
