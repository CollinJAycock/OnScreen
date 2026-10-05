// The server's per-user event stream (GET /api/v1/notifications/stream, SSE),
// one connection for the whole app. Android keeps one shared SSE too
// (NotificationsRepository) and fans it out: the player's cross-device
// progress sync (`progress.updated`), the admin "stop this stream"
// (`playback.stop`) and "play on this TV" (`playback.transfer`). Here the
// player owned the only EventSource, opened when it started and closed when
// it left, so a transfer sent while the TV sat on the hub (the usual case)
// was never seen.
//
// Started by the root layout while signed in, stopped on the signed-out
// screens (sign-out, forget server and every Unauthorized lead there) and when
// the stored tokens are cleared. Subscribers register per event type and get
// back an unsubscribe. The decisions (parsing, the transfer match, when to
// stream, when a reconnect refreshes the tokens) are pure and tested; the
// connection is a small class over injected deps with a runtime singleton
// below.

import { api, type RefreshOutcome } from './api/client';
import type { NotificationEvent } from './api/types';

export const PROGRESS_UPDATED_EVENT = 'progress.updated';
export const PLAYBACK_TRANSFER_EVENT = 'playback.transfer';

// ── Pure helpers ───────────────────────────────────────────────────────

/** The stream URL. EventSource can't send an Authorization header, so the
 *  purpose=asset token goes in `?token=`: the server mounts the stream under
 *  RequiredAllowQueryToken, which rejects a general access token in a URL,
 *  and the asset token can't be replayed as a Bearer. */
export function streamUrl(origin: string, assetToken: string): string {
  return `${origin.replace(/\/$/, '')}/api/v1/notifications/stream?token=${encodeURIComponent(assetToken)}`;
}

/** One SSE `data:` payload as an event, or null when it isn't one. */
export function parseEvent(raw: unknown): NotificationEvent | null {
  if (typeof raw !== 'string' || raw === '') return null;
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!v || typeof v !== 'object') return null;
  const e = v as { type?: unknown };
  return typeof e.type === 'string' && e.type !== '' ? (v as NotificationEvent) : null;
}

/** Routes that mean "signed out": the stream has nobody to stream for there.
 *  Every Unauthorized in the app lands on /login, sign-out too; forgetting
 *  the server lands on /setup; /pair is the other way to sign in. */
const SIGNED_OUT_ROUTES = new Set(['/login', '/setup', '/pair']);

/** Whether the app should hold the stream open on route `routeId` (the
 *  SvelteKit route id, e.g. '/watch/[id]'). */
export function streamWanted(routeId: string | null | undefined, signedIn: boolean): boolean {
  if (!signedIn) return false;
  return !routeId || !SIGNED_OUT_ROUTES.has(routeId);
}

export interface PlaybackTransfer {
  itemId: string;
  positionMs: number;
  targetClientName: string;
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** A `playback.transfer` event's payload (server: TransferPayload
 *  { item_id, position_ms, target_client_name }), or null when it isn't a
 *  usable one. The item id goes into a route, so it must be a UUID (the
 *  server only sends those; Android validates its deep link the same way). */
export function parseTransfer(evt: NotificationEvent | null | undefined): PlaybackTransfer | null {
  if (!evt || evt.type !== PLAYBACK_TRANSFER_EVENT) return null;
  const d = evt.data as Record<string, unknown> | undefined;
  if (!d || typeof d !== 'object') return null;
  const itemId = d.item_id;
  const target = d.target_client_name;
  if (typeof itemId !== 'string' || !UUID_RE.test(itemId)) return null;
  if (typeof target !== 'string' || target === '') return null;
  const pos = d.position_ms;
  const positionMs = typeof pos === 'number' && Number.isFinite(pos) && pos > 0 ? Math.floor(pos) : 0;
  return { itemId, positionMs, targetClientName: target };
}

/** Is this transfer for this TV? The channel is per USER, so every one of the
 *  user's apps receives every transfer; only the one whose client name (the
 *  name its heartbeats carry, device.ts clientName()) is the target acts.
 *  Exact match, as on Android: "Play on Living Room TV" from a phone must
 *  not also start the bedroom TV. */
export function isTransferForMe(evt: NotificationEvent | null | undefined, myClientName: string): boolean {
  const t = parseTransfer(evt);
  return !!t && !!myClientName && t.targetClientName === myClientName;
}

/** Hidden longer than the server's keepalive interval (30 s), the app's
 *  connection may not have survived: a TV in standby keeps the app resident
 *  while its network sleeps, and a NAT that dropped the idle mapping leaves
 *  the socket half-open, silent, with no error ever (EventSource can't see
 *  the keepalive comments to time out against, as Android's watchdog does). */
export const REDIAL_AFTER_HIDDEN_MS = 30_000;

/** Whether coming back to the foreground after `hiddenMs` should redial. */
export function shouldRedialAfterHidden(hiddenMs: number): boolean {
  return Number.isFinite(hiddenMs) && hiddenMs > REDIAL_AFTER_HIDDEN_MS;
}

/** The server's AssetTokenTTL (internal/auth/tokens.go): how long the
 *  stream's `?token=` opens it for, from when it was minted. */
export const ASSET_TOKEN_TTL_MS = 24 * 60 * 60_000;
/** "Near expiry": within this of the TTL. */
export const ASSET_TOKEN_EXPIRY_MARGIN_MS = 60 * 60_000;

/** Whether the stored asset token, minted at `issuedAtMs` by this TV's
 *  clock, is near or past its expiry at `nowMs`. Unknown is not near: no
 *  record (a token stored by an older build), or a clock now behind the
 *  record (a TV that lost the time). */
export function assetTokenNearExpiry(issuedAtMs: number | null, nowMs: number): boolean {
  if (issuedAtMs === null || !Number.isFinite(issuedAtMs) || !Number.isFinite(nowMs)) return false;
  const age = nowMs - issuedAtMs;
  return age >= 0 && age >= ASSET_TOKEN_TTL_MS - ASSET_TOKEN_EXPIRY_MARGIN_MS;
}

export interface RedialState {
  /** A connection opened in this run (since start or restart): the stored
   *  asset token has been seen to work. */
  openedThisRun: boolean;
  /** This run already tried a refresh while the stored asset token was the
   *  one stored now: it got no verdict, or it landed and the stream still
   *  wouldn't open. */
  refreshAttemptedOnThisToken: boolean;
  /** When the stored asset token was minted is known (stored by this build). */
  tokenAgeKnown: boolean;
  /** The stored asset token is known to be near or past its expiry. */
  tokenNearExpiry: boolean;
  /** The stored asset token isn't the one the failed dial used: something
   *  else (an API call's 401) refreshed meanwhile. */
  tokenChangedSinceDial: boolean;
  /** The browser says there is no network. */
  offline: boolean;
}

/**
 * Whether to refresh the tokens before dialling again. EventSource never
 * says what status a failed connection got, so an expired asset token's 401
 * looks like any drop. And every refresh rotates the refresh token: a
 * rotation whose response is lost leaves this TV holding a retired token,
 * and the server reads presenting it as theft and signs the user out on
 * every device (2026-09-30). A TV waking from standby is exactly where that
 * happens (the dial goes out before the Wi-Fi is back), so only when expiry
 * is the likely cause:
 * - the token is known to be near or past its expiry;
 * - its age is unknown (stored by an older build) and nothing has opened in
 *   this run. A token known to be fresh that doesn't open isn't expired:
 *   the network or the server is the problem, and a rotation can't fix it.
 * Never while offline, and at most once per stored token per run: a refresh
 * with no verdict isn't retried until the token changes or a new run starts
 * (a wake, the network coming back). A stream that was open and dropped
 * redials with the stored token, and a token another refresh stored since
 * the failed dial is tried first.
 */
export function shouldRefreshBeforeRedial(s: RedialState): boolean {
  if (s.offline || s.tokenChangedSinceDial || s.refreshAttemptedOnThisToken) return false;
  if (s.tokenNearExpiry) return true;
  return !s.tokenAgeKnown && !s.openedThisRun;
}

// ── The connection ─────────────────────────────────────────────────────

export type EventListener = (evt: NotificationEvent) => void;

/** The part of EventSource this uses. */
export interface EventSourceLike {
  onmessage: ((ev: { data: unknown }) => void) | null;
  onopen: ((ev?: unknown) => void) | null;
  onerror: ((ev?: unknown) => void) | null;
  close(): void;
}

export interface EventStreamDeps {
  /** The server origin; null before setup. */
  origin(): string | null;
  /** The stored purpose=asset token; null when signed out. */
  assetToken(): string | null;
  /** When that token was minted (this TV's clock), null when unknown. */
  assetTokenIssuedAt(): number | null;
  /** Rotate the tokens through the client's one refresh in flight (a fresh
   *  asset token for the reconnect). See RefreshOutcome. */
  refresh(): Promise<RefreshOutcome>;
  /** False when the browser knows there is no network (navigator.onLine). */
  online(): boolean;
  now(): number;
  connect(url: string): EventSourceLike;
  setTimer(fn: () => void, ms: number): unknown;
  clearTimer(handle: unknown): void;
}

export const RECONNECT_MIN_MS = 1_000;
export const RECONNECT_MAX_MS = 30_000;
/** A connection that stayed open this long (one keepalive interval) was
 *  healthy, and its drop is retried promptly again. Resetting on open alone
 *  redialled about once a second for good against a proxy or a restarting
 *  server that accepts the stream and then closes it at once. */
export const STABLE_OPEN_MS = 30_000;

/**
 * One EventSource, reconnected by hand. EventSource retries a drop on its own
 * but with the same URL, and the asset token in it can expire (the server
 * then answers 401 forever): on an error the connection is closed and it
 * reconnects after a backoff of 1 s doubling to 30 s, reset once a
 * connection has stayed open for STABLE_OPEN_MS, with the stored asset
 * token, refreshed first only when
 * shouldRefreshBeforeRedial says so. A refresh the server rejects (the
 * sign-in is dead: revoked, expired, signed out everywhere) stops the stream
 * and tells onRejected's listeners; one that got no verdict (offline, 503)
 * keeps the tokens and retries on the backoff.
 */
export class EventStream {
  private readonly listeners = new Map<string, Set<EventListener>>();
  private readonly rejectedListeners = new Set<() => void>();
  private source: EventSourceLike | null = null;
  private running = false;
  private timer: unknown = null;
  private delay = RECONNECT_MIN_MS;
  // Bumped by stop(): a refresh still out for a stopped run must not dial.
  private gen = 0;
  // This run's history, for shouldRefreshBeforeRedial.
  private openedThisRun = false;
  private refreshAttemptToken: string | null = null;
  private dialedToken: string | null = null;
  // When the current connection opened (deps.now()), null until it does.
  private openedAt: number | null = null;

  constructor(private readonly deps: EventStreamDeps) {}

  /** Wanted open (connected, or waiting to reconnect). */
  get active(): boolean {
    return this.running;
  }

  /** Open the stream; a no-op while it is already wanted open. */
  start(): void {
    if (this.running) return;
    this.running = true;
    this.delay = RECONNECT_MIN_MS;
    this.openedThisRun = false;
    this.refreshAttemptToken = null;
    this.dial();
  }

  /** Close it, and stop reconnecting. */
  stop(): void {
    this.running = false;
    this.gen++;
    if (this.timer !== null) {
      this.deps.clearTimer(this.timer);
      this.timer = null;
    }
    const s = this.source;
    this.source = null;
    try {
      s?.close();
    } catch {
      /* already closed */
    }
  }

  /** Close and dial again now, if it is wanted open: another user signed in
   *  (a connection authenticated as the previous one would keep delivering
   *  their events), or the TV woke from standby or got its network back (a
   *  connection from before can be half-open, silent, with no error ever). */
  restart(): void {
    if (!this.running) return;
    this.stop();
    this.start();
  }

  /** Listen for one event type; returns the unsubscribe. */
  subscribe(type: string, fn: EventListener): () => void {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
    }
    set.add(fn);
    return () => {
      this.listeners.get(type)?.delete(fn);
    };
  }

  /** Told when a refresh for a reconnect came back as the server's verdict
   *  that the sign-in is dead. The stream has stopped by then, and the
   *  client has dropped the stored tokens unless a newer sign-in replaced
   *  them. Returns the unsubscribe. */
  onRejected(fn: () => void): () => void {
    this.rejectedListeners.add(fn);
    return () => {
      this.rejectedListeners.delete(fn);
    };
  }

  /** Hand an event to its type's listeners. One listener throwing doesn't
   *  keep it from the others. */
  dispatch(evt: NotificationEvent): void {
    const set = this.listeners.get(evt.type);
    if (!set) return;
    for (const fn of [...set]) {
      try {
        fn(evt);
      } catch (e) {
        console.warn('[events] listener failed', evt.type, e);
      }
    }
  }

  private dial(): void {
    const origin = this.deps.origin();
    const tok = this.deps.assetToken();
    if (!origin || !tok) {
      // Signed out (or an older server without asset tokens): nothing to
      // authenticate with. The next sign-in starts it again.
      this.stop();
      return;
    }
    this.dialedToken = tok;
    this.openedAt = null;
    let es: EventSourceLike;
    try {
      es = this.deps.connect(streamUrl(origin, tok));
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.source = es;
    es.onmessage = (ev) => {
      if (this.source !== es) return;
      const evt = parseEvent(ev.data);
      if (evt) this.dispatch(evt);
    };
    es.onopen = () => {
      if (this.source !== es) return;
      this.openedThisRun = true;
      this.openedAt = this.deps.now();
    };
    es.onerror = () => {
      if (this.source !== es) return;
      try {
        es.close();
      } catch {
        /* already closed */
      }
      this.source = null;
      // Healthy for a while: the drop retries promptly, with this token.
      if (this.openedAt !== null && this.deps.now() - this.openedAt >= STABLE_OPEN_MS) {
        this.delay = RECONNECT_MIN_MS;
      }
      this.openedAt = null;
      this.scheduleReconnect();
    };
  }

  private scheduleReconnect(): void {
    if (!this.running || this.timer !== null) return;
    const delay = this.delay;
    this.delay = Math.min(delay * 2, RECONNECT_MAX_MS);
    const gen = this.gen;
    this.timer = this.deps.setTimer(() => {
      this.timer = null;
      if (!this.running || gen !== this.gen) return;
      const redial = () => {
        if (this.running && gen === this.gen && !this.source) this.dial();
      };
      const stored = this.deps.assetToken();
      const issuedAt = this.deps.assetTokenIssuedAt();
      const refresh = shouldRefreshBeforeRedial({
        openedThisRun: this.openedThisRun,
        refreshAttemptedOnThisToken: stored !== null && stored === this.refreshAttemptToken,
        tokenAgeKnown: issuedAt !== null,
        tokenNearExpiry: assetTokenNearExpiry(issuedAt, this.deps.now()),
        tokenChangedSinceDial: stored !== this.dialedToken,
        offline: !this.deps.online(),
      });
      if (!refresh) {
        redial();
        return;
      }
      this.refreshAttemptToken = stored;
      // A refresh that got no verdict still dials with what is stored, and
      // that dial's error reschedules (without refreshing that token again).
      this.deps.refresh().then((outcome) => {
        if (outcome === 'rejected') {
          this.rejected(gen);
          return;
        }
        redial();
      }, redial);
    }, delay);
  }

  // The refresh's verdict: the sign-in is dead, and no reconnect can open.
  // Stop, rather than redial every 30 s for good. Unless a newer run owns the
  // stream (restarted under a sign-in made since): the verdict was on the
  // token before it. A run stopped meanwhile still tells: the client clearing
  // the dead tokens is what stopped it.
  private rejected(gen: number): void {
    if (this.running && gen !== this.gen) return;
    this.stop();
    for (const fn of [...this.rejectedListeners]) {
      try {
        fn();
      } catch (e) {
        console.warn('[events] rejected listener failed', e);
      }
    }
  }
}

// ── Runtime ────────────────────────────────────────────────────────────

/** The app's stream. */
export const events = new EventStream({
  origin: () => api.getOrigin(),
  assetToken: () => api.getAssetToken(),
  assetTokenIssuedAt: () => api.getAssetTokenIssuedAt(),
  // The client's single-flight refresh, shared with every API call's 401.
  refresh: () => api.refreshTokensOutcome(),
  online: () => typeof navigator === 'undefined' || navigator.onLine !== false,
  now: () => Date.now(),
  connect: (url) => new EventSource(url) as unknown as EventSourceLike,
  setTimer: (fn, ms) => setTimeout(fn, ms),
  clearTimer: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
});
