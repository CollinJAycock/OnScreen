import { describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { ApiError, Unauthorized } from './api/client';
import type { CapabilitiesResponse, RequestQuota } from './api/endpoints';
import {
  NAV_GATES_CLOSED,
  NAV_GATES_MAX_AGE_MS,
  NO_QUOTA_ENDPOINT,
  NavGateLoader,
  discoverVisible,
  isMissingQuotaEndpoint,
  liveTvVisible,
  navGatesFor,
  onlineSubtitlesVisible,
  recordingsVisible,
  type NavGateDeps,
  type NavGates,
} from './navGates';

/** Capabilities with just the flags the gates read (an older server leaves
 *  live_tv_configured out: pass undefined). */
function caps(f: {
  requests?: boolean;
  live_tv?: boolean;
  dvr?: boolean;
  live_tv_configured?: boolean;
}): CapabilitiesResponse {
  return { features: f } as unknown as CapabilitiesResponse;
}

function quota(canRequest: boolean): RequestQuota {
  return {
    can_request: canRequest,
    window_days: 7,
    movies: { limit: 0, used: 0, remaining: null },
    tv: { limit: 0, used: 0, remaining: null },
  };
}

// The store-review server: a TMDB key (requests on), no tuner (live_tv and
// dvr are wired in, live_tv_configured says there's nothing to watch), and the
// reviewer account may not request.
const REVIEW_SERVER = caps({ requests: true, live_tv: true, dvr: true, live_tv_configured: false });
// A server with a TMDB key and a tuner.
const CONFIGURED = caps({ requests: true, live_tv: true, dvr: true, live_tv_configured: true });
// Older than live_tv_configured.
const OLDER = caps({ requests: true, live_tv: true, dvr: true });

const ALL_OPEN: NavGates = { discover: true, liveTv: true, recordings: true };

describe('gate rules', () => {
  it('hides all three for the store reviewer', () => {
    expect(navGatesFor(REVIEW_SERVER, quota(false))).toEqual(NAV_GATES_CLOSED);
  });

  it('shows all three to an account that may request on a server with a tuner', () => {
    expect(navGatesFor(CONFIGURED, quota(true))).toEqual(ALL_OPEN);
  });

  it('hides Discover on a server without requests, whatever the account', () => {
    expect(discoverVisible(caps({ requests: false }), quota(true))).toBe(false);
    expect(discoverVisible(caps({}), quota(true))).toBe(false);
  });

  it('shows Discover on a server without the quota endpoint, whatever its requests flag', () => {
    expect(discoverVisible(CONFIGURED, NO_QUOTA_ENDPOINT)).toBe(true);
    // v2.4's flag read only TMDB_API_KEY: false for a key set in Settings or
    // the bundled one, while Discover worked (0.2.0 showed it).
    expect(discoverVisible(caps({ requests: false }), NO_QUOTA_ENDPOINT)).toBe(true);
    expect(discoverVisible(caps({}), NO_QUOTA_ENDPOINT)).toBe(true);
  });

  it('keeps Discover hidden while the allowance loads or when its load failed', () => {
    expect(discoverVisible(CONFIGURED, undefined)).toBe(false);
    expect(discoverVisible(CONFIGURED, null)).toBe(false);
  });

  it('shows Discover only for an explicit can_request true', () => {
    const noFlag = { window_days: 7 } as unknown as RequestQuota;
    expect(discoverVisible(CONFIGURED, noFlag)).toBe(false);
    expect(discoverVisible(CONFIGURED, quota(true))).toBe(true);
  });

  it('follows live_tv_configured over live_tv', () => {
    expect(liveTvVisible(REVIEW_SERVER)).toBe(false);
    expect(liveTvVisible(CONFIGURED)).toBe(true);
    expect(liveTvVisible(caps({ live_tv: false, live_tv_configured: true }))).toBe(true);
  });

  it('falls back to live_tv on a server older than live_tv_configured', () => {
    expect(navGatesFor(OLDER, quota(true))).toEqual(ALL_OPEN);
    expect(liveTvVisible(caps({ live_tv: false, dvr: false }))).toBe(false);
    expect(liveTvVisible(caps({}))).toBe(false);
  });

  it('shows Recordings only with Live TV and DVR', () => {
    expect(recordingsVisible(CONFIGURED)).toBe(true);
    expect(recordingsVisible(caps({ live_tv: true, dvr: false, live_tv_configured: true }))).toBe(false);
    expect(recordingsVisible(caps({ live_tv: true, dvr: true, live_tv_configured: false }))).toBe(false);
    expect(recordingsVisible(caps({ live_tv: false, dvr: true }))).toBe(false);
  });

  it('closes every gate while capabilities load or when they failed', () => {
    expect(navGatesFor(undefined, quota(true))).toEqual(NAV_GATES_CLOSED);
    expect(navGatesFor(null, quota(true))).toEqual(NAV_GATES_CLOSED);
    expect(navGatesFor(null, NO_QUOTA_ENDPOINT)).toEqual(NAV_GATES_CLOSED);
  });
});

describe('isMissingQuotaEndpoint', () => {
  it("reads 404 and an older server's 400 (/requests/{id}) as no endpoint", () => {
    expect(isMissingQuotaEndpoint(new ApiError(404, 'NOT_FOUND', 'resource not found'))).toBe(true);
    expect(isMissingQuotaEndpoint(new ApiError(400, 'BAD_REQUEST', 'invalid request id'))).toBe(true);
  });

  it('reads anything else as a failed load', () => {
    expect(isMissingQuotaEndpoint(new ApiError(500, 'INTERNAL', 'boom'))).toBe(false);
    expect(isMissingQuotaEndpoint(new ApiError(503, 'ERROR', 'unavailable'))).toBe(false);
    expect(isMissingQuotaEndpoint(new Unauthorized())).toBe(false);
    expect(isMissingQuotaEndpoint(new Error('Network error: offline'))).toBe(false);
  });
});

// ── NavGateLoader ──────────────────────────────────────────────────────

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** A loader whose server answers are queued per call: each call takes the
 *  next answer (a value, or an Error to reject with). Its clock moves only
 *  by advance(). */
function harness(who: string | null = 'tv.example u1') {
  let identity = who;
  let clock = 1_000_000;
  const capsAnswers: Array<CapabilitiesResponse | Error | Deferred<CapabilitiesResponse>> = [];
  const quotaAnswers: Array<RequestQuota | Error | Deferred<RequestQuota>> = [];
  const answer = <T>(q: Array<T | Error | Deferred<T>>, name: string): Promise<T> => {
    const a = q.shift();
    if (a === undefined) return Promise.reject(new Error(`unexpected ${name} call`));
    if (a instanceof Error) return Promise.reject(a);
    if (a && typeof a === 'object' && 'promise' in a) return a.promise;
    return Promise.resolve(a as T);
  };
  const deps: NavGateDeps = {
    identity: () => identity,
    capabilities: vi.fn(() => answer(capsAnswers, 'capabilities')),
    quota: vi.fn(() => answer(quotaAnswers, 'quota')),
    now: () => clock,
  };
  const loader = new NavGateLoader(deps);
  const seen: NavGates[] = [];
  loader.gates.subscribe((g) => seen.push(g));
  return {
    loader,
    deps,
    capsAnswers,
    quotaAnswers,
    seen,
    gates: () => get(loader.gates),
    signInAs: (id: string | null) => {
      identity = id;
    },
    advance: (ms: number) => {
      clock += ms;
    },
  };
}

describe('NavGateLoader', () => {
  it('keeps every gate closed for the reviewer, from first render to loaded', async () => {
    const h = harness();
    h.capsAnswers.push(REVIEW_SERVER);
    h.quotaAnswers.push(quota(false));
    await h.loader.ensure();
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
    // Never open, not even for a moment.
    expect(h.seen.every((g) => !g.discover && !g.liveTv && !g.recordings)).toBe(true);
  });

  it('opens the gates once both answers are in, all at once', async () => {
    const h = harness();
    const c = deferred<CapabilitiesResponse>();
    const q = deferred<RequestQuota>();
    h.capsAnswers.push(c);
    h.quotaAnswers.push(q);
    const load = h.loader.ensure();
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
    c.resolve(CONFIGURED);
    await Promise.resolve();
    await Promise.resolve();
    // Live TV and Recordings wait for the account's answer too, so the bar
    // changes once rather than once per answer.
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
    q.resolve(quota(true));
    await load;
    expect(h.gates()).toEqual(ALL_OPEN);
    expect(h.seen.filter((g) => g.liveTv && !g.discover)).toEqual([]);
  });

  it('shares one load between concurrent callers and asks nothing once loaded', async () => {
    const h = harness();
    h.capsAnswers.push(CONFIGURED);
    h.quotaAnswers.push(quota(true));
    const a = h.loader.ensure();
    const b = h.loader.ensure();
    expect(b).toBe(a);
    await a;
    await h.loader.ensure();
    expect(h.deps.capabilities).toHaveBeenCalledTimes(1);
    expect(h.deps.quota).toHaveBeenCalledTimes(1);
  });

  it('keeps everything hidden when capabilities fail, and asks again next time', async () => {
    const h = harness();
    h.capsAnswers.push(new Error('Network error: offline'));
    h.quotaAnswers.push(quota(true));
    await h.loader.ensure();
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);

    h.capsAnswers.push(CONFIGURED);
    await h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);
    expect(h.deps.capabilities).toHaveBeenCalledTimes(2);
    // The allowance was known: not asked again.
    expect(h.deps.quota).toHaveBeenCalledTimes(1);
  });

  it('shows Discover on an older server whose quota route is missing (404 / 400)', async () => {
    for (const err of [
      new ApiError(404, 'NOT_FOUND', 'resource not found'),
      new ApiError(400, 'BAD_REQUEST', 'invalid request id'),
    ]) {
      const h = harness();
      h.capsAnswers.push(OLDER);
      h.quotaAnswers.push(err);
      await h.loader.ensure();
      expect(h.gates()).toEqual(ALL_OPEN);
    }
  });

  it('keeps Discover hidden when the allowance fails to load, and asks again', async () => {
    const h = harness();
    h.capsAnswers.push(CONFIGURED);
    h.quotaAnswers.push(new ApiError(500, 'INTERNAL', 'boom'));
    await h.loader.ensure();
    expect(h.gates()).toEqual({ discover: false, liveTv: true, recordings: true });

    h.quotaAnswers.push(quota(true));
    await h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);
  });

  it('shows Discover on a v2.4 server whose requests flag is off (Settings or bundled TMDB key)', async () => {
    const h = harness();
    h.capsAnswers.push(caps({ requests: false, live_tv: true, dvr: true }));
    h.quotaAnswers.push(new ApiError(400, 'BAD_REQUEST', 'invalid request id'));
    await h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);
  });

  it('asks for a failed allowance again even when requests are off', async () => {
    // An older server's flag can be off while Discover works: only the
    // quota endpoint's absence says so.
    const h = harness();
    h.capsAnswers.push(caps({ requests: false, live_tv: true, dvr: true }));
    h.quotaAnswers.push(new ApiError(500, 'INTERNAL', 'boom'));
    await h.loader.ensure();
    expect(h.gates()).toEqual({ discover: false, liveTv: true, recordings: true });

    h.quotaAnswers.push(new ApiError(400, 'BAD_REQUEST', 'invalid request id'));
    await h.loader.ensure();
    expect(h.deps.quota).toHaveBeenCalledTimes(2);
    expect(h.deps.capabilities).toHaveBeenCalledTimes(1);
    expect(h.gates()).toEqual(ALL_OPEN);
  });

  it('asks again once the answers are NAV_GATES_MAX_AGE_MS old, keeping the pills meanwhile', async () => {
    const h = harness();
    h.capsAnswers.push(CONFIGURED);
    h.quotaAnswers.push(quota(true));
    await h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);

    h.advance(NAV_GATES_MAX_AGE_MS - 1);
    await h.loader.ensure();
    expect(h.deps.capabilities).toHaveBeenCalledTimes(1);
    expect(h.deps.quota).toHaveBeenCalledTimes(1);

    // The tuner was removed and requesting turned off for the account.
    h.advance(1);
    const c = deferred<CapabilitiesResponse>();
    h.capsAnswers.push(c);
    h.quotaAnswers.push(quota(false));
    const load = h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);
    c.resolve(REVIEW_SERVER);
    await load;
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
    expect(h.deps.capabilities).toHaveBeenCalledTimes(2);
    expect(h.deps.quota).toHaveBeenCalledTimes(2);

    // Fresh again.
    await h.loader.ensure();
    expect(h.deps.capabilities).toHaveBeenCalledTimes(2);
  });

  it('keeps the known pills when a refresh fails, and asks again on the next call', async () => {
    const h = harness();
    h.capsAnswers.push(CONFIGURED);
    h.quotaAnswers.push(quota(true));
    await h.loader.ensure();

    h.advance(NAV_GATES_MAX_AGE_MS);
    h.capsAnswers.push(new Error('Network error: offline'));
    h.quotaAnswers.push(new ApiError(503, 'ERROR', 'unavailable'));
    const from = h.seen.length;
    await h.loader.ensure();
    // Never closed, not even for a moment.
    expect(h.seen.slice(from).every((g) => g.discover && g.liveTv && g.recordings)).toBe(true);
    expect(h.gates()).toEqual(ALL_OPEN);

    // Not fresh yet: the next call asks again.
    h.capsAnswers.push(OLDER);
    h.quotaAnswers.push(quota(false));
    await h.loader.ensure();
    expect(h.gates()).toEqual({ discover: false, liveTv: true, recordings: true });
    expect(h.deps.capabilities).toHaveBeenCalledTimes(3);
  });

  it('loads nothing while signed out', async () => {
    const h = harness(null);
    await h.loader.ensure();
    expect(h.deps.capabilities).not.toHaveBeenCalled();
    expect(h.deps.quota).not.toHaveBeenCalled();
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
  });

  it('closes every gate on reset (sign-out), and the next sign-in loads its own', async () => {
    const h = harness();
    h.capsAnswers.push(CONFIGURED);
    h.quotaAnswers.push(quota(true));
    await h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);

    h.loader.reset();
    h.signInAs(null);
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);

    // The reviewer signs in on the same server.
    h.signInAs('tv.example reviewer');
    h.capsAnswers.push(REVIEW_SERVER);
    h.quotaAnswers.push(quota(false));
    await h.loader.ensure();
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
  });

  it("drops an answer that lands after a reset (the last user's)", async () => {
    const h = harness();
    const c = deferred<CapabilitiesResponse>();
    const q = deferred<RequestQuota>();
    h.capsAnswers.push(c);
    h.quotaAnswers.push(q);
    const load = h.loader.ensure();
    h.loader.reset();
    c.resolve(CONFIGURED);
    q.resolve(quota(true));
    await load;
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
  });

  it('forgets the answers when the user or the server changes without a sign-out', async () => {
    const h = harness('a.example u1');
    h.capsAnswers.push(CONFIGURED);
    h.quotaAnswers.push(quota(true));
    await h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);

    // Another user: closed at once, before their answers come back.
    h.signInAs('a.example reviewer');
    const q = deferred<RequestQuota>();
    h.capsAnswers.push(REVIEW_SERVER);
    h.quotaAnswers.push(q);
    const load = h.loader.ensure();
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
    q.resolve(quota(false));
    await load;
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);

    // Another server.
    h.signInAs('b.example u1');
    h.capsAnswers.push(OLDER);
    h.quotaAnswers.push(new ApiError(404, 'NOT_FOUND', 'resource not found'));
    await h.loader.ensure();
    expect(h.gates()).toEqual(ALL_OPEN);
    expect(h.deps.capabilities).toHaveBeenCalledTimes(3);
  });
});

// ── The player's online subtitle search ────────────────────────────────

describe('online subtitle search gate', () => {
  const withSubs = (on?: boolean) =>
    ({ features: { requests: true, live_tv: true, dvr: true, live_tv_configured: false, subtitles_external: on } }) as unknown as CapabilitiesResponse;

  it('opens only for subtitles_external true, closed while loading and on failure', () => {
    expect(onlineSubtitlesVisible(withSubs(true))).toBe(true);
    // The review server: no OpenSubtitles key.
    expect(onlineSubtitlesVisible(withSubs(false))).toBe(false);
    // A server older than the field.
    expect(onlineSubtitlesVisible(withSubs(undefined))).toBe(false);
    expect(onlineSubtitlesVisible(undefined)).toBe(false);
    expect(onlineSubtitlesVisible(null)).toBe(false);
  });

  it('follows the loaded capabilities, and closes on reset', async () => {
    const h = harness();
    const c = deferred<CapabilitiesResponse>();
    h.capsAnswers.push(c);
    h.quotaAnswers.push(quota(false));
    const load = h.loader.ensure();
    expect(get(h.loader.onlineSubtitles)).toBe(false);
    c.resolve(withSubs(true));
    await load;
    expect(get(h.loader.onlineSubtitles)).toBe(true);
    // The pills are the reviewer's either way.
    expect(h.gates()).toEqual(NAV_GATES_CLOSED);
    h.loader.reset();
    expect(get(h.loader.onlineSubtitles)).toBe(false);
  });

  it('stays closed when capabilities fail to load', async () => {
    const h = harness();
    h.capsAnswers.push(new Error('Network error: offline'));
    h.quotaAnswers.push(quota(true));
    await h.loader.ensure();
    expect(get(h.loader.onlineSubtitles)).toBe(false);
  });
});
