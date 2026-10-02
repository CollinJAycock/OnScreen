// Which optional pills the top nav (components/TopNav) shows: Discover, Live
// TV and Recordings. Each leads to a screen that only says "you can't use
// this" when the server or the account doesn't have the feature: Discover
// answers every search with "requesting is turned off for your account" for
// an account an admin barred from requesting, and Live TV / Recordings say
// "No channels configured" on a server without a tuner. The web app hides
// the same links (web/src/lib/featureGates.ts); unlike it, admins get no
// exception here, since the TV can't set either feature up.
//
// A pill appears once the server has said it applies, never on a guess:
// while the answers load, and when a load fails, the gate stays closed, so a
// pill may pop in but never flashes up and disappears. Only the menu
// changes; a route reached another way (#/discover) still opens. The
// player's online subtitle search follows the same capabilities answer
// (onlineSubtitlesGate).
//
// The rules are pure and tested; the loading is a small class over injected
// deps, with the app's one instance below. The answers belong to one server
// and one account: they're dropped when the stored sign-in is cleared
// (sign-out, Forget / Change server, a rejected refresh), and a load for a
// different server or user starts over. webOS keeps the app resident for
// days, so known answers are asked again once they're NAV_GATES_MAX_AGE_MS
// old (a tuner added, a TMDB key set, requesting granted), the pills staying
// as they are until the new answers are in.

import { derived, get, writable, type Readable } from 'svelte/store';
import { ApiError, api } from './api/client';
import {
  requests,
  system,
  type CapabilitiesResponse,
  type RequestQuota,
} from './api/endpoints';

/** The server's capabilities: undefined while loading, null when the load
 *  failed. */
export type CapsState = CapabilitiesResponse | null | undefined;

/** The server has no GET /requests/quota (older than per-account request
 *  permissions), so every account may request when the server has requests
 *  on, as it was then. */
export const NO_QUOTA_ENDPOINT = 'no-quota-endpoint';

/** The account's request allowance: undefined while loading, null when the
 *  load failed, NO_QUOTA_ENDPOINT on an older server, else its answer. */
export type QuotaState = RequestQuota | typeof NO_QUOTA_ENDPOINT | null | undefined;

export interface NavGates {
  discover: boolean;
  liveTv: boolean;
  recordings: boolean;
}

/** Every gated pill hidden: before anything is known, after a reset. */
export const NAV_GATES_CLOSED: NavGates = { discover: false, liveTv: false, recordings: false };

// ── Rules ──────────────────────────────────────────────────────────────

/** Discover: the server has requests on (a TMDB key) and the account may
 *  request. A server without the quota endpoint (v2.4 and older) shows it
 *  whatever its features.requests says, as 0.2.0 did: that flag read only
 *  TMDB_API_KEY there, so it was false for a key set in Settings (the usual
 *  way) or the bundled one while Discover worked. */
export function discoverVisible(caps: CapsState, quota: QuotaState): boolean {
  if (!caps) return false;
  if (quota === NO_QUOTA_ENDPOINT) return true;
  if (caps.features?.requests !== true) return false;
  return !!quota && quota.can_request === true;
}

/** Live TV: at least one enabled tuner is set up (live_tv_configured). A
 *  server older than that field falls back to live_tv, which only says the
 *  subsystem is built in: the pill showed on those before. */
export function liveTvVisible(caps: CapsState): boolean {
  const f = caps?.features;
  if (!f) return false;
  if (typeof f.live_tv_configured === 'boolean') return f.live_tv_configured;
  return f.live_tv === true;
}

/** Recordings: as Live TV (recordings come off the tuners), and the server
 *  has DVR. */
export function recordingsVisible(caps: CapsState): boolean {
  return liveTvVisible(caps) && caps?.features?.dvr === true;
}

/** The player's Subtitles > "Find more online…" (not a pill, the same
 *  answer): the server has an online subtitle provider set up
 *  (features.subtitles_external). Without one its search answers 503 "not
 *  configured". Closed while capabilities load and when they failed. */
export function onlineSubtitlesVisible(caps: CapsState): boolean {
  return caps?.features?.subtitles_external === true;
}

export function navGatesFor(caps: CapsState, quota: QuotaState): NavGates {
  return {
    discover: discoverVisible(caps, quota),
    liveTv: liveTvVisible(caps),
    recordings: recordingsVisible(caps),
  };
}

/** Whether a failed quota load means the server has no quota endpoint. A
 *  server from before it routes /requests/quota to /requests/{id}, which
 *  answers 400 ("invalid request id"); one without requests at all, 404. */
export function isMissingQuotaEndpoint(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 404 || e.status === 400);
}

// ── Loading ────────────────────────────────────────────────────────────

/** How long known answers hold before the next load asks again. */
export const NAV_GATES_MAX_AGE_MS = 10 * 60 * 1000;

export interface NavGateDeps {
  /** Whose answers these are: the server and the signed-in user, null when
   *  signed out (nothing loads, every gate stays closed). */
  identity(): string | null;
  capabilities(): Promise<CapabilitiesResponse>;
  quota(): Promise<RequestQuota>;
  /** The clock (ms), for NAV_GATES_MAX_AGE_MS. */
  now(): number;
}

interface GateState {
  caps: CapsState;
  quota: QuotaState;
}

const UNKNOWN: GateState = { caps: undefined, quota: undefined };

export class NavGateLoader {
  private readonly state = writable<GateState>(UNKNOWN);
  /** The pills to show, recomputed whenever an answer lands or a reset
   *  forgets them. */
  readonly gates: Readable<NavGates> = derived(this.state, (s) => navGatesFor(s.caps, s.quota));
  /** Whether the player offers the online subtitle search. */
  readonly onlineSubtitles: Readable<boolean> = derived(this.state, (s) => onlineSubtitlesVisible(s.caps));
  private readonly deps: NavGateDeps;
  // The identity the stored answers belong to.
  private owner: string | null = null;
  private inFlight: Promise<void> | null = null;
  // Bumped by reset so a load that started before a sign-out (or for the
  // last server) can't store its answer afterwards.
  private generation = 0;
  // When a load last got every answer it asked for (deps.now); 0 = never.
  private checkedAt = 0;

  constructor(deps: NavGateDeps) {
    this.deps = deps;
  }

  /** Loads what isn't known yet, and both answers again once they're
   *  NAV_GATES_MAX_AGE_MS old. Concurrent callers share one load, whose
   *  answers land together (the bar changes once, not once per answer). A
   *  failed answer keeps what was known, closed when nothing was, and is
   *  asked again on the next call. A different server or user than the
   *  stored answers belong to forgets those first. */
  ensure(): Promise<void> {
    const who = this.deps.identity();
    if (who !== this.owner) {
      this.reset();
      this.owner = who;
    }
    if (!who) return Promise.resolve();
    if (this.inFlight) return this.inFlight;

    const s = get(this.state);
    const stale = this.checkedAt > 0 && this.deps.now() - this.checkedAt >= NAV_GATES_MAX_AGE_MS;
    const wantCaps = stale || !s.caps;
    // Asked even when capabilities say requests are off: without the quota
    // endpoint (an older server) Discover shows regardless.
    const wantQuota = stale || !s.quota;
    if (!wantCaps && !wantQuota) return Promise.resolve();

    const gen = this.generation;
    // What this load got; undefined = that load failed.
    const got: GateState = { caps: undefined, quota: undefined };
    const loads: Promise<void>[] = [];
    if (wantCaps) {
      loads.push(
        this.deps.capabilities().then(
          (caps) => {
            got.caps = caps ?? undefined;
          },
          () => undefined,
        ),
      );
    }
    if (wantQuota) {
      loads.push(
        this.deps.quota().then(
          (quota) => {
            got.quota = quota ?? undefined;
          },
          (e) => {
            if (isMissingQuotaEndpoint(e)) got.quota = NO_QUOTA_ENDPOINT;
          },
        ),
      );
    }
    const load = Promise.all(loads).then(() => {
      if (gen !== this.generation) return;
      this.inFlight = null;
      const cur = get(this.state);
      this.state.set({
        caps: wantCaps ? (got.caps ?? cur.caps ?? null) : cur.caps,
        quota: wantQuota ? (got.quota ?? cur.quota ?? null) : cur.quota,
      });
      const complete = (!wantCaps || got.caps !== undefined) && (!wantQuota || got.quota !== undefined);
      if (complete) this.checkedAt = this.deps.now();
    });
    this.inFlight = load;
    return load;
  }

  /** Forgets every answer and closes every gate: the stored sign-in was
   *  cleared, or the server changed. */
  reset(): void {
    this.generation++;
    this.inFlight = null;
    this.owner = null;
    this.checkedAt = 0;
    this.state.set(UNKNOWN);
  }
}

/** The server's host name, so the API client's http → https upgrade of the
 *  stored address (adoptTlsUpgrade: same host) keeps the answers. */
function serverHost(origin: string): string {
  try {
    return new URL(origin).hostname.toLowerCase();
  } catch {
    return origin;
  }
}

const loader = new NavGateLoader({
  identity: () => {
    const origin = api.getOrigin();
    if (!origin || !api.getToken()) return null;
    return `${serverHost(origin)} ${api.getUser()?.user_id ?? ''}`;
  },
  // Public: no sign-in needed, but asked only for a signed-in user above.
  capabilities: () => system.capabilities(),
  quota: () => requests.quota(),
  now: () => Date.now(),
});

// The stored sign-in was cleared (sign-out, Forget / Change server, a
// rejected refresh): the pills go at once, and the next sign-in's top nav
// asks for its own.
api.onSignedOut(() => loader.reset());

/** The app's gates (TopNav reads them). */
export const navGates: Readable<NavGates> = loader.gates;

/** "Find more online…" in the player's Subtitles picker (the player calls
 *  ensureNavGates when it opens). */
export const onlineSubtitlesGate: Readable<boolean> = loader.onlineSubtitles;

/** Load the gates for the signed-in user: TopNav on every mount, the layout
 *  when the app comes back to the front or the network returns, the hub
 *  after a load that worked. A no-op while they're known and fresh. */
export function ensureNavGates(): Promise<void> {
  return loader.ensure();
}

/** Forget the gates (tests; the app's are reset by api.onSignedOut above). */
export function resetNavGates(): void {
  loader.reset();
}
