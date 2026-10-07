/**
 * OnScreen API client — wraps fetch with httpOnly cookie auth and
 * standard error handling. Tokens live in httpOnly cookies (set by the
 * server); localStorage holds only non-secret user metadata for UI routing.
 *
 * Same-origin (`/api/v1`) when served from the Go binary's embedded
 * webui (browser, default). When running inside the Tauri desktop
 * client the user picks their server URL at first launch via the
 * setup gate; that URL is read once on module load and cached.
 * `setApiBase()` is exposed so the startup flow can refresh BASE
 * after a URL change without a full reload.
 */

// Value import is safe: playback-decision only type-imports from api (erased at
// compile), so there's no runtime import cycle.
import { clientCapabilitiesHeader } from './playback-decision';

let apiBase = '/api/v1';

/** Override the API base — Tauri startup flow calls this with the
 *  user's configured `<serverUrl>/api/v1` once the URL is known. */
export function setApiBase(url: string) {
  apiBase = url;
}

// In-memory bearer cache for the native client. Cookie-based auth
// doesn't survive cross-origin (Tauri webview → remote OnScreen) on
// plain-http servers because cookies need SameSite=None;Secure.
// Bearer over Authorization is the universal path.
//
// Mutated by setBearerToken on login/refresh + on startup hydration.
// Browser builds never set this; the same-origin cookie path keeps
// working unchanged.
let bearerToken: string | null = null;
let refreshTokenStore: string | null = null;
// The purpose=asset token. Cross-origin clients put THIS (never the
// general bearer) in `?token=` on asset routes — the server rejects a
// general token in a URL. Browser builds leave it null and use cookies.
let assetTokenStore: string | null = null;

/** Set or clear the in-memory tokens. The Tauri shell also persists
 *  them to its store, but this in-memory copy is what the fetch
 *  wrapper / assetUrl read on every call so the persistence layer
 *  isn't on the hot path. `asset` is the purpose=asset token used by
 *  assetUrl for cross-origin `?token=`. */
export function setBearerToken(
  access: string | null,
  refresh: string | null = null,
  asset: string | null = null,
) {
  bearerToken = access;
  refreshTokenStore = refresh;
  assetTokenStore = asset;
}

/** Read the cached bearer token. Used by the native audio engine
 *  wiring to send Authorization on Rust-initiated HTTP fetches
 *  (cpal pipeline can't read api.ts internals through fetch since
 *  it owns its own ureq client). Returns null in browser builds
 *  where bearer auth isn't used. */
export function getBearerToken(): string | null { return bearerToken; }

/** Read the cached purpose=asset token. Exposed for the native audio /
 *  now-playing wiring that builds artwork URLs Rust-side (it can't read
 *  assetUrl's module state through fetch). Null in browser builds. */
export function getAssetToken(): string | null { return assetTokenStore; }

/** Read the configured API base. Used by the native audio engine
 *  wiring to construct absolute media URLs the Rust-side ureq
 *  client can fetch directly — same-origin paths from api.ts are
 *  meaningless to a Rust HTTP client. */
export function getApiBase(): string { return apiBase; }

/** Resolve a server asset path (`/artwork/...`, `/media/stream/...`,
 *  `/media/subtitles/...`) to a URL a browser asset element can load.
 *
 *  **Pass the COMPLETE path including any query string the asset
 *  endpoint expects** — the helper appends `?token=` (or `&token=`)
 *  at the END, so callers that concatenate their own `?w=300` after
 *  the helper's return value would corrupt the URL. Use template
 *  literals: `assetUrl(`/artwork/${path}?v=${ver}&w=300`)`, never
 *  `assetUrl('/artwork/'+path) + '?w=300'`.
 *
 *  - Same-origin browser builds (apiBase = "/api/v1"): returns the
 *    path unchanged so the browser hits the same origin (Go server
 *    or its embedded webui in production, Vite proxy in dev). The
 *    httpOnly auth cookie attaches automatically — no token needed
 *    in the URL.
 *  - Cross-origin (Tauri client pointed at any server): prepends
 *    the configured server origin AND appends `&token=<asset>` (or
 *    `?token=` if no other params) using the purpose=asset token —
 *    NOT the general bearer. The server rejects a general access
 *    token in `?token=` (a general-API credential must never travel
 *    in a URL that lands in logs / Referer / history); the asset
 *    token authenticates the read-only asset routes and can't be
 *    replayed as a Bearer. If no asset token is cached (talking to a
 *    pre-asset-token server, or before the first login/refresh) the
 *    URL is returned without a token — the request will 401 until an
 *    asset token is available, which is the safe failure.
 *
 *  Caller-supplied tokens (e.g. transcode segment tokens) take
 *  precedence — the helper doesn't overwrite an existing `token=`
 *  parameter.
 */
/** Produces a stable per-installation client_name string the server
 *  uses to attribute scrobbles to a specific device + populate the
 *  cross-device "Play on…" picker. Format:
 *    Web — Chrome on Windows
 *    Desktop — Windows  (Tauri builds set onscreen_client_kind to "Desktop")
 *  Stored in localStorage so a user who renames the entry via a
 *  future settings UI has their choice persist across reloads. */
export function getClientName(): string {
  if (typeof window === 'undefined') return 'Web';
  const stored = localStorage.getItem('onscreen_client_name');
  if (stored && stored.trim() !== '') return stored;
  const kind = localStorage.getItem('onscreen_client_kind') ?? 'Web';
  const ua = navigator.userAgent;
  // Best-effort extraction — order matters because UA strings
  // contain multiple browser identifiers (Chrome's UA also
  // contains "Safari" and "Mozilla"); pick the most-specific
  // engine first.
  const browser =
    /Edg\//.test(ua) ? 'Edge' :
    /OPR\//.test(ua) ? 'Opera' :
    /Firefox/.test(ua) ? 'Firefox' :
    /Chrome/.test(ua) ? 'Chrome' :
    /Safari/.test(ua) ? 'Safari' :
    'Browser';
  const os =
    /Windows/.test(ua) ? 'Windows' :
    /Mac OS|Macintosh/.test(ua) ? 'macOS' :
    /Linux/.test(ua) ? 'Linux' :
    /Android/.test(ua) ? 'Android' :
    /iPad|iPhone/.test(ua) ? 'iOS' :
    'Desktop';
  return `${kind} — ${browser} on ${os}`;
}

export function assetUrl(path: string): string {
  if (apiBase.startsWith('/')) return path;
  const base = apiBase.replace(/\/api\/v1\/?$/, '') + path;
  if (!assetTokenStore || /[?&]token=/.test(base)) return base;
  return base + (base.includes('?') ? '&' : '?') + 'token=' + encodeURIComponent(assetTokenStore);
}

/** Build the request headers, attaching Authorization when a bearer
 *  token is cached. Browser builds skip the bearer (cookies cover
 *  auth there). */
function authHeaders(): Record<string, string> {
  const h: Record<string, string> = { 'Content-Type': 'application/json' };
  if (bearerToken) h.Authorization = `Bearer ${bearerToken}`;
  // Declarative playback capability profile (see docs/capability-profiles.md).
  // Memoized + browser-only; '' in SSR so the header is simply omitted and the
  // server falls back to its safe defaults.
  const caps = clientCapabilitiesHeader();
  if (caps) h['X-Client-Capabilities'] = caps;
  return h;
}

/** Pick the right credentials mode for a request.
 *
 *  - Same-origin (browser embed): 'same-origin' so cookies attach.
 *  - Tauri (always cross-origin, always bearer-mode): 'omit'.
 *    Bearer is in Authorization, no cookies needed, and 'omit'
 *    avoids the Access-Control-Allow-Credentials class of CORS
 *    errors entirely — preflight passes against any origin on
 *    the server's allowlist without the operator having to opt
 *    into credentialled CORS. Login (which runs before the
 *    bearer cache is hydrated) gets the same treatment because
 *    isTauri() is true the whole session.
 *  - Cross-origin browser (rare — e.g. embedded TV apps):
 *    'include' for cookie auth; server must echo
 *    Access-Control-Allow-Credentials: true.
 */
function credentialsMode(): RequestCredentials {
  if (apiBase.startsWith('/')) return 'same-origin';
  // Lazy require to avoid a static dep on the native helper at
  // module-load time — keeps the browser bundle clean and
  // sidesteps any future circularity if native.ts grows api.ts
  // imports.
  if (typeof window !== 'undefined' && (window as Window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__) {
    return 'omit';
  }
  return 'include';
}

/** Persist new tokens via the Tauri shell, no-op in the browser.
 *  Fire-and-forget — failure to persist is non-fatal because the
 *  in-memory bearer is already updated. */
async function persistTokensIfTauri(access: string, refresh: string, asset: string = ''): Promise<void> {
  // Same-origin browser: the httpOnly cookies set by this same response ARE
  // the credentials, shared by every tab. A per-tab in-memory copy goes stale
  // the moment another tab rotates the pair, and the server prefers a body
  // refresh_token / Authorization header over the cookies — so this tab would
  // later spend the superseded refresh token: reuse, which deletes every
  // session of the user. Cache only where cookies can't reach the server
  // (Tauri, cross-origin embeds); browser builds keep the cache null, as
  // getBearerToken/getAssetToken document.
  if (!apiBase.startsWith('/')) setBearerToken(access, refresh, asset || null);
  try {
    const { isTauri, setStoredTokens } = await import('./native');
    if (isTauri()) await setStoredTokens(access, refresh, asset);
  } catch {
    // Tauri not present or store IPC unavailable — in-memory tokens
    // still work for this session.
  }
}

// ── Cross-tab refresh serialization ───────────────────────────────────────────
//
// Refresh tokens are one-shot: /auth/refresh rotates the token, and the server
// treats a superseded token coming back as theft and deletes EVERY session of
// the user (web, phone, TVs). In browser mode the refresh token is an httpOnly
// cookie shared by every tab of the origin, while ApiClient.refreshPromise only
// dedupes within one tab — so two tabs whose access tokens expire together
// would both POST the same cookie, and the second POST is "reuse" that signs
// the user out everywhere. The helpers below make the refresh a cross-tab
// critical section.
//
// Primary: the Web Locks API. Fallback, because navigator.locks exists only in
// secure contexts (self-hosters commonly open http://<LAN-IP>:7070) and not in
// older browsers: a best-effort localStorage lease.
//
// A tab never POSTs merely because it got tired of waiting. The holder's POST
// is capped at REFRESH_POST_TIMEOUT_MS — a hung one fails like a network
// error, which frees the lock for the next tab — and waiters wait longer than
// that. A wait that still runs out means the holder may be mid-POST with the
// very cookie this tab would send, so the waiter takes a refresh that landed
// meanwhile or else fails its own: the request that needed it fails, the tab
// stays signed in and the cookies are untouched. One failed request beats a
// reuse that signs the user out on every device.
//
// Cookie-mode sign-out runs in the same critical section (logoutAcrossTabs):
// a logout that overtakes another tab's refresh would revoke nothing and be
// undone by that refresh's Set-Cookie. It leaves a sign-out marker, so tabs
// queued behind it don't mistake an earlier refresh for a jar that still
// holds cookies (see REFRESHED_AT_KEY).
//
// What no ordering between tabs can prevent: a POST the server processed but
// whose response never arrived (a dropped connection, a tab closed mid-POST,
// or the timeout above) leaves the jar holding the superseded cookie, so the
// next refresh from any tab is reuse. That exposure is the same with or
// without these helpers.

const REFRESH_LOCK_NAME = 'onscreen-auth-refresh';
/** Rewritten by every successful refresh in any tab. A tab that queued behind
 *  another and sees it changed knows the cookie jar already holds a fresh pair
 *  and skips its own POST. That saves a rotation; it is not what prevents
 *  reuse (the lock is) — a second refresh after the first one sends the
 *  already-ROTATED cookie, which the server accepts.
 *  A cookie-mode sign-out writes it too, with a LOGOUT_MARKER_PREFIX value
 *  that says the opposite: the jar is being emptied. A waiter that finds one
 *  POSTs after all, and the server's 401 to the empty jar signs that tab out
 *  as well. Without it, a queue of refresh (tab C), sign-out (tab A), refresh
 *  (tab B) would show B the marker C changed; B would skip its POST, retry
 *  against the jar A just emptied, get a plain 401 and stay looking signed
 *  in. A marker that vanished (site data cleared) is no refresh either. */
const REFRESHED_AT_KEY = 'onscreen_auth_refreshed_at';
const LOGOUT_MARKER_PREFIX = 'logout:';
const REFRESH_LEASE_KEY = 'onscreen_auth_refresh_lease';
/** Cap on the POST made inside the critical section. Only a live tab can hold
 *  a Web Lock (closing or crashing the tab releases it), so a holder whose
 *  fetch hangs would otherwise keep every other tab waiting; the abort makes
 *  it fail like a network error, which releases the lock.
 *  The abort is itself a way to end up presenting a superseded cookie, and
 *  nothing client-side can prevent that: it ends this tab's fetch, not the
 *  server's work. If the server still processes the refresh, it rotates the
 *  token, the Set-Cookie carrying the new one is dropped with the aborted
 *  response, the jar keeps the old cookie, and the next refresh from any tab
 *  presents it — reuse. The cap trades that (a server more than 20 s late
 *  with a refresh) for never wedging every tab behind one hung fetch. */
const REFRESH_POST_TIMEOUT_MS = 20_000;
/** Fallback lease lifetime. Outlives the POST timeout, so a holder's POST has
 *  ended before its lease can lapse (see REFRESH_LEASE_MARGIN_MS); also how
 *  long a tab closed mid-refresh, which never releases its lease, blocks the
 *  others. */
const REFRESH_LEASE_MS = 30_000;
/** A lease claim is acted on only while it has at least the POST timeout plus
 *  this much life left. The settle below can stretch to seconds in a
 *  timer-throttled background tab, and a claim that aged meanwhile could lapse
 *  mid-POST — letting another tab claim the lease and POST the same cookie. */
const REFRESH_LEASE_MARGIN_MS = 5_000;
/** A real claim never expires more than REFRESH_LEASE_MS from now, while the
 *  clock holds still. A lease whose exp lies further ahead than that by more
 *  than this slack was left under a clock that has since been set back a long
 *  way, or is corrupt, and counts as lapsed: honouring it would wedge every
 *  tab's refresh until its bogus exp.
 *  A whole lease lifetime of slack, because a small step back (an NTP
 *  correction, a manual adjustment) during a live holder's POST pushes its
 *  lease just as far ahead — and a waiter that took that for bogus would
 *  claim the lease and POST the same cookie: reuse, which signs the user out
 *  on every device. The price is that a bogus lease inside the window is
 *  honoured until it lapses, at most REFRESH_LEASE_MS + this (~60 s), and
 *  waiters meanwhile time out and fail their request without posting
 *  (REFRESH_WAIT_MS). A step back of more than this during a POST still
 *  reads as bogus. */
const REFRESH_LEASE_CLOCK_SLACK_MS = REFRESH_LEASE_MS;
/** How long a tab waits for its turn: longer than a live holder can take (the
 *  POST timeout) and than a dead holder's lease lasts (unless a clock set back
 *  stretched it; see REFRESH_LEASE_CLOCK_SLACK_MS). A wait that runs out
 *  anyway never ends in a refresh POST — see refreshAcrossTabs. */
const REFRESH_WAIT_MS = 35_000;
const REFRESH_LEASE_POLL_MS = 100;
/** Pause between claiming the lease and reading it back (see
 *  acquireRefreshLease). */
const REFRESH_LEASE_SETTLE_MS = 50;

// localStorage can be missing (SSR) or throw on access (storage blocked,
// Safari private-mode quota). Coordination then degrades to "none" — the
// pre-lock behaviour — rather than crashing the refresh.
function storageGet(key: string): string | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage.getItem(key);
  } catch {
    return null;
  }
}

/** False when the write didn't happen (no storage, or it threw). */
function storageSet(key: string, value: string): boolean {
  try {
    if (typeof localStorage === 'undefined') return false;
    localStorage.setItem(key, value);
    return true;
  } catch {
    return false;
  }
}

function storageRemove(key: string): void {
  try {
    if (typeof localStorage !== 'undefined') localStorage.removeItem(key);
  } catch { /* best-effort */ }
}

/** Unique-enough id for the marker and the lease owner. Not
 *  crypto.randomUUID: that is secure-context-only too, and the fallback exists
 *  precisely for insecure contexts. Uniqueness is all that's needed here. */
function uniqueToken(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2);
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** What a caller brings to the cross-tab critical section. */
interface AuthCriticalSection<T> {
  /** The work, run while this tab holds the lock. Its signal aborts after
   *  REFRESH_POST_TIMEOUT_MS. */
  run: (signal: AbortSignal) => Promise<T>;
  /** Runs instead of `run` when this tab's wait for its turn ran out. */
  onWaitTimeout: () => Promise<T>;
  /** Lease fallback only: stop waiting as soon as this holds, then `run`
   *  (which checks for itself what that means). A queued Web Lock request
   *  simply waits for its grant. */
  stopWaiting?: () => boolean;
}

/** Run `cs` as the cross-tab auth critical section: under the Web Lock, or
 *  under the localStorage lease where the Locks API is missing. */
async function withAuthLock<T>(cs: AuthCriticalSection<T>): Promise<T> {
  const locks = typeof navigator === 'undefined' ? undefined : navigator.locks;
  if (locks && typeof locks.request === 'function') return withAuthWebLock(locks, cs);
  return withAuthLease(cs);
}

/** Run `post` with a signal that aborts after REFRESH_POST_TIMEOUT_MS. */
async function withPostTimeout<T>(post: (signal: AbortSignal) => Promise<T>): Promise<T> {
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), REFRESH_POST_TIMEOUT_MS);
  try {
    return await post(abort.signal);
  } finally {
    clearTimeout(timer);
  }
}

/** Run `refresh` as the cross-tab critical section, handing it a signal that
 *  aborts after REFRESH_POST_TIMEOUT_MS. Resolves true WITHOUT calling it when
 *  another tab completed a refresh while this one queued (and no sign-out
 *  came after it); rejects without calling it when this tab's wait ran out
 *  and no such refresh landed; otherwise resolves or rejects exactly as
 *  `refresh` does. */
async function refreshAcrossTabs(refresh: (signal: AbortSignal) => Promise<boolean>): Promise<boolean> {
  // Snapshot BEFORE queueing: a change seen once we hold the lock means some
  // tab refreshed in the meantime, and its Set-Cookie is already in the jar
  // (cookies are stored before that tab's fetch even resolved) — unless the
  // latest change is a sign-out's, which empties the jar (REFRESHED_AT_KEY).
  // Posting when unsure is safe: under the lock, the cookie sent is current.
  const markerBefore = storageGet(REFRESHED_AT_KEY);
  const refreshedMeanwhile = () => {
    const marker = storageGet(REFRESHED_AT_KEY);
    return marker !== null && marker !== markerBefore && !marker.startsWith(LOGOUT_MARKER_PREFIX);
  };
  return withAuthLock({
    run: async (signal) => {
      if (refreshedMeanwhile()) return true;
      const ok = await refresh(signal);
      // Published while still holding the lock, so the next tab in line sees it.
      if (ok) storageSet(REFRESHED_AT_KEY, uniqueToken());
      return ok;
    },
    // The wait for another tab's refresh ran out. That tab may still be
    // mid-POST with the cookie this one would send, and a second POST of it
    // is reuse — so never go ahead: take its refresh if one landed meanwhile,
    // otherwise fail like a network error does.
    onWaitTimeout: async () => {
      if (refreshedMeanwhile()) return true;
      throw new Error('auth refresh: timed out waiting for another tab');
    },
    stopWaiting: refreshedMeanwhile,
  });
}

/** Cookie-mode sign-out as the same critical section. A logout that overtakes
 *  another tab's refresh of the same cookie sends the pre-rotation cookie: the
 *  server finds nothing to delete under it, and the refresh's response then
 *  lands its Set-Cookie and signs the browser back in. Holding the lock, the
 *  logout carries whatever the last refresh left in the jar.
 *  No marker shortcut for the logout itself: a refresh that landed while this
 *  queued is exactly the session to revoke. And it publishes a sign-out
 *  marker, never a refresh's, so a tab queued behind it POSTs its refresh,
 *  finds the cleared jar and fails it — which signs that tab out too — even
 *  when another tab's refresh changed the marker while it queued. The marker
 *  goes out before the POST, so a lease-fallback waiter polling meanwhile
 *  keeps waiting (one that polls between the earlier refresh's release and
 *  this claim still takes that refresh, and its retry races the logout), and
 *  whether or not the POST works: all it can cost a waiter is a rotation.
 *  A wait that runs out POSTs anyway: a logout can't trip reuse detection (a
 *  superseded cookie just finds nothing to delete), and a sign-out that never
 *  reaches the server leaves the session alive. */
async function logoutAcrossTabs(logout: (signal: AbortSignal) => Promise<void>): Promise<void> {
  const run = (signal: AbortSignal) => {
    storageSet(REFRESHED_AT_KEY, LOGOUT_MARKER_PREFIX + uniqueToken());
    return logout(signal);
  };
  return withAuthLock({ run, onWaitTimeout: () => withPostTimeout(run) });
}

async function withAuthWebLock<T>(locks: LockManager, cs: AuthCriticalSection<T>): Promise<T> {
  // A Web Lock is released when the callback settles (throws included) and
  // when the holding tab closes or crashes, so a dead tab cannot keep it; a
  // live one is bounded by the POST timeout around cs.run. The abort here
  // only cancels a still-queued request, never a lock already granted.
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), REFRESH_WAIT_MS);
  let granted = false;
  try {
    return await locks.request(REFRESH_LOCK_NAME, { signal: abort.signal }, () => {
      granted = true;
      clearTimeout(timer);
      return withPostTimeout(cs.run);
    });
  } catch (e) {
    // The work itself failed (network error, POST timeout) — propagate.
    if (granted) throw e;
    if (abort.signal.aborted) return await cs.onWaitTimeout();
    // The Locks API refused the request (e.g. an opaque origin): coordinate
    // through the lease instead, which in turn goes uncoordinated only where
    // storage is unusable too.
    return await withAuthLease(cs);
  } finally {
    clearTimeout(timer);
  }
}

/** Fallback when navigator.locks is unavailable. BEST-EFFORT: localStorage has
 *  no atomic compare-and-set, so two tabs that both read "no lease" before
 *  either one's claim is visible to the other both claim it. The read-back
 *  after a settle in acquireRefreshLease resolves those (last writer wins,
 *  the other goes back to waiting) unless a claim reaches the other tab more
 *  slowly than the settle. Lease expiry and the POST timeout run on timers
 *  and the tab's clock, so a heavily throttled background tab can still
 *  outrun them; the margin check narrows that, it cannot close it. */
async function withAuthLease<T>(cs: AuthCriticalSection<T>): Promise<T> {
  const owner = uniqueToken();
  const lease = await acquireRefreshLease(owner, cs.stopWaiting);
  if (lease === 'timeout') return await cs.onWaitTimeout();
  try {
    // 'stopped' (another tab's refresh landed): cs.run sees that too and
    // skips its POST.
    return await withPostTimeout(cs.run);
  } finally {
    if (lease === 'held') releaseRefreshLease(owner);
  }
}

/** Remove the lease if it is still this owner's claim. One that lapsed and
 *  was taken over by another tab is not ours to remove. */
function releaseRefreshLease(owner: string): void {
  if (readRefreshLease()?.owner === owner) storageRemove(REFRESH_LEASE_KEY);
}

function readRefreshLease(): { owner: string; exp: number } | null {
  const raw = storageGet(REFRESH_LEASE_KEY);
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as { owner?: unknown; exp?: unknown };
    return typeof v.owner === 'string' && typeof v.exp === 'number' ? { owner: v.owner, exp: v.exp } : null;
  } catch {
    return null;
  }
}

/** Whether another tab's lease still holds: not yet expired, and no further
 *  ahead than a real claim can be (see REFRESH_LEASE_CLOCK_SLACK_MS). */
function refreshLeaseLive(lease: { exp: number }): boolean {
  const now = Date.now();
  return lease.exp > now && lease.exp <= now + REFRESH_LEASE_MS + REFRESH_LEASE_CLOCK_SLACK_MS;
}

/** Waits, at most REFRESH_WAIT_MS, until this tab holds the lease ('held').
 *  Stops early when stopWaiting() holds ('stopped': another tab's refresh
 *  landed) or when storage won't hold a lease ('uncoordinated': every tab of
 *  the origin is in the same spot, so there is nothing to coordinate with —
 *  the pre-lease behaviour). 'timeout' means another tab kept the lease the
 *  whole time. Every exit but 'held' withdraws this tab's claim if one from a
 *  round that didn't take is still standing: left behind, it would hold off
 *  every other tab until it lapsed. */
async function acquireRefreshLease(
  owner: string,
  stopWaiting?: () => boolean,
): Promise<'held' | 'stopped' | 'uncoordinated' | 'timeout'> {
  const deadline = Date.now() + REFRESH_WAIT_MS;
  let outcome: 'stopped' | 'uncoordinated' | 'timeout' = 'timeout';
  while (Date.now() < deadline) {
    if (stopWaiting?.()) {
      outcome = 'stopped';
      break;
    }
    const lease = readRefreshLease();
    if (lease && lease.owner !== owner && refreshLeaseLive(lease)) {
      await sleep(REFRESH_LEASE_POLL_MS);
      continue;
    }
    // Free, lapsed because its tab died mid-refresh, bogus, or our own claim
    // from a round that didn't take: claim it, then read it back after a
    // beat. A tab that claimed in the same instant overwrote one of the two
    // writes; only the surviving owner proceeds.
    if (!storageSet(REFRESH_LEASE_KEY, JSON.stringify({ owner, exp: Date.now() + REFRESH_LEASE_MS }))) {
      outcome = 'uncoordinated';
      break;
    }
    await sleep(REFRESH_LEASE_SETTLE_MS);
    const now = readRefreshLease();
    // Proceed only on our own claim with life left for the whole POST.
    // Otherwise go round again: another tab's claim won; the lease vanished
    // (a finishing holder's release raced our claim — the stopWaiting check
    // at the top sees its refresh); or the beat stretched in a throttled tab
    // and our claim aged — claim afresh (if it lapsed meanwhile, the new
    // claim races any other tab's through this same read-back).
    if (now?.owner === owner && now.exp - Date.now() >= REFRESH_POST_TIMEOUT_MS + REFRESH_LEASE_MARGIN_MS) {
      return 'held';
    }
  }
  releaseRefreshLease(owner);
  return outcome;
}

interface ApiResponse<T> {
  data: T;
}

interface ApiListResponse<T> {
  data: T[];
  meta: { total: number; cursor: string };
}

interface ApiError {
  error: {
    code: string;
    message: string;
    request_id: string;
  };
}

/** Error thrown by the API client on a non-2xx response. Carries the HTTP
 *  status and the server's error `code` so callers can branch on a specific
 *  condition (e.g. a PARENTAL_LIMIT 403 raised when a child starts playback
 *  outside their allowed hours or past their daily cap) instead of having to
 *  string-match the message. */
export class ApiRequestError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
  ) {
    super(message);
    this.name = 'ApiRequestError';
  }
}

/** ApiRequestError code (status 401) for a request that hit a 401 and could
 *  not get the session renewed for a reason that says nothing about the
 *  tokens — the server unreachable or failing, or another tab's refresh
 *  taking too long. The user stays signed in; retrying later can succeed. */
export const REFRESH_UNAVAILABLE_CODE = 'REFRESH_UNAVAILABLE';

export interface UserMeta {
  user_id: string;
  username: string;
  is_admin: boolean;
}

export class ApiClient {
  // Dedupes refreshes within this tab (refreshSession); tryRefresh serializes
  // across tabs.
  private refreshPromise: Promise<boolean> | null = null;

  /** Store non-secret user metadata for UI routing. */
  setUser(meta: UserMeta | null) {
    if (meta) {
      localStorage.setItem('onscreen_user', JSON.stringify(meta));
    } else {
      localStorage.removeItem('onscreen_user');
    }
  }

  /** Read stored user metadata, or null if not logged in. */
  getUser(): UserMeta | null {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) return null;
    try { return JSON.parse(raw); } catch { return null; }
  }

  /**
   * Per-tab admin "view as" override. When set in sessionStorage, every
   * GET request gets `view_as=<userId>` appended so the server's
   * impersonation middleware substitutes the target user's claims.
   * Per-tab (sessionStorage) — admin's own tabs aren't affected, and
   * closing the impersonating tab clears the override.
   */
  private withViewAs(method: string, path: string): string {
    if (method !== 'GET' || typeof sessionStorage === 'undefined') return path;
    const targetId = sessionStorage.getItem('onscreen_view_as');
    if (!targetId) return path;
    const sep = path.includes('?') ? '&' : '?';
    return `${path}${sep}view_as=${encodeURIComponent(targetId)}`;
  }

  /**
   * Shared 401-retry wrapper.  Calls `doFetch` to get the response, then
   * `parseResponse` to turn it into the caller's desired shape.  On a 401
   * it attempts a single silent token refresh; only a refresh the server
   * rejected redirects to login.
   */
  private async requestWithRetry<T>(
    path: string,
    doFetch: () => Promise<Response>,
    parseResponse: (resp: Response) => Promise<T>,
    retry = true
  ): Promise<T> {
    const resp = await doFetch();

    if (resp.status === 401 && retry) {
      let refreshed: boolean;
      try {
        refreshed = await this.refreshSession();
      } catch {
        // The refresh got no verdict on the tokens: a network error, the
        // POST timeout, a timed-out wait for another tab's refresh, or the
        // server answering 5xx/429 (see postRefresh). They may well be fine,
        // so fail just this request — as the ApiRequestError pages already
        // show as an error — and keep the user signed in: no teardown, no
        // redirect. The teardown below would also clear the shared
        // onscreen_user under every other tab of the origin.
        throw new ApiRequestError(
          'Could not renew your session. Check your connection and try again.',
          401,
          REFRESH_UNAVAILABLE_CODE,
        );
      }
      if (refreshed) {
        return this.requestWithRetry(path, doFetch, parseResponse, false);
      }
      // Refresh rejected → tokens are dead (server-side session purge,
      // logout-on-another-device bumped session_epoch, refresh token
      // expiry, or the keychain hydrated with values from a server
      // that no longer recognises them). Tear everything down so the
      // user lands at a clean /login:
      //   1. Clear in-memory bearer cache so subsequent calls don't
      //      retry with the dead token.
      //   2. Wipe localStorage user metadata so the layout doesn't
      //      flash a "logged in" UI before the redirect.
      //   3. Native (Tauri) path: drop the stored refresh + access
      //      tokens from the keychain. Without this, the next app
      //      launch re-hydrates the dead tokens and lands the user
      //      right back here — the bug that motivated this commit.
      setBearerToken(null, null);
      this.setUser(null);
      if (typeof window !== 'undefined') {
        // Fire-and-forget — `clearStoredTokens` is a native-only IPC
        // and the import is dynamic so the browser bundle doesn't
        // pull in the Tauri shim. Failures (no Tauri / IPC denied)
        // are non-fatal: the in-memory + localStorage clear above
        // is enough to break the retry loop in this session.
        import('./native').then(({ isTauri, clearStoredTokens }) => {
          if (isTauri()) void clearStoredTokens();
        }).catch(() => {});
      }
      window.location.href = '/login';
      return undefined as T;
    }

    return parseResponse(resp);
  }

  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
    retry = true,
    signal?: AbortSignal,
  ): Promise<T> {
    const finalPath = this.withViewAs(method, path);
    return this.requestWithRetry(
      finalPath,
      () => fetch(apiBase + finalPath, {
        method,
        headers: authHeaders(),
        credentials: credentialsMode(),
        body: body ? JSON.stringify(body) : undefined,
        signal,
      }),
      async (resp) => {
        if (resp.status === 204) return undefined as T;
        const json = (await resp.json()) as ApiResponse<T> | ApiError;
        if (!resp.ok) {
          const err = json as ApiError;
          throw new ApiRequestError(
            err.error?.message ?? `HTTP ${resp.status}`,
            resp.status,
            err.error?.code ?? '',
          );
        }
        return (json as ApiResponse<T>).data;
      },
      retry,
    );
  }

  async requestList<T>(path: string): Promise<{ items: T[]; total: number }> {
    const finalPath = this.withViewAs('GET', path);
    return this.requestWithRetry(
      finalPath,
      () => fetch(apiBase + finalPath, {
        method: 'GET',
        headers: authHeaders(),
        credentials: credentialsMode()
      }),
      async (resp) => {
        const json = await resp.json();
        if (!resp.ok) {
          const err = json as ApiError;
          throw new ApiRequestError(
            err.error?.message ?? `HTTP ${resp.status}`,
            resp.status,
            err.error?.code ?? '',
          );
        }
        const envelope = json as ApiListResponse<T>;
        return { items: envelope.data ?? [], total: envelope.meta?.total ?? 0 };
      }
    );
  }

  /** Refresh the session outside the 401 path — the SSO-callback bootstrap
   *  uses it to learn who just signed in. Same route as the 401 retry: joins
   *  a refresh already running in this tab, and in cookie mode runs as the
   *  cross-tab critical section. Resolves true once the session is fresh
   *  (refreshed here, or by another tab while this one queued — either way
   *  getUser() now holds the refreshed user), false only when the server
   *  rejected the tokens (401/403: they are dead); throws when the refresh
   *  got no verdict on them — a network error, a POST timeout, a timed-out
   *  wait for another tab, or a 5xx/429 answer. Never tears down or
   *  redirects: that is the caller's call.
   *  `signal` (a bearer-mode sign-out's POST cap) aborts the bearer-mode
   *  POST this call starts. A refresh this tab already had running is joined
   *  as it is: it wasn't begun with the signal, and giving up on it could
   *  let it land after the sign-out and store its pair and user again. */
  refreshSession(signal?: AbortSignal): Promise<boolean> {
    if (!this.refreshPromise) {
      this.refreshPromise = this.tryRefresh(signal).finally(() => {
        this.refreshPromise = null;
      });
    }
    return this.refreshPromise;
  }

  private tryRefresh(signal?: AbortSignal): Promise<boolean> {
    // Bearer (native) mode posts THIS window's in-memory refresh token, so
    // there is no shared credential to serialize — and another window's
    // refresh wouldn't have updated our copy, which makes the "another tab
    // already refreshed" shortcut wrong there. Cookie mode shares one refresh
    // cookie across every tab of the origin: cross-tab critical section, which
    // also caps the POST (REFRESH_POST_TIMEOUT_MS) so it can't hold the lock
    // forever.
    if (refreshTokenStore) return this.postRefresh(signal);
    return refreshAcrossTabs((postSignal) => this.postRefresh(postSignal));
  }

  private async postRefresh(signal?: AbortSignal): Promise<boolean> {
    // Only the server rejecting the tokens (401, or 403) returns false;
    // the caller treats that as "tokens are dead" and tears down.
    // Everything that says nothing about the tokens throws, so the
    // caller fails just the request at hand and keeps them for the
    // next try: a network error or abort, a 5xx (the server couldn't
    // check the token, and leaves the cookies alone when it says so),
    // a 429, or any other unexpected answer. Without this distinction
    // a flaky LAN or a restarting server would log a user out after
    // one failed refresh, even though their tokens are perfectly valid.
    let resp: Response;
    try {
      // Browser path: refresh token is in an httpOnly cookie scoped
      // to /api/v1/auth — sent automatically. Native path: post the
      // stored refresh token in the body (the endpoint accepts both
      // — see auth.go's refresh handler) since cookies don't survive
      // cross-origin from the Tauri webview.
      const body: Record<string, string> = {};
      if (refreshTokenStore) {
        body.refresh_token = refreshTokenStore;
      }
      resp = await fetch(apiBase + '/auth/refresh', {
        method: 'POST',
        headers: authHeaders(),
        credentials: credentialsMode(),
        body: refreshTokenStore ? JSON.stringify(body) : undefined,
        // The POST timeout (see REFRESH_POST_TIMEOUT_MS): every cookie-mode
        // refresh, and a bearer-mode sign-out's rotation (revokeSession).
        // Also bounds the body read below: aborted there, it lands in the
        // unreadable-body branch.
        signal,
      });
    } catch (e) {
      // Network error / DNS failure / fetch aborted — propagate so
      // the caller's catch keeps the user logged in for retry. The
      // /api/v1 hub poll on app foreground will re-trigger refresh
      // when the network comes back.
      throw e;
    }
    if (resp.status === 401 || resp.status === 403) {
      // Refresh token rejected: tokens dead. The keychain clear in
      // the caller means the user starts clean at /login rather than
      // re-loading a stale token.
      return false;
    }
    if (!resp.ok) {
      throw new ApiRequestError(`auth refresh: HTTP ${resp.status}`, resp.status, REFRESH_UNAVAILABLE_CODE);
    }
    try {
      const json = (await resp.json()) as ApiResponse<TokenPair>;
      const pair = json.data;
      // Update stored user metadata + bearer cache. The persistent
      // store update is fire-and-forget — if it fails we still have
      // the in-memory copy and the user can re-login next launch.
      this.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
      void persistTokensIfTauri(pair.access_token, pair.refresh_token, pair.asset_token ?? '');
      return true;
    } catch (e) {
      // A 2xx whose body didn't arrive whole or didn't parse (the POST
      // timeout firing mid-read included). The server most likely
      // rotated the token. Cookie mode: its Set-Cookie already put the
      // new pair in the jar, so the session is probably fine — no
      // verdict, throw. Bearer mode: the new pair was only in this
      // body, and the refresh token we hold is superseded; sending it
      // again would be reuse, which signs the user out everywhere. To
      // us it is dead: false, and the caller's tear-down path runs.
      if (refreshTokenStore) return false;
      throw e;
    }
  }

  /** Revoke the current session on the server (best-effort; throws when the
   *  server can't be reached or, in bearer mode, can't rotate the token
   *  first). Cookie (browser) mode: the refresh cookie rides the POST, sent
   *  inside the cross-tab critical section refreshes run in (see
   *  logoutAcrossTabs). Bearer (native) mode has no cookie, so the refresh
   *  token must go in the body. Current servers revoke a body token even
   *  when the bearer has expired; older ones only honoured it alongside a
   *  VALID bearer (an expired one was treated as anonymous: still 204,
   *  nothing revoked), so bearer mode rotates first to guarantee a fresh
   *  bearer, then revokes the rotated refresh token — unless the server
   *  rejects the rotation, which means the token is dead already. Sent to
   *  the current apiBase, i.e. the server that issued the token — callers
   *  must run this BEFORE changing or clearing the server URL.
   *  Every POST this starts is capped at REFRESH_POST_TIMEOUT_MS, as the
   *  cookie-mode one is inside the critical section: a hung one fails like a
   *  network error instead of holding the sign-out forever. An abort can't
   *  cost a reuse here — authApi.logout discards the tokens right after —
   *  only the revocation. The one wait it doesn't cap is on a refresh this
   *  window already had running (see refreshSession); the layout's sign-out
   *  bounds that. */
  async revokeSession(): Promise<void> {
    if (!refreshTokenStore) {
      await logoutAcrossTabs((signal) => this.request('POST', '/auth/logout', undefined, false, signal));
      return;
    }
    if (credentialsMode() !== 'omit') {
      // A cross-origin browser embed holding its own token copy: its
      // refreshes don't take the cross-tab lock (tryRefresh), so there is
      // nothing for the logout to wait out.
      await withPostTimeout((signal) => this.request('POST', '/auth/logout', undefined, false, signal));
      return;
    }
    // Joins a refresh this window already has running rather than spending
    // the same refresh token twice (reuse).
    if (!(await withPostTimeout((signal) => this.refreshSession(signal))) || !refreshTokenStore) return;
    await withPostTimeout((signal) => fetch(apiBase + '/auth/logout', {
      method: 'POST',
      headers: authHeaders(),
      credentials: credentialsMode(),
      body: JSON.stringify({ refresh_token: refreshTokenStore }),
      signal,
    }));
  }

  get = <T>(path: string) => this.request<T>('GET', path);
  post = <T>(path: string, body?: unknown) => this.request<T>('POST', path, body);

  /** POST a file as the raw request body, typed by the blob (an image
   *  upload). Same auth, retry and error handling as the JSON calls. */
  async postBlob<T>(path: string, blob: Blob): Promise<T> {
    const finalPath = this.withViewAs('POST', path);
    return this.requestWithRetry(
      finalPath,
      () => fetch(apiBase + finalPath, {
        method: 'POST',
        headers: { ...authHeaders(), 'Content-Type': blob.type || 'application/octet-stream' },
        credentials: credentialsMode(),
        body: blob,
      }),
      async (resp) => {
        if (resp.status === 204) return undefined as T;
        const json = (await resp.json()) as ApiResponse<T> | ApiError;
        if (!resp.ok) {
          const err = json as ApiError;
          throw new ApiRequestError(err.error?.message ?? `HTTP ${resp.status}`, resp.status, err.error?.code ?? '');
        }
        return (json as ApiResponse<T>).data;
      },
    );
  }
  /** POST that does NOT attempt the 401→refresh→logout cascade. For
   *  pre-session auth steps (TOTP verify) where a 401 means "wrong
   *  code, retry", not "session dead, bounce to /login". */
  postNoRefresh = <T>(path: string, body?: unknown) => this.request<T>('POST', path, body, false);
  put = <T>(path: string, body?: unknown) => this.request<T>('PUT', path, body);
  patch = <T>(path: string, body?: unknown) => this.request<T>('PATCH', path, body);
  del = (path: string, body?: unknown) => this.request<void>('DELETE', path, body);
  delete = (path: string, body?: unknown) => this.request<void>('DELETE', path, body);

  /**
   * Start or stop the per-tab impersonation override. Pass a UserMeta
   * to begin viewing as that user (banner appears, every GET request
   * carries view_as); pass null to clear and return to the admin's
   * own view. Per-tab via sessionStorage so other tabs are unaffected.
   */
  setViewAs(target: { id: string; username: string } | null) {
    if (typeof sessionStorage === 'undefined') return;
    if (target) {
      sessionStorage.setItem('onscreen_view_as', target.id);
      sessionStorage.setItem('onscreen_view_as_name', target.username);
    } else {
      sessionStorage.removeItem('onscreen_view_as');
      sessionStorage.removeItem('onscreen_view_as_name');
    }
  }

  /** Returns the currently impersonated user's id+name, or null. */
  getViewAs(): { id: string; username: string } | null {
    if (typeof sessionStorage === 'undefined') return null;
    const id = sessionStorage.getItem('onscreen_view_as');
    const username = sessionStorage.getItem('onscreen_view_as_name');
    if (!id || !username) return null;
    return { id, username };
  }
}

export const api = new ApiClient();

// ── SSO-callback bootstrap ────────────────────────────────────────────────────
//
// Every SSO/SAML/OIDC callback lands on "/?<x>_auth=1" holding fresh auth
// cookies but no onscreen_user (a server response can't write localStorage).
// The root layout refreshes to learn who signed in (api.refreshSession), and
// the home page's auth gate must not bounce to /login before that settles.
// The refresh can take a while — queued behind another tab's for up to
// REFRESH_WAIT_MS, then its own capped POST — so the gate waits on the
// layout's actual promise rather than a fixed window.

let authBootstrap: Promise<void> | null = null;

/** Longest the gate waits on a registered bootstrap. A cookie-mode refresh
 *  ends on its own before this (a bounded wait, then a capped POST); the
 *  bound is for whatever else might hang. */
const AUTH_BOOTSTRAP_MAX_WAIT_MS = REFRESH_WAIT_MS + REFRESH_POST_TIMEOUT_MS + 5_000;
/** How long the gate waits for a bootstrap to register at all — the fixed
 *  window it used to give the layout. */
const AUTH_BOOTSTRAP_START_WAIT_MS = 3_000;
const AUTH_BOOTSTRAP_POLL_MS = 100;

/** The layout registers its SSO-callback refresh here as it starts it, and
 *  gets `refresh` back to await. */
export function trackAuthBootstrap<T>(refresh: Promise<T>): Promise<T> {
  authBootstrap = refresh.then(() => undefined, () => undefined);
  return refresh;
}

/** For the home page's auth gate on an SSO-callback landing: resolves once
 *  the layout's bootstrap refresh has settled — onscreen_user then holds the
 *  signed-in user, unless the refresh failed — or after
 *  AUTH_BOOTSTRAP_MAX_WAIT_MS. With no bootstrap registered yet it polls for
 *  up to AUTH_BOOTSTRAP_START_WAIT_MS, returning early once `ready()` holds
 *  and moving on to wait for a bootstrap that registers meanwhile. */
export async function waitForAuthBootstrap(ready: () => boolean): Promise<void> {
  const startBy = Date.now() + AUTH_BOOTSTRAP_START_WAIT_MS;
  while (!authBootstrap) {
    if (ready() || Date.now() >= startBy) return;
    await sleep(AUTH_BOOTSTRAP_POLL_MS);
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  const cap = new Promise<void>((resolve) => { timer = setTimeout(resolve, AUTH_BOOTSTRAP_MAX_WAIT_MS); });
  try {
    await Promise.race([authBootstrap, cap]);
  } finally {
    clearTimeout(timer);
  }
}

/** Forget the registered bootstrap (tests only). */
export function resetAuthBootstrap(): void {
  authBootstrap = null;
}

/** Fire-and-forget JSON request that survives page unmount. Uses
 *  `keepalive: true` so the browser keeps the request inflight after
 *  the page tears down — required for the watch page's "save progress
 *  on close" hook. Bypasses the standard request wrapper because:
 *   - response is irrelevant (caller can't await it; the page is gone)
 *   - 401 retry has nowhere to redirect mid-unmount
 *  Carries the same auth as regular requests: Authorization header for
 *  Tauri/bearer mode, cookie for browser/same-origin. */
export function apiBeacon(method: 'PUT' | 'POST', path: string, body: unknown): void {
  fetch(apiBase + path, {
    method,
    headers: authHeaders(),
    credentials: credentialsMode(),
    body: JSON.stringify(body),
    keepalive: true,
  }).catch(() => {});
}

// ── Auth ──────────────────────────────────────────────────────────────────────

export interface TokenPair {
  access_token: string;
  refresh_token: string;
  /** purpose=asset token for cross-origin asset `?token=`. Optional —
   *  absent on servers that predate the asset-token work. */
  asset_token?: string;
  expires_at: string;
  user_id: string;
  username: string;
  is_admin: boolean;
  /** Set when a TOTP-enabled account cleared the password step but owes
   *  a second factor. access/refresh are empty; post the code +
   *  login_challenge_token to authApi.totp.verify to finish. */
  totp_required?: boolean;
  login_challenge_token?: string;
}

/** Capture the access + refresh + asset tokens from a successful auth
 *  call so subsequent requests carry the bearer header and asset URLs
 *  carry the asset token. Browser builds short-circuit (the in-memory
 *  cache is unread there since the cookie path covers auth). */
async function captureTokens(pair: TokenPair): Promise<TokenPair> {
  await persistTokensIfTauri(pair.access_token, pair.refresh_token, pair.asset_token ?? '');
  return pair;
}

export const authApi = {
  setupStatus: () => api.get<{ setup_required: boolean }>('/setup/status'),
  login: async (username: string, password: string) => {
    // postNoRefresh, NOT post: on the login endpoint a 401 means "wrong
    // credentials — show the error", not "session expired, silently refresh".
    // A fresh login has no session to refresh, so the generic 401 path would
    // fire /auth/refresh (also 401) and then hard-redirect to /login, wiping
    // the form before any error banner renders. NoRefresh lets the 401
    // surface as an ApiRequestError the login page can display.
    const pair = await api.postNoRefresh<TokenPair>('/auth/login', { username, password });
    // A TOTP-gated account returns no tokens yet — don't persist empties;
    // the caller drives the second-factor step and captures on verify.
    if (pair.totp_required) return pair;
    return captureTokens(pair);
  },
  /** Two-factor auth. setup/activate/disable/status are authenticated
   *  (Settings → Security); verify is the public login second step. */
  totp: {
    status: () => api.get<{ enabled: boolean; recovery_codes_remaining: number }>('/auth/totp/status'),
    setup: () => api.post<{ otpauth_url: string; secret: string }>('/auth/totp/setup'),
    activate: (code: string) => api.post<{ recovery_codes: string[] }>('/auth/totp/activate', { code }),
    disable: (code: string) => api.post<{ enabled: boolean }>('/auth/totp/disable', { code }),
    verify: async (loginChallengeToken: string, code: string) =>
      captureTokens(await api.postNoRefresh<TokenPair>('/auth/totp/verify', {
        login_challenge_token: loginChallengeToken,
        code,
      })),
  },
  register: (username: string, password: string, email?: string) =>
    api.post<{ id: string; username: string }>('/auth/register', { username, password, email }),
  logout: async () => {
    try {
      // revokeSession, not a bare POST: in bearer (Tauri) mode there is no
      // cookie, and a body-less logout revoked nothing — the 30-day refresh
      // token outlived "Sign out".
      await api.revokeSession();
    } finally {
      // Clear bearer + persisted tokens regardless of whether the
      // server-side logout succeeded — a leaked refresh token is a
      // worse outcome than a transient API error.
      setBearerToken(null, null, null);
      try {
        const { isTauri, clearStoredTokens } = await import('./native');
        if (isTauri()) await clearStoredTokens();
      } catch { /* native shell unavailable */ }
    }
  },
  oidcEnabled: () => api.get<{ enabled: boolean; display_name: string }>('/auth/oidc/enabled'),
  ldapEnabled: () => api.get<{ enabled: boolean; display_name: string }>('/auth/ldap/enabled'),
  samlEnabled: () => api.get<{ enabled: boolean; display_name: string }>('/auth/saml/enabled'),
  ldapLogin: async (username: string, password: string) =>
    // postNoRefresh for the same reason as login above: a 401 is "bad
    // credentials", not a dead session to refresh-then-redirect away.
    captureTokens(await api.postNoRefresh<TokenPair>('/auth/ldap/login', { username, password })),
  forgotPasswordEnabled: () => api.get<{ enabled: boolean }>('/auth/forgot-password/enabled'),
  forgotPassword: (email: string) => api.post<{ message: string }>('/auth/forgot-password', { email }),
  resetPassword: (token: string, password: string) => api.post<{ message: string }>('/auth/reset-password', { token, password }),
  // Native client device pairing.
  claimPair: (pin: string, device_name?: string) =>
    api.post<{ status: string; device_name: string }>('/auth/pair/claim', { pin, device_name }),
  // What the server knows about the device that requested a pending PIN, so
  // the confirmation screen can describe what is being authorised rather than
  // showing a bare six-digit number. Returns 404 for an unknown or spent code.
  pendingPair: (pin: string) =>
    api.get<{ ip?: string; user_agent?: string; requested_at?: string }>(
      `/auth/pair/pending?pin=${encodeURIComponent(pin)}`
    )
};

// ── Email (admin) ─────────────────────────────────────────────────────────────

export const emailApi = {
  enabled: () => api.get<{ enabled: boolean }>('/email/enabled'),
  sendTest: (to: string) => api.post<{ message: string }>('/email/test', { to })
};

// ── Jobs / status ─────────────────────────────────────────────────────────────

export interface JobsStatus {
  scans: { library_id: string; library_name?: string }[];
  missing_art_count: number;
  unmatched_count: number;
}

export const jobsApi = {
  get: () => api.get<JobsStatus>('/jobs')
};

// ── Capabilities (public feature-flag feed) ───────────────────────────────────

export interface CapabilitiesFeatures {
  transcode: boolean;
  trickplay: boolean;
  subtitles_external: boolean;
  subtitles_ocr: boolean;
  oidc: boolean;
  ldap: boolean;
  device_pairing: boolean;
  plugins: boolean;
  backup: boolean;
  people_credits: boolean;
  photos: boolean;
  music: boolean;
  webhooks: boolean;
  notifications: boolean;
  /** A TMDB key is configured, so Discover and requests work. Whether the
   *  signed-in account may request is per user (requestsApi.quota). */
  requests: boolean;
  /** An enabled Radarr or Sonarr feeds the Upcoming calendar. Absent on
   *  older servers. */
  upcoming?: boolean;
  /** The Live TV subsystem is wired — not that any tuner exists. */
  live_tv: boolean;
  dvr: boolean;
  /** live_tv, and at least one enabled tuner. Absent on older servers. */
  live_tv_configured?: boolean;
  lyrics: boolean;
  intro_markers: boolean;
  chapters: boolean;
  web_downloads: boolean;
}

export interface CapabilitiesResponse {
  features: CapabilitiesFeatures;
  // Other fields exist (server, codecs, limits, discovery) but the UI
  // only consumes features today; type-import what we use, ignore the rest.
}

export const capabilitiesApi = {
  get: () => api.get<CapabilitiesResponse>('/system/capabilities')
};

// ── Admin Fix Match (unmatched items tray) ────────────────────────────────────

export interface UnmatchedItem {
  id: string;
  library_id: string;
  type: 'movie' | 'show';
  title: string;
  year?: number;
}

export interface UnmatchedListResponse {
  items: UnmatchedItem[];
  total: number;
}

export const unmatchedApi = {
  list: (libraryId?: string) => {
    const qs = libraryId ? `?library_id=${encodeURIComponent(libraryId)}` : '';
    return api.get<UnmatchedListResponse>(`/admin/items/unmatched${qs}`);
  }
};

// ── Admin Missing Art (poster-fix tray) ───────────────────────────────────────

export interface MissingArtItem {
  id: string;
  library_id: string;
  type: 'movie' | 'show';
  title: string;
  year?: number;
  tmdb_id?: number;
}

export interface MissingArtListResponse {
  items: MissingArtItem[];
  total: number;
}

export const missingArtApi = {
  list: () => api.get<MissingArtListResponse>('/admin/items/missing-art')
};

// ── Report a problem (user) + Library health (admin) ─────────────────────────

export type IssueKind = 'video' | 'audio' | 'subtitles' | 'wrong_match' | 'other';
export type IssueStatus = 'open' | 'resolved' | 'dismissed';

/** A problem report as its reporter sees it. */
export interface MediaIssue {
  id: string;
  item_id: string;
  file_id?: string;
  kind: IssueKind;
  note?: string;
  status: IssueStatus;
  created_at: string;
  resolved_at?: string;
  resolution_note?: string;
}

/** One row of the admin queue. file_name is the file's base name only. */
export interface AdminMediaIssue extends MediaIssue {
  library_id: string;
  item_type: string;
  item_title: string;
  item_year?: number;
  show_title?: string;
  season_number?: number;
  episode_number?: number;
  poster_path?: string;
  reporter_id: string;
  reporter_username: string;
  resolved_by_username?: string;
  file_name?: string;
}

export interface DamagedFile {
  file_id: string;
  item_id: string;
  library_id: string;
  item_type: string;
  title: string;
  year?: number;
  show_title?: string;
  season_number?: number;
  episode_number?: number;
  path_basename: string;
  integrity_detail?: string;
  checked_at?: string;
}

export interface LibraryHealth {
  open_issues: number;
  damaged_files: DamagedFile[];
  damaged_total: number;
  unmatched_count: number;
  missing_art_count: number;
}

export interface RegrabResult {
  service_id: string;
  service_name: string;
  service_kind: 'radarr' | 'sonarr';
  command: 'MoviesSearch' | 'SeriesSearch' | 'SeasonSearch' | 'EpisodeSearch';
  command_id: number;
  arr_id: number;
  title: string;
  blocklisted: boolean;
  blocklisted_release?: string;
  /** Movie / episode only: the *arr app still has a file, so its search
   *  only grabs an upgrade. */
  has_file?: boolean;
}

export const issuesApi = {
  /** The caller's own reports on an item, newest first. */
  listMine: (itemId: string) => api.get<MediaIssue[]>(`/items/${encodeURIComponent(itemId)}/issues`),
  /** 409 ALREADY_REPORTED for a second open report of the same kind;
   *  429 TOO_MANY_OPEN_ISSUES past the per-user cap. */
  create: (itemId: string, body: { kind: IssueKind; note?: string; file_id?: string }) =>
    api.post<MediaIssue>(`/items/${encodeURIComponent(itemId)}/issues`, body),
  adminList: (params: { status?: IssueStatus | 'all'; limit?: number; offset?: number } = {}) => {
    const qs = new URLSearchParams();
    if (params.status) qs.set('status', params.status);
    if (params.limit != null) qs.set('limit', String(params.limit));
    if (params.offset != null) qs.set('offset', String(params.offset));
    const q = qs.toString();
    return api.get<{ items: AdminMediaIssue[]; total: number }>(`/admin/issues${q ? `?${q}` : ''}`);
  },
  /** Resolve or dismiss an open report; the reporter is notified. */
  close: (issueId: string, status: 'resolved' | 'dismissed', note?: string) =>
    api.patch<MediaIssue>(`/admin/issues/${encodeURIComponent(issueId)}`, note ? { status, note } : { status }),
  libraryHealth: (params: { limit?: number; offset?: number } = {}) => {
    const qs = new URLSearchParams();
    if (params.limit != null) qs.set('limit', String(params.limit));
    if (params.offset != null) qs.set('offset', String(params.offset));
    const q = qs.toString();
    return api.get<LibraryHealth>(`/admin/library-health${q ? `?${q}` : ''}`);
  },
  /** Ask Radarr/Sonarr to search the item again. 409 NOT_MANAGED when no
   *  instance manages it; 502 ARR_UNAVAILABLE when one can't be reached. */
  regrab: (itemId: string, blocklist: boolean) =>
    api.post<RegrabResult>(`/admin/items/${encodeURIComponent(itemId)}/regrab`, { blocklist }),
};

// ── Admin Maintenance (bulk one-shot operations) ──────────────────────────────

export interface ReprobeResult {
  files_cleared: number;
  libraries_queued: number;
}

export const maintenanceApi = {
  // Re-probes existing files to backfill technical metadata (frame_rate,
  // replaygain_*) that the old persist bug dropped to NULL. Clears file
  // hashes so the next scan can't fast-skip, then enqueues that scan.
  // Optional libraryId scopes it; omit for every library.
  reprobeMetadata: (libraryId?: string) => {
    const qs = libraryId ? `?library_id=${encodeURIComponent(libraryId)}` : '';
    return api.post<ReprobeResult>(`/maintenance/reprobe-metadata${qs}`);
  }
};

// ── Invites (admin) ───────────────────────────────────────────────────────────

export const inviteApi = {
  create: (email?: string) => api.post<{ invite_url: string; id: string }>('/invites', { email }),
  list: () => api.get<Array<{ id: string; email: string | null; expires_at: string; used_at: string | null; created_at: string }>>('/invites'),
  del: (id: string) => api.del(`/invites/${id}`),
  accept: (token: string, username: string, password: string) => api.post<{ message: string }>('/invites/accept', { token, username, password })
};

// ── Users (admin) ─────────────────────────────────────────────────────────────

export interface User {
  id: string;
  username: string;
  is_admin: boolean;
  created_at: string;
  // Per-user request auto-approval. Stored as set, but the server ignores
  // both while the user has a content-rating ceiling, and admins' own
  // requests are always approved regardless.
  auto_approve_movies: boolean;
  auto_approve_tv: boolean;
  // Content-rating ceiling, when the list includes it. Managed profiles'
  // ceilings are also available from profileApi.list().
  max_content_rating?: string | null;
  // False blocks the user's new requests (admins are never blocked). Older
  // servers omit it — treat absent as true.
  can_request?: boolean;
  // Per-window request limits; null = the server default applies, 0 =
  // unlimited.
  request_quota_movies?: number | null;
  request_quota_tv?: number | null;
}

/** Body of PUT /users/{id}/request-permissions. Every field is optional (at
 *  least one required); an omitted field keeps its stored value. A quota of
 *  null resets it to the server default, 0 is unlimited. */
export interface RequestPermissions {
  auto_approve_movies?: boolean;
  auto_approve_tv?: boolean;
  can_request?: boolean;
  quota_movies?: number | null;
  quota_tv?: number | null;
}

export interface SwitchableUser {
  id: string;
  username: string;
  is_admin: boolean;
  has_pin: boolean;
}

export const userApi = {
  list: () => api.requestList<User>('/users'),
  create: (username: string, password: string, email?: string) =>
    api.post<{ id: string; username: string }>('/auth/register', { username, password, email }),
  del: (id: string) => api.del(`/users/${id}`),
  resetPassword: (id: string, password: string) =>
    api.put<void>(`/users/${id}/password`, { password }),
  setAdmin: (id: string, isAdmin: boolean) =>
    api.patch<void>(`/users/${id}`, { is_admin: isAdmin }),
  setPin: (pin: string, password: string) =>
    api.put<void>('/users/me/pin', { pin, password }),
  clearPin: (password: string) =>
    api.del('/users/me/pin', { password }),
  listSwitchable: () =>
    api.get<SwitchableUser[]>('/users/switchable'),
  pinSwitch: async (userId: string, pin: string) =>
    captureTokens(await api.post<TokenPair>('/auth/pin-switch', { user_id: userId, pin })),
  getPreferences: () =>
    api.get<UserPreferences>('/users/me/preferences'),
  setPreferences: (prefs: UserPreferences) =>
    api.put<void>('/users/me/preferences', prefs),
  // Full ordered hub layout; empty array resets to the default layout.
  setHubLayout: (rows: HubRowPref[]) =>
    api.put<void>('/users/me/hub-layout', { rows }),
  setContentRating: (userId: string, maxContentRating: string | null) =>
    api.put<void>(`/users/${userId}/content-rating`, { max_content_rating: maxContentRating }),
  getLibraries: (userId: string) =>
    api.get<UserLibraryAccess[]>(`/users/${userId}/libraries`),
  setLibraries: (userId: string, libraryIds: string[]) =>
    api.put<void>(`/users/${userId}/libraries`, { library_ids: libraryIds }),
  getWatchLimit: (userId: string) =>
    api.get<WatchLimitInfo>(`/users/${userId}/watch-limit`),
  setWatchLimit: (userId: string, policy: WatchLimitPolicy) =>
    api.put<void>(`/users/${userId}/watch-limit`, policy),
  /** Admin-enforced streaming caps for the target user (null = no cap). */
  getStreamingLimits: (userId: string) =>
    api.get<StreamingLimits>(`/users/${userId}/streaming-limits`),
  setStreamingLimits: (userId: string, limits: StreamingLimits) =>
    api.put<void>(`/users/${userId}/streaming-limits`, limits),
  /** Whether the target user's movie / TV requests skip the admin queue. */
  setRequestPermissions: (userId: string, perms: RequestPermissions) =>
    api.put<void>(`/users/${userId}/request-permissions`, perms),
  /** The caller's own watch policy + today's usage + whether playback is
   *  allowed right now. Used by the player to pre-check before starting a
   *  stream so a restricted child is blocked before any content plays. */
  getMyWatchLimit: () =>
    api.get<WatchLimitInfo>('/users/me/watch-limit')
};

/** A user's parental watch policy plus today's usage and current allowed
 *  state. All three limit fields are null when unrestricted; the window
 *  bounds are minutes from local midnight in [0,1440) and are set or cleared
 *  together. remaining_minutes is present only when a daily cap is set. */
export interface StreamingLimits {
  max_concurrent_streams: number | null;
  max_stream_bitrate_kbps: number | null;
}

export interface WatchLimitInfo {
  daily_limit_minutes: number | null;
  allowed_start_minute: number | null;
  allowed_end_minute: number | null;
  used_minutes_today: number;
  remaining_minutes?: number;
  allowed: boolean;
  reason?: string;
}

/** The settable parental policy. null on a field clears that limit; the two
 *  window bounds must be set or cleared together. */
export interface WatchLimitPolicy {
  daily_limit_minutes: number | null;
  allowed_start_minute: number | null;
  allowed_end_minute: number | null;
}

/** Per-user external-scrobble status. Credentials are never returned by the
 *  server — only what's linked. *_available says whether the admin has set
 *  the service up at all; the Last.fm / Trakt fields are absent from servers
 *  that predate them. */
export interface ScrobbleStatus {
  listenbrainz_linked: boolean;
  listenbrainz_enabled: boolean;
  lastfm_available?: boolean;
  lastfm_linked?: boolean;
  lastfm_username?: string;
  trakt_available?: boolean;
  trakt_linked?: boolean;
  trakt_username?: string;
}

/** Where a Last.fm / Trakt link stands after a complete call. */
export type ScrobbleLinkStatus = 'pending' | 'slow_down' | 'linked' | 'expired' | 'denied';

export interface ScrobbleLinkResult {
  status: ScrobbleLinkStatus;
  username?: string;
}

/** Last.fm link: the user approves OnScreen at auth_url, then the page polls
 *  complete with the sealed pending handle. */
export interface LastFMLinkStart {
  auth_url: string;
  pending: string;
}

/** Trakt link (device code): the user enters user_code at verification_url,
 *  and the page polls complete every interval seconds for expires_in. */
export interface TraktLinkStart {
  user_code: string;
  verification_url: string;
  expires_in: number;
  interval: number;
  pending: string;
}

export const scrobbleApi = {
  status: () => api.get<ScrobbleStatus>('/users/me/scrobble'),
  /** Link or update the ListenBrainz token. An empty token unlinks. */
  setListenBrainz: (token: string, enabled: boolean) =>
    api.put<void>('/users/me/scrobble/listenbrainz', { token, enabled }),
  startLastFM: () => api.post<LastFMLinkStart>('/users/me/scrobble/lastfm/link', {}),
  completeLastFM: (pending: string) =>
    api.post<ScrobbleLinkResult>('/users/me/scrobble/lastfm/link/complete', { pending }),
  unlinkLastFM: () => api.delete('/users/me/scrobble/lastfm'),
  startTrakt: () => api.post<TraktLinkStart>('/users/me/scrobble/trakt/link', {}),
  completeTrakt: (pending: string) =>
    api.post<ScrobbleLinkResult>('/users/me/scrobble/trakt/link/complete', { pending }),
  unlinkTrakt: () => api.delete('/users/me/scrobble/trakt')
};

export interface UserLibraryAccess {
  library_id: string;
  name: string;
  type: string;
  enabled: boolean;
}

export interface UserPreferences {
  preferred_audio_lang: string | null;
  preferred_subtitle_lang: string | null;
  max_content_rating: string | null;
  // When true, auto-select only forced subtitles in the preferred language
  // (show foreign-dialogue captions, not full subtitles). Returned by the
  // server's preferences response.
  forced_subtitles_only?: boolean;
  // Optional on the wire: server returns it always, but the
  // setPreferences PUT body can omit it (server treats absent as
  // "leave unchanged" via SQL COALESCE).
  episode_use_show_poster?: boolean;
  // Whether the caller's own account has a PIN set. Server returns it on GET;
  // the Settings page reads its own PIN status here (the switchable-profiles
  // list is scoped to managed children and never contains the caller).
  has_pin?: boolean;
  // Hub row customization — absent until the user customizes.
  hub_layout?: HubRowPref[];
}

// One entry of the per-user hub layout. key: "continue_tv",
// "continue_movies", "continue_other", "next_up", "plan_to_watch",
// "trending", "libraries", "library:<uuid>" or "collection:<uuid>". Hub rows not present in the
// saved layout render enabled, after the configured rows — so new libraries
// appear without re-saving. Exception: a saved layout that predates
// next_up / plan_to_watch comes back from GET /users/me/preferences with
// those two inserted (enabled) right after the continue_* rows.
export interface HubRowPref {
  key: string;
  enabled: boolean;
}

// ── Libraries ─────────────────────────────────────────────────────────────────

export interface Library {
  id: string;
  name: string;
  type: 'movie' | 'show' | 'music' | 'photo' | 'dvr' | 'audiobook' | 'podcast' | 'home_video' | 'book' | 'anime' | 'cartoons';
  scan_paths: string[];
  agent: string;
  language: string;
  scan_interval_minutes?: number;
  // Visibility flag (v2.1+). false = public (every authenticated user
  // can see it); true = private (requires an explicit grant in the
  // library_access table). Admins bypass this check entirely.
  is_private: boolean;
  // When true, every newly-created user (invite, OIDC/SAML/LDAP JIT
  // auto-create) is automatically granted access. Only meaningful on
  // private libraries — the settings UI hides the toggle for public
  // libraries since the grant is a no-op.
  auto_grant_new_users: boolean;
  // Automatic seek-bar thumbnail (trickplay) generation after scans and in
  // the nightly backfill. Optional: servers before v2.5 don't send it.
  trickplay_enabled?: boolean;
  // Import movie.nfo <set> (and <tag>) groupings as manual collections.
  // Optional: servers before v2.6 don't send it.
  nfo_collections?: NFOCollectionsMode;
  created_at: string;
  updated_at: string;
}

/** What a library imports from movie.nfo files as collections. */
export type NFOCollectionsMode = 'off' | 'sets' | 'sets_and_tags';

// Per-library seek-bar thumbnail progress (admin). pending = not generated
// yet, including the item generating right now; failed includes items with
// no usable file.
export interface LibraryTrickplayStatus {
  enabled: boolean;
  total: number;
  done: number;
  pending: number;
  failed: number;
}

export const libraryApi = {
  trickplayStatus: (id: string) =>
    api.get<LibraryTrickplayStatus>(`/libraries/${id}/trickplay/status`),
  // Queues every item still missing thumbnails; resolves with how many were
  // newly queued (0 when everything is already done or in the queue).
  generateTrickplay: (id: string, includeFailed = false) =>
    api.post<{ status: 'queued'; queued: number }>(
      `/libraries/${id}/trickplay/generate${includeFailed ? '?include_failed=true' : ''}`
    ),
  list: () => api.get<Library[]>('/libraries'),
  get: (id: string) => api.get<Library>(`/libraries/${id}`),
  create: (body: Partial<Library>) => api.post<Library>('/libraries', body),
  update: (id: string, body: Partial<Library>) => api.patch<Library>(`/libraries/${id}`, body),
  del: (id: string) => api.del(`/libraries/${id}`),
  scan: (id: string) => api.post(`/libraries/${id}/scan`),
  detectIntros: (id: string) => api.post(`/libraries/${id}/detect-intros`)
};

// ── Media Items ───────────────────────────────────────────────────────────────

// MediaItem is the lightweight representation returned by the library
// items list endpoint. The watch page uses ItemDetail (with files +
// streams) instead of this shape.
export interface MediaItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  summary?: string;
  rating?: number;
  duration_ms?: number;
  genres?: string[];
  poster_path?: string;
  // Foreign-language title for movies; author for audiobooks (the
  // scanner stashes the parsed audiobook author here in v2.0 to avoid
  // a migration just for one column). Surfaced in the library grid
  // when the item is an audiobook.
  original_title?: string;
  // Date taken / released. EXIF DateTimeOriginal for photos, file
  // mtime for home videos, TMDB release date for movies/episodes.
  // Drives the date-grouped grid on home video and photo libraries.
  taken_at?: string;
  created_at: string;
  updated_at: string;
  // Child rows listed via `type` (an album on the albums index) carry their
  // parent — the album's artist. Absent on top-level rows.
  parent_id?: string;
  parent_title?: string;
  // The caller's watch state (movies, episodes, videos). Absent = unwatched.
  watch_state?: 'watched' | 'in_progress' | 'unwatched';
  view_offset_ms?: number;
  // Shows only: episodes in total and episodes the caller hasn't watched.
  leaf_count?: number;
  unwatched_count?: number;
}

// 'artist' orders by the parent's title — the artist, for albums.
export type SortField = 'title' | 'year' | 'rating' | 'created_at' | 'taken_at' | 'artist';

export interface PhotoEXIF {
  taken_at?: string;
  camera_make?: string;
  camera_model?: string;
  lens_model?: string;
  focal_length_mm?: number;
  aperture?: number;
  shutter_speed?: string;
  iso?: number;
  flash?: boolean;
  orientation?: number;
  width?: number;
  height?: number;
  gps_lat?: number;
  gps_lon?: number;
  gps_alt?: number;
}

export interface ListItemsParams {
  sort?: SortField;
  sort_dir?: 'asc' | 'desc';
  genre?: string;
  year_min?: number;
  year_max?: number;
  rating_min?: number;
  // Override the library's default root item type. Lets a music library
  // page list music_video items alongside its artists, a podcast library
  // list episodes, etc. Validated server-side against an allow-list per
  // library type.
  type?: string;
  // Narrow by the caller's watch state (server-side; total stays exact).
  // Videos: watched = finished or marked played; in_progress = started,
  // not finished; unwatched = never started (or marked unplayed since).
  // Shows/seasons: watched = every episode watched; in_progress = some
  // episode watched or started, not all; unwatched = nothing started.
  watch?: WatchFilter;
}

export type WatchFilter = 'unwatched' | 'in_progress' | 'watched';

// ── Settings ──────────────────────────────────────────────────────────────────

export interface OpenSubtitlesSettings {
  api_key: string;
  username: string;
  password: string; // "****" if set, "" if empty
  languages: string;
  enabled: boolean;
}

export interface OIDCSettings {
  enabled: boolean;
  display_name: string;
  issuer_url: string;
  client_id: string;
  client_secret: string; // "****" if set, "" if empty
  scopes: string;
  username_claim: string;
  groups_claim: string;
  admin_group: string;
}

export interface LDAPSettings {
  enabled: boolean;
  display_name: string;
  host: string;
  start_tls: boolean;
  use_ldaps: boolean;
  skip_tls_verify: boolean;
  bind_dn: string;
  bind_password: string; // "****" if set, "" if empty
  user_search_base: string;
  user_filter: string;
  username_attr: string;
  email_attr: string;
  admin_group_dn: string;
}

export interface SAMLSettings {
  enabled: boolean;
  display_name: string;
  idp_metadata_url: string;
  entity_id: string;
  sp_certificate_pem: string;
  sp_private_key_pem: string; // "****" if set, "" if empty
  email_attribute: string;
  username_attribute: string;
  groups_attribute: string;
  admin_group: string;
}

export interface SMTPSettings {
  enabled: boolean;
  host: string;
  port: number;
  username: string;
  password: string; // "****" if set, "" if empty
  from: string;
}

export interface OTelSettings {
  enabled: boolean;
  endpoint: string;          // OTLP/gRPC URL, e.g. http://localhost:4317
  sample_ratio: number;      // 0.0–1.0
  deployment_env: string;    // tagged on every span; e.g. "production"
}

export interface StorageSettings {
  enabled: boolean;
  backend: string;           // "local" | "s3"
  endpoint: string;          // S3 host, no scheme (e.g. s3.us-west-2.amazonaws.com)
  region: string;
  bucket: string;
  access_key: string;
  secret_key: string;        // "****" if set, "" if empty
  use_ssl: boolean;
  media_root: string;        // local path prefix stripped to form the object key
  path_prefix: string;       // prefix inside the bucket
  cdn_base_url: string;      // optional CDN origin for signed URLs
  path_mappings: Record<string, string>; // local backend: from→to prefix remap (multi-site DR)
}

export interface StorageTestResult {
  ok: boolean;
  error?: string;
}

// Cluster-wide server toggles that used to be env-only. Restart-required.
// ABR fields live in TranscodeConfig (Settings ▸ Transcode), not here.
export interface SystemSettings {
  server_name: string;
  retain_months: number;
  tmdb_rate_limit: number;
  public_asset_cache: boolean;
  static_abr_enabled: boolean;
  missing_file_grace_minutes: number;
  scan_file_concurrency: number;
  scan_library_concurrency: number;
  discovery_enabled: boolean;
  discovery_port: number;
}

export interface TLSStatus {
  configured: boolean;
  source: 'env-file' | 'uploaded' | 'none';
  subject?: string;
  not_after?: string;
}

export interface NodeSettings {
  node_id: string;
  is_current: boolean;
  listen_addr: string;
  metrics_addr: string;
  worker_health_addr: string;
  cache_path: string;
  static_abr_root: string;
  site_id: string;
  transcode_qsv_decode: boolean;
  disable_embedded_worker: boolean;
}

export interface NodeSummary {
  node_id: string;
  updated_at: string;
}

export interface NodeList {
  current_node_id: string;
  nodes: NodeSummary[];
}

export interface GeneralSettings {
  base_url: string;            // public URL — used for OAuth redirects, LAN discovery
  log_level: string;           // debug | info | warn | error
  cors_allowed_origins: string[];
}

/** Auto-approve defaults stamped onto new full accounts (register, invite,
 *  SSO/LDAP provisioning). Changing them never touches existing users, and
 *  managed household profiles always start with both off. */
export interface RequestSettings {
  default_auto_approve_movies: boolean;
  default_auto_approve_tv: boolean;
  // Live server-wide request quotas for users without their own: requests
  // per quota_window_days-day window, 0 = unlimited. Past the limit a request
  // still goes through but waits for an admin. Admins are exempt.
  quota_movies?: number;
  quota_tv?: number;
  quota_window_days?: number;
}

export interface ServerSettings {
  tmdb_api_key: string;
  tvdb_api_key: string;
  arr_api_key: string;
  arr_webhook_url: string;
  arr_path_mappings?: Record<string, string>;
  transcode_encoders: string;
  web_downloads_enabled: boolean;
  opensubtitles: OpenSubtitlesSettings;
  lastfm: LastFMAppSettings;
  trakt: TraktAppSettings;
  oidc: OIDCSettings;
  ldap: LDAPSettings;
  saml: SAMLSettings;
  smtp: SMTPSettings;
  otel: OTelSettings;
  general: GeneralSettings;
  requests: RequestSettings;
}

/** The Last.fm API account users' Last.fm scrobbling runs under. The key is
 *  shown; the secret comes back as "****" when set, and sending "****" back
 *  keeps it. */
export interface LastFMAppSettings {
  api_key: string;
  shared_secret: string;
}

/** The Trakt API application users' Trakt scrobbling runs under. Same
 *  masking as LastFMAppSettings. */
export interface TraktAppSettings {
  client_id: string;
  client_secret: string;
}

export interface OpenSubtitlesUpdate {
  api_key?: string;
  username?: string;
  password?: string;
  languages?: string;
  enabled?: boolean;
}

export interface EncoderEntry {
  encoder: string;
  label: string;
}

export interface EncoderInfo {
  detected: EncoderEntry[];
  current: string;
}

export interface WorkerInfo {
  id: string;
  addr: string;
  capabilities: string[];
  max_sessions: number;
  active_sessions: number;
  registered_at: string;
}

export interface WorkerSlotConfig {
  addr: string;
  name: string;
  encoder: string;
  max_sessions?: number;
}

export interface FleetConfig {
  embedded_enabled: boolean;
  embedded_encoder: string;
  /** Cap on embedded concurrent sessions; omit/0 = TRANSCODE_MAX_SESSIONS default. */
  embedded_max_sessions?: number;
  workers: WorkerSlotConfig[];
}

export interface FleetWorkerStatus {
  id: string;
  addr: string;
  name: string;
  encoder: string;
  online: boolean;
  active_sessions: number;
  max_sessions: number;
  /** Cost-weighted utilization 0–100 (a 4K stream reads far higher than a 480p one). */
  load_percent: number;
  /** Node has a GPU HDR→SDR tonemap path; the dispatcher routes HDR jobs here first. */
  gpu_tonemap: boolean;
  capabilities: string[];
  /** Per-worker encoder→label map (e.g. "h264_nvenc"→"NVIDIA GeForce RTX 4080 Laptop GPU").
   *  The Device dropdown for each worker row reads this rather than the host's merged
   *  encoder list so options match what THIS worker actually has. */
  encoder_labels?: Record<string, string>;
}

export interface FleetStatus {
  embedded_enabled: boolean;
  embedded_disabled_by_env: boolean;
  embedded_encoder: string;
  embedded_online: boolean;
  embedded_active_sessions: number;
  embedded_max_sessions: number;
  /** Cost-weighted utilization 0–100 of the embedded worker. */
  embedded_load_percent: number;
  /** Embedded worker has a GPU HDR→SDR tonemap path. */
  embedded_gpu_tonemap: boolean;
  embedded_capabilities: string[];
  workers: FleetWorkerStatus[];
  /** This server's LAN IPv4, for showing worker-reachable connection URLs. "" if undetected. */
  server_lan_ip: string;
}

export interface TranscodeConfig {
  nvenc_preset: string;
  nvenc_tune: string;
  nvenc_rc: string;
  maxrate_ratio: number;
  max_bitrate_kbps: number;
  max_width: number;
  max_height: number;
  // Adaptive-bitrate HLS ladder. Server backfills nil ptr fields with the
  // env-effective default before responding, so these are always present on GET.
  abr: boolean;
  abr_max_height: number;        // 0 = no hard ceiling
  abr_auto_max_height: number;   // soft ceiling for AUTO playback
}

export interface WorkerCredentials {
  database_url: string;
  valkey_url: string;
  secret_key: string;
}

export const settingsApi = {
  get: () => api.get<ServerSettings>('/settings'),
  update: (body: Partial<ServerSettings>) => api.patch<void>('/settings', body),
  getEncoders: () => api.get<EncoderInfo>('/settings/encoders'),
  getWorkers: () => api.get<WorkerInfo[]>('/settings/workers'),
  getFleet: () => api.get<FleetStatus>('/settings/fleet'),
  updateFleet: (body: FleetConfig) => api.put<void>('/settings/fleet', body),
  revealWorkerCredentials: (password: string, totpCode: string) =>
    api.post<WorkerCredentials>('/settings/worker-credentials', { password, totp_code: totpCode }),
  getTranscodeConfig: () => api.get<TranscodeConfig>('/settings/transcode-config'),
  updateTranscodeConfig: (body: TranscodeConfig) => api.put<void>('/settings/transcode-config', body),
  testEmail: (to: string) => api.post<{ message: string }>('/email/test', { to }),
  getStorage: () => api.get<StorageSettings>('/settings/storage'),
  updateStorage: (body: StorageSettings) => api.put<void>('/settings/storage', body),
  testStorage: (body: StorageSettings) => api.post<StorageTestResult>('/settings/storage/test', body),
  getSystem: () => api.get<SystemSettings>('/settings/system'),
  updateSystem: (body: SystemSettings) => api.put<void>('/settings/system', body),

  getTLS: () => api.get<TLSStatus>('/settings/tls'),
  updateTLS: (body: { cert_pem: string; key_pem: string }) => api.put<void>('/settings/tls', body),

  getNodes: () => api.get<NodeList>('/settings/nodes'),
  getNode: (nodeID: string) => api.get<NodeSettings>(`/settings/node/${encodeURIComponent(nodeID)}`),
  updateNode: (nodeID: string, body: NodeSettings) => api.put<void>(`/settings/node/${encodeURIComponent(nodeID)}`, body),
  deleteNode: (nodeID: string) => api.delete(`/settings/node/${encodeURIComponent(nodeID)}`),
};

// ── Filesystem browser ────────────────────────────────────────────────────────

export interface BrowseResult {
  path: string;
  parent: string;
  dirs: string[];
}

export const fsApi = {
  browse: (path = '/') =>
    api.get<BrowseResult>(`/fs/browse?path=${encodeURIComponent(path)}`)
};

// ── Media Items ───────────────────────────────────────────────────────────────

export const mediaApi = {
  listItems: (libraryId: string, limit = 50, offset = 0, params?: ListItemsParams) => {
    const qs = new URLSearchParams();
    qs.set('limit', String(limit));
    qs.set('offset', String(offset));
    if (params?.sort) qs.set('sort', params.sort);
    if (params?.sort_dir) qs.set('sort_dir', params.sort_dir);
    if (params?.genre) qs.set('genre', params.genre);
    if (params?.year_min != null) qs.set('year_min', String(params.year_min));
    if (params?.year_max != null) qs.set('year_max', String(params.year_max));
    if (params?.rating_min != null) qs.set('rating_min', String(params.rating_min));
    if (params?.type) qs.set('type', params.type);
    if (params?.watch) qs.set('watch', params.watch);
    return api.requestList<MediaItem>(`/libraries/${libraryId}/items?${qs.toString()}`);
  },
  // One random item matching the same filters as listItems ("Surprise me").
  // 404 when nothing matches.
  randomItem: (libraryId: string, params?: Omit<ListItemsParams, 'sort' | 'sort_dir'>) => {
    const qs = new URLSearchParams();
    if (params?.genre) qs.set('genre', params.genre);
    if (params?.year_min != null) qs.set('year_min', String(params.year_min));
    if (params?.year_max != null) qs.set('year_max', String(params.year_max));
    if (params?.rating_min != null) qs.set('rating_min', String(params.rating_min));
    if (params?.type) qs.set('type', params.type);
    if (params?.watch) qs.set('watch', params.watch);
    return api.get<{ id: string; type: string }>(`/libraries/${libraryId}/random?${qs.toString()}`);
  },
  // Genre / year facets count the library's top-level type unless `type`
  // names another level — a music library's genres and years are on its
  // albums ('album'), not its artists.
  genres: (libraryId: string, type?: string) =>
    api.get<GenreCount[]>(`/libraries/${libraryId}/genres${type ? `?type=${encodeURIComponent(type)}` : ''}`),
  years: (libraryId: string, type?: string) =>
    api.get<YearCount[]>(`/libraries/${libraryId}/years${type ? `?type=${encodeURIComponent(type)}` : ''}`),
  // eventCollections returns the auto-created event_folder collections
  // for a home_video library — one per non-root subfolder under the
  // library's scan paths. Empty for any other library type.
  eventCollections: (libraryId: string) =>
    api.get<EventCollection[]>(`/libraries/${libraryId}/event-collections`),
  enrichItem: (id: string) =>
    api.post<void>(`/items/${id}/enrich`),
};

// WatchStatus mirrors WatchStatusResponse on the server. Status is
// one of the five anime-tracker classifications shipped as a generic
// per-(user, item) feature.
export type WatchStatusValue =
  | 'plan_to_watch'
  | 'watching'
  | 'on_hold'
  | 'completed'
  | 'dropped';

export interface WatchStatus {
  status: WatchStatusValue;
  created_at: string;
  updated_at: string;
}

// Per-user audiobook listening speed. source says where it came from:
// 'book' (set on this book), 'recent' (the user's latest speed on another
// book) or 'default' (1.0, nothing set yet).
export interface PlaybackRate {
  rate: number;
  source: 'book' | 'recent' | 'default';
}

// A bookmark in an audiobook. item_id is the playable item the position is
// in: the book itself for a single-file book, else a chapter, which
// item_title / item_index name (item_index is omitted for a single-file
// book's own bookmarks).
export interface Bookmark {
  id: string;
  item_id: string;
  item_title: string;
  item_index?: number;
  position_ms: number;
  note: string;
  created_at: string;
}

export interface EventCollection {
  id: string;
  name: string;
  poster_path?: string;
}

export interface LyricsResponse {
  plain: string;
  synced: string;
}

export const lyricsApi = {
  get: (itemId: string) => api.get<LyricsResponse>(`/items/${itemId}/lyrics`),
};

export interface GenreCount {
  name: string;
  count: number;
}

export interface YearCount {
  year: number;
  count: number;
}

// ── People (cast/crew) ────────────────────────────────────────────────────────

export interface PersonSummary {
  id: string;
  tmdb_id?: number;
  name: string;
  profile_path?: string;
}

export interface Credit {
  person: PersonSummary;
  role: string; // 'cast' | 'director' | 'writer' | 'producer' | 'creator'
  character?: string;
  job?: string;
  order: number;
}

export interface Person {
  id: string;
  tmdb_id?: number;
  name: string;
  profile_path?: string;
  bio?: string;
  birthday?: string;
  deathday?: string;
  place_of_birth?: string;
}

export interface FilmographyEntry {
  item_id: string;
  library_id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  rating?: number;
  role: string;
  character?: string;
  job?: string;
}

export const peopleApi = {
  credits: (itemId: string) => api.get<Credit[]>(`/items/${itemId}/credits`),
  get: (id: string) => api.get<Person>(`/people/${id}`),
  filmography: (id: string) => api.get<FilmographyEntry[]>(`/people/${id}/filmography`),
  search: (q: string) => api.get<PersonSummary[]>(`/people?q=${encodeURIComponent(q)}`)
};

// ── Item detail (player) ──────────────────────────────────────────────────────

export interface AudioStream {
  index: number;
  codec: string;
  channels: number;
  language: string;
  title: string;
}

export interface SubtitleStream {
  index: number;
  codec: string;
  language: string;
  title: string;
  forced: boolean;
  sdh?: boolean;
  /** The file marks this track default (absent from older servers). */
  default?: boolean;
}

export interface ExternalSubtitle {
  id: string;
  file_id: string;
  language: string;
  title?: string;
  forced: boolean;
  sdh: boolean;
  source: string;
  source_id?: string;
  url: string;
}

export interface SubtitleSearchResult {
  provider_file_id: number;
  file_name: string;
  language: string;
  release: string;
  hearing_impaired: boolean;
  hd: boolean;
  from_trusted: boolean;
  rating: number;
  download_count: number;
  uploader_name: string;
}

export interface Chapter {
  title: string;
  start_ms: number;
  end_ms: number;
}

export interface ItemFile {
  id: string;
  stream_url: string;
  container?: string;
  video_codec?: string;
  audio_codec?: string;
  resolution_w?: number;
  resolution_h?: number;
  bitrate?: number;
  hdr_type?: string;
  duration_ms?: number;
  faststart: boolean;
  // Video bit depth (8/10/12) of the primary video stream, from pix_fmt.
  // Distinct from bit_depth (audio). Undefined until the file is (re)probed.
  video_bit_depth?: number;
  // Deep-decode integrity probe verdict: 'unchecked' | 'ok' | 'damaged'.
  // Damaged files get a "file is damaged" badge + playback pre-empt.
  integrity_status?: string;
  // Failure summary behind a damaged verdict. Server sends it to admins only.
  integrity_detail?: string;
  // Audio quality fields (music libraries — undefined for video).
  bit_depth?: number;
  sample_rate?: number;
  channel_layout?: string;
  lossless?: boolean;
  replaygain_track_gain?: number;
  replaygain_track_peak?: number;
  replaygain_album_gain?: number;
  replaygain_album_peak?: number;
  audio_streams: AudioStream[];
  subtitle_streams: SubtitleStream[];
  external_subtitles?: ExternalSubtitle[];
  chapters: Chapter[];
  // Per-file 24h PASETO bound to the requesting user. Used in the
  // `?token=` query of the download URL on clients that can't carry
  // cookies (mobile browser save-as, Android Download Manager). Omitted
  // by the server when the stream-token maker isn't wired.
  stream_token?: string;
}

export interface Marker {
  kind: 'intro' | 'credits';
  start_ms: number;
  end_ms: number;
  source: 'auto' | 'manual' | 'chapter';
}

export interface ItemDetail {
  id: string;
  library_id: string;
  title: string;
  type: string;
  // Foreign-language title for movies, author name for audiobooks
  // (re-purposed by the scanner). Used as a byline fallback when the
  // parent author lookup fails.
  original_title?: string;
  year?: number;
  summary?: string;
  rating?: number; // external critic score (provider)
  audience_rating?: number; // external audience score (provider)
  user_rating?: number; // the signed-in user's own 0-10 rating
  community_rating?: number; // mean of all OnScreen users' ratings
  rating_count?: number; // number of OnScreen user ratings behind community_rating
  duration_ms?: number;
  poster_path?: string;
  fanart_path?: string;
  content_rating?: string;
  genres: string[];
  parent_id?: string;
  grandparent_id?: string;
  index?: number;
  view_offset_ms: number;
  // Playable videos only: the caller's watch state (manual marks included).
  watch_state?: 'watched' | 'in_progress' | 'unwatched';
  updated_at: number;
  is_favorite: boolean;
  // ISO timestamp of media_items.originally_available_at — populated
  // for movies/episodes (TMDB release date), photos (EXIF taken),
  // home videos (file mtime). The metadata editor pre-fills its date
  // field from this when present.
  taken_at?: string;
  files: ItemFile[];
  markers?: Marker[];
  tmdb_id?: number;
  tvdb_id?: number;
  // Anime-native external IDs. Populated when an anime library
  // matched the show / movie via the AniList agent. Surfaced on
  // the detail page as deep-link buttons (anilist.co, MAL).
  anilist_id?: number;
  mal_id?: number;
  // Anime episode subtype (`ova`, `ona`, `special`, `movie`).
  // Absent / `episode` = ordinary episode.
  kind?: string;
  // Manga / book reader page-flip direction. `ltr` (Western), `rtl`
  // (manga), `ttb` (webtoon / manhwa / manhua vertical strip).
  // Absent → reader picks library-type default at open time.
  reading_direction?: 'ltr' | 'rtl' | 'ttb';
  // Music-specific fields (undefined for non-music items).
  musicbrainz_id?: string;
  musicbrainz_release_id?: string;
  musicbrainz_release_group_id?: string;
  musicbrainz_artist_id?: string;
  musicbrainz_album_artist_id?: string;
  disc_total?: number;
  track_total?: number;
  original_year?: number;
  compilation?: boolean;
  release_type?: string;
  // Movies only: the TMDB franchise collection the movie belongs to
  // (the "Part of the <Name>" shelf). Absent when none.
  collection?: ItemCollectionRef;
  // Movies and shows: the manual (admin-curated) collections the item is in.
  collections?: ItemManualCollectionRef[];
}

export interface FavoriteItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  duration_ms?: number;
  favorited_at: string;
}

export interface ChildItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  summary?: string;
  rating?: number;
  duration_ms?: number;
  poster_path?: string;
  thumb_path?: string;
  index?: number;
  // Tracks: disc within the album (index is the track on that disc).
  // Absent = no disc tag, i.e. disc 1. Children arrive in (disc, track) order.
  disc_number?: number;
  // Episode subtype within an anime show (`ova`, `ona`, `special`,
  // `movie`). Absent or `episode` = ordinary episode. Surfaced as a
  // badge on the episode list.
  kind?: string;
  // Per-user playback state populated by the server's children
  // endpoint. Used by show / season detail pages to render the
  // watched indicator + in-progress bar on each episode row.
  view_offset_ms?: number;
  watched?: boolean;
  created_at: string;
  updated_at: number;
}

export interface MatchCandidate {
  tmdb_id: number;
  title: string;
  year?: number;
  summary?: string;
  poster_url?: string;
  rating?: number;
}

export interface PosterCandidate {
  url: string;
  width: number;
  height: number;
  language?: string | null;
  vote: number;
}

// ── Collections & Playlists ──────────────────────────────────────────────────

export interface Collection {
  id: string;
  name: string;
  description?: string;
  type: 'auto_genre' | 'playlist' | 'franchise' | 'event_folder' | 'photo_album' | 'smart_playlist' | 'manual';
  genre?: string;
  poster_path?: string;
  created_at: string;
  // Manual collections only (type 'manual', admin-curated, shared):
  // how the items are listed, the member whose poster is the cover (absent =
  // the first member's), and how many members the caller can see.
  // poster_path carries the cover.
  item_order?: CollectionItemOrder;
  poster_item_id?: string;
  item_count?: number;
  // Manual collections, v2.6: on the home screen (an admin promoted it); a
  // smart collection's rules (its members follow them; hand edits are
  // refused); set when an admin uploaded a cover (collectionPosterUrl, it
  // wins over poster_path); 'nfo' when imported from movie.nfo files.
  promoted?: boolean;
  rules?: CollectionRules;
  poster_version?: number;
  source?: string;
  // Franchise collections only (type 'franchise'): the TMDB collection id
  // and art. `parts` is set on GET /collections/{id} — every film of the
  // franchise the caller may see, owned (item_id) or not (item_id null).
  tmdb_collection_id?: number;
  poster_url?: string;
  backdrop_url?: string;
  parts?: FranchisePart[];
}

/** One film of a franchise collection. item_id is set when it's in the
 *  server and visible to the caller; otherwise it's a missing part (only
 *  listed for profiles without a rating ceiling) with the caller's own
 *  open request status, if any. */
export interface FranchisePart {
  tmdb_id: number;
  title: string;
  year?: number;
  release_date?: string;
  poster_url?: string;
  overview?: string;
  item_id: string | null;
  // Every copy of this film the caller can see (e.g. a 4K and a 1080p
  // library), lowest id first; item_id is the first of these.
  item_ids?: string[];
  request_status: 'pending' | 'approved' | 'downloading' | null;
}

/** How a manual collection lists its members: the admin's own order, oldest
 *  first, or by title. */
export type CollectionItemOrder = 'custom' | 'release' | 'title';

/** A smart collection's rules: movies and/or shows, optionally one genre, a
 *  year range, a minimum rating and some libraries; limit caps the members
 *  (default 200, at most 500). */
export interface CollectionRules {
  types: ('movie' | 'show')[];
  genres?: string[];
  year_min?: number;
  year_max?: number;
  rating_min?: number;
  library_ids?: string[];
  limit?: number;
}

/** The URL of a manual collection's uploaded cover, or null without one. */
export function collectionPosterUrl(c: Pick<Collection, 'id' | 'poster_version'>): string | null {
  if (c.poster_version == null) return null;
  return assetUrl(`/api/v1/collections/${c.id}/poster?v=${c.poster_version}`);
}

/** A franchise or manual collection with visible items in one library
 *  (GET /libraries/{id}/collections — the library Collections tab). */
export interface LibraryCollection {
  id: string;
  name: string;
  type: 'franchise' | 'manual';
  tmdb_collection_id?: number;
  poster_url?: string;
  poster_path?: string;
  item_count: number;
  /** A manual collection's uploaded cover (collectionPosterUrl). */
  poster_version?: number;
}

/** The franchise a movie belongs to, on the movie's detail response. */
export interface ItemCollectionRef {
  id: string;
  name: string;
  part_count: number;
  owned_count: number;
}

/** A manual collection a movie or show belongs to, on its detail response. */
export interface ItemManualCollectionRef {
  id: string;
  name: string;
}

export interface CollectionItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  rating?: number;
  poster_path?: string;
  duration_ms?: number;
  position?: number;
}

export const collectionApi = {
  /** The caller's collections plus server-owned ones. TMDB franchise
   *  collections are opt-in on the server (?include=franchise) so native
   *  home screens that render every collection as a row aren't flooded;
   *  pass includeFranchise where the UI has a place for them. */
  list: (opts: { includeFranchise?: boolean } = {}) =>
    api.get<Collection[]>(opts.includeFranchise ? '/collections?include=franchise' : '/collections'),
  get: (id: string) => api.get<Collection>(`/collections/${id}`),
  create: (name: string, description?: string) =>
    api.post<Collection>('/collections', { name, description }),
  /** A shared, admin-curated collection (admins only). */
  createManual: (name: string, description?: string, itemOrder: CollectionItemOrder = 'custom',
                 extra: { rules?: CollectionRules; promoted?: boolean } = {}) =>
    api.post<Collection>('/collections', { name, description, type: 'manual', item_order: itemOrder, ...extra }),
  /** Fields left out keep their value. */
  update: (id: string, name: string, description?: string) =>
    api.patch<Collection>(`/collections/${id}`, { name, description }),
  /** A manual collection's settings; poster_item_id '' goes back to the
   *  first member's poster. Fields left out keep their value. */
  updateSettings: (id: string, settings: {
    name?: string; description?: string; item_order?: CollectionItemOrder; poster_item_id?: string;
    /** On the home screen for everyone who can see it. */
    promoted?: boolean;
    /** Rules make it smart; null makes it hand-picked again (members stay). */
    rules?: CollectionRules | null;
  }) =>
    api.patch<Collection>(`/collections/${id}`, settings),
  /** Uploads a cover (a JPEG at most 1 MB — see fitCoverImage). */
  uploadPoster: (id: string, image: Blob) =>
    api.postBlob<Collection>(`/collections/${id}/poster`, image),
  /** Back to the chosen (or first) member's poster. */
  deletePoster: (id: string) => api.delete(`/collections/${id}/poster`),
  /** Adds several movies / shows to a manual collection at once. */
  addItems: (collectionId: string, mediaItemIds: string[]) =>
    api.post<void>(`/collections/${collectionId}/items`, { media_item_ids: mediaItemIds }),
  /** A manual collection's custom order: every member, in order. */
  reorder: (collectionId: string, itemIds: string[]) =>
    api.put<void>(`/collections/${collectionId}/items/order`, { item_ids: itemIds }),
  delete: (id: string) => api.delete(`/collections/${id}`),
  items: (id: string, limit = 50, offset = 0) =>
    api.requestList<CollectionItem>(`/collections/${id}/items?limit=${limit}&offset=${offset}`),
  addItem: (collectionId: string, mediaItemId: string) =>
    api.post<void>(`/collections/${collectionId}/items`, { media_item_id: mediaItemId }),
  removeItem: (collectionId: string, itemId: string) =>
    api.delete(`/collections/${collectionId}/items/${itemId}`),
  /** Franchise collections with visible items in a library. */
  forLibrary: (libraryId: string) =>
    api.get<LibraryCollection[]>(`/libraries/${libraryId}/collections`),
};

export interface Playlist {
  id: string;
  name: string;
  description?: string;
  // 'playlist' (static, items in collection_items) or 'smart_playlist'
  // (rules-evaluated, items resolved at query time). The frontend
  // branches on this to surface a "Smart" badge and gate the manual
  // add/remove buttons.
  type: 'playlist' | 'smart_playlist';
  rules?: SmartPlaylistRules;
  created_at: string;
  updated_at: string;
}

export interface SmartPlaylistRules {
  types?: string[];
  genres?: string[];
  year_min?: number;
  year_max?: number;
  rating_min?: number;
  limit?: number;
}

export interface PlaylistItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  rating?: number;
  poster_path?: string;
  duration_ms?: number;
  position: number;
}

export const playlistApi = {
  list: () => api.get<Playlist[]>('/playlists'),
  create: (name: string, description?: string, rules?: SmartPlaylistRules) =>
    api.post<Playlist>('/playlists', rules ? { name, description, rules } : { name, description }),
  update: (id: string, name: string, description?: string) =>
    api.patch<Playlist>(`/playlists/${id}`, { name, description }),
  delete: (id: string) => api.delete(`/playlists/${id}`),
  items: (id: string) =>
    api.requestList<PlaylistItem>(`/playlists/${id}/items`),
  addItem: (playlistId: string, mediaItemId: string) =>
    api.post<void>(`/playlists/${playlistId}/items`, { media_item_id: mediaItemId }),
  removeItem: (playlistId: string, itemId: string) =>
    api.delete(`/playlists/${playlistId}/items/${itemId}`),
  reorder: (playlistId: string, itemIds: string[]) =>
    api.put<void>(`/playlists/${playlistId}/items/order`, { item_ids: itemIds })
};

// ── Photos: map + albums ─────────────────────────────────────────────────────

/** One geotagged photo from GET /photos/map. The server applies the
 *  caller's library access and content-rating ceiling. */
export interface PhotoMapPoint {
  id: string;
  library_id: string;
  title: string;
  poster_path?: string;
  lat: number;
  lon: number;
  taken_at?: string;
  created_at: string;
}

/** Optional bounding box + cap for GET /photos/map. Each edge is
 *  independent; the server filters with a plain BETWEEN, so a box that
 *  crosses the antimeridian must be sent as two queries. */
export interface PhotoMapQuery {
  min_lat?: number;
  max_lat?: number;
  min_lon?: number;
  max_lon?: number;
  limit?: number;
}

/** A photo album (a `photo_album` collection). Albums are owner-only:
 *  the list holds the caller's own albums and nobody else's. item_count
 *  and cover_path come from the list; create / update responses omit them. */
export interface PhotoAlbum {
  id: string;
  name: string;
  description?: string;
  cover_path?: string;
  item_count: number;
  created_at: string;
  updated_at: string;
}

export interface PhotoAlbumItem {
  id: string;
  library_id: string;
  title: string;
  poster_path?: string;
  taken_at?: string;
  camera_make?: string;
  camera_model?: string;
  width?: number;
  height?: number;
  orientation?: number;
  added_at: string;
}

export const photoApi = {
  /** Geotagged photos in a library. `total` is the library-wide count
   *  (ignoring the box), so total > items.length means the answer was
   *  truncated at the limit. */
  map: (libraryId: string, q: PhotoMapQuery = {}) => {
    const qs = new URLSearchParams({ library_id: libraryId });
    for (const k of ['min_lat', 'max_lat', 'min_lon', 'max_lon', 'limit'] as const) {
      const v = q[k];
      if (v != null && Number.isFinite(v)) qs.set(k, String(v));
    }
    return api.requestList<PhotoMapPoint>(`/photos/map?${qs.toString()}`);
  },
};

export const photoAlbumApi = {
  list: () => api.get<PhotoAlbum[]>('/photo-albums'),
  create: (name: string, description?: string) =>
    api.post<PhotoAlbum>('/photo-albums', description ? { name, description } : { name }),
  /** Rename. The server keeps the old name for a blank one. */
  rename: (id: string, name: string) =>
    api.patch<PhotoAlbum>(`/photo-albums/${encodeURIComponent(id)}`, { name }),
  delete: (id: string) => api.delete(`/photo-albums/${encodeURIComponent(id)}`),
  /** Newest-taken first. The server returns up to 5000 per request. */
  items: (id: string) =>
    api.requestList<PhotoAlbumItem>(`/photo-albums/${encodeURIComponent(id)}/items`),
  /** Adding a photo that's already in the album is a no-op. */
  addItem: (albumId: string, mediaItemId: string) =>
    api.post<void>(`/photo-albums/${encodeURIComponent(albumId)}/items`, { media_item_id: mediaItemId }),
  removeItem: (albumId: string, mediaItemId: string) =>
    api.delete(`/photo-albums/${encodeURIComponent(albumId)}/items/${encodeURIComponent(mediaItemId)}`),
};

export interface ManagedProfile {
  id: string;
  username: string;
  avatar_url?: string;
  has_pin: boolean;
  created_at: string;
  max_content_rating?: string | null;
  // When true (default) the profile sees the parent owner's library
  // grants. When false the profile uses its own library_access rows
  // — set explicit grants via userApi.setLibraries(profileId, [...])
  // to narrow what the profile can see.
  inherit_library_access: boolean;
}

export const profileApi = {
  list: () => api.get<ManagedProfile[]>('/profiles'),
  create: (username: string, avatar_url?: string, pin?: string) =>
    api.post<ManagedProfile>('/profiles', { username, avatar_url, pin }),
  update: (id: string, username: string, avatar_url?: string) =>
    api.patch<ManagedProfile>(`/profiles/${id}`, { username, avatar_url }),
  delete: (id: string) => api.delete(`/profiles/${id}`),
  setLibraryInherit: (id: string, inherit: boolean) =>
    api.put<void>(`/profiles/${id}/library-inherit`, { inherit }),
};

export const itemApi = {
  get: (id: string) => api.get<ItemDetail>(`/items/${id}`),
  exif: (id: string) => api.get<PhotoEXIF>(`/items/${id}/exif`),
  children: (id: string) =>
    api.requestList<ChildItem>(`/items/${id}/children`),
  progress: (id: string, viewOffsetMs: number, durationMs: number, state: 'playing' | 'paused' | 'stopped',
             decision?: 'directPlay' | 'directStream' | 'remux' | 'transcode') =>
    api.put<void>(`/items/${id}/progress`, {
      view_offset_ms: viewOffsetMs,
      duration_ms: durationMs,
      state,
      // client_name drives the device picker in the cross-device
      // "Play on…" remote control. Browser builds derive a label
      // from userAgent + a "Web —" prefix so the user can tell the
      // browser apart from native clients in the picker.
      client_name: getClientName(),
      // decision feeds the analytics direct-vs-transcode split.
      ...(decision ? { decision } : {}),
    }),
  searchMatch: (id: string, query: string, year?: number) =>
    api.get<MatchCandidate[]>(`/items/${id}/match/search?query=${encodeURIComponent(query)}${year ? `&year=${year}` : ''}`),
  applyMatch: (id: string, tmdbId: number) =>
    api.post<void>(`/items/${id}/match`, { tmdb_id: tmdbId }),
  listPosters: (id: string, tmdbId: number) =>
    api.get<PosterCandidate[]>(`/items/${id}/posters?tmdb_id=${tmdbId}`),
  applyPoster: (id: string, url: string) =>
    api.post<void>(`/items/${id}/poster`, { url }),
  // updateMetadata edits the title / summary / taken-at on items the
  // metadata agent doesn't enrich (home_video + photo). Each field is
  // "set when present" — pass undefined to leave a field unchanged,
  // empty string to clear (title rejects empty server-side). taken_at
  // accepts YYYY-MM-DD or full RFC3339; the server stores midnight UTC
  // for date-only inputs since the date-grouped grid only keys on
  // (year, month).
  updateMetadata: (id: string, fields: { title?: string; summary?: string; taken_at?: string }) =>
    api.patch<{ id: string; title: string; summary: string | null; originally_available_at: string | null }>(`/items/${id}`, fields),
  remove: (id: string) => api.delete(`/items/${id}`),
  // Cross-device transfer — list distinct clients the user has
  // recently scrobbled from (drives the "Play on…" picker), and
  // broadcast a transfer event the receiving client filters by
  // target_client_name and starts playback on a match.
  listDevices: () =>
    api.get<{ client_name: string; last_seen: string }[]>(`/playback/devices`),
  transfer: (itemId: string, targetClientName: string, positionMs: number) =>
    api.post<void>(`/playback/transfer`, {
      item_id: itemId,
      target_client_name: targetClientName,
      position_ms: positionMs,
    }),
  addFavorite: (id: string) => api.post<void>(`/items/${id}/favorite`, {}),
  // Per-user star rating (0-10). setRating upserts; clearRating un-rates.
  setRating: (id: string, score: number) =>
    api.put<{ score: number }>(`/items/${id}/rating`, { score }),
  clearRating: (id: string) => api.delete(`/items/${id}/rating`),
  removeFavorite: (id: string) => api.delete(`/items/${id}/favorite`),
  listMarkers: (id: string) => api.requestList<Marker>(`/items/${id}/markers`),
  upsertMarker: (id: string, kind: 'intro' | 'credits', startMs: number, endMs: number) =>
    api.put<Marker>(`/items/${id}/markers/${kind}`, { start_ms: startMs, end_ms: endMs }),
  deleteMarker: (id: string, kind: 'intro' | 'credits') =>
    api.delete(`/items/${id}/markers/${kind}`),
  trickplayStatus: (id: string) =>
    api.get<{
      status: 'not_started' | 'pending' | 'done' | 'failed' | 'skipped';
      sprite_count?: number;
      interval_sec?: number;
      thumb_width?: number;
      thumb_height?: number;
      last_error?: string;
    }>(`/items/${id}/trickplay`),

  // Watching-status: per-user Plan to Watch / Watching / etc.
  // Returns 404 when no row exists; UI treats that as "no status".
  getWatchStatus: (id: string) =>
    api.get<WatchStatus>(`/items/${id}/watch-status`),
  setWatchStatus: (id: string, status: WatchStatusValue) =>
    api.put<WatchStatus>(`/items/${id}/watch-status`, { status }),
  clearWatchStatus: (id: string) =>
    api.delete(`/items/${id}/watch-status`),

  // Audiobook listening speed (0.5-3.0) and bookmarks, per user. {id} is
  // the book or one of its chapters (it resolves to the book); a new
  // bookmark goes on the playable item — the single-file book, or the
  // chapter being played. The list covers the whole book, in listening
  // order. Non-audiobooks are 422; a server without these routes is 404.
  getPlaybackRate: (id: string) =>
    api.get<PlaybackRate>(`/items/${id}/playback-rate`),
  setPlaybackRate: (id: string, rate: number) =>
    api.put<void>(`/items/${id}/playback-rate`, { rate }),
  listBookmarks: (id: string) =>
    api.get<Bookmark[]>(`/items/${id}/bookmarks`),
  addBookmark: (id: string, positionMs: number, note = '') =>
    api.post<Bookmark>(`/items/${id}/bookmarks`, { position_ms: positionMs, note }),
  updateBookmark: (bookmarkId: string, note: string) =>
    api.patch<void>(`/bookmarks/${bookmarkId}`, { note }),
  deleteBookmark: (bookmarkId: string) =>
    api.delete(`/bookmarks/${bookmarkId}`),

  // Played / unplayed without playing. On a show or season the server
  // applies it to every episode underneath that the caller can see.
  markWatched: (id: string) => api.post<void>(`/items/${id}/watched`, {}),
  markUnwatched: (id: string) => api.delete(`/items/${id}/watched`),
  // Hide an item from Continue Watching until it's played again.
  dismissContinueWatching: (id: string) =>
    api.post<void>(`/items/${id}/dismiss-continue-watching`, {}),
  // Show or season: which episode Play should start (see UpNext).
  upNext: (id: string) => api.get<UpNext>(`/items/${id}/up-next`),
};

export const subtitleApi = {
  search: (itemId: string, params: { lang?: string; query?: string } = {}) => {
    const qs = new URLSearchParams();
    if (params.lang) qs.set('lang', params.lang);
    if (params.query) qs.set('query', params.query);
    const suffix = qs.toString() ? `?${qs}` : '';
    return api.requestList<SubtitleSearchResult>(`/items/${itemId}/subtitles/search${suffix}`);
  },
  download: (itemId: string, body: {
    file_id: string;
    provider_file_id: number;
    language: string;
    title?: string;
    hearing_impaired?: boolean;
    rating?: number;
    download_count?: number;
  }) => api.post<ExternalSubtitle>(`/items/${itemId}/subtitles/download`, body),
  remove: (itemId: string, subId: string) =>
    api.delete(`/items/${itemId}/subtitles/${subId}`),
  // OCR is job-queued in v2.1+: POST returns 202 with a job descriptor,
  // and clients poll the GET endpoint until status is "completed" or
  // "failed". The synchronous v2.0 behavior 524'd behind reverse proxies
  // with sub-multi-minute response timeouts (Cloudflare Tunnel = 100 s)
  // for feature-length PGS tracks.
  ocr: (itemId: string, body: {
    file_id: string;
    stream_index: number;
    language?: string;
    title?: string;
    forced?: boolean;
    sdh?: boolean;
  }) => api.post<OCRJob>(`/items/${itemId}/subtitles/ocr`, body),
  ocrStatus: (itemId: string, jobId: string) =>
    api.get<OCRJob>(`/items/${itemId}/subtitles/ocr/${jobId}`),
};

export interface OCRJob {
  job_id: string;
  status: 'running' | 'completed' | 'failed';
  file_id: string;
  stream_index: number;
  started_at: string;
  completed_at?: string;
  error?: string;
  subtitle?: ExternalSubtitle;
}

export const favoritesApi = {
  list: (limit = 100, offset = 0) =>
    api.requestList<FavoriteItem>(`/favorites?limit=${limit}&offset=${offset}`)
};

// ── Live TV ───────────────────────────────────────────────────────────────────

export interface LiveTVChannel {
  id: string;
  tuner_id: string;
  tuner_name: string;
  tuner_type: string;
  number: string;
  callsign?: string;
  name: string;
  logo_url?: string;
  enabled: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export interface LiveTVNowNext {
  channel_id: string;
  program_id: string;
  title: string;
  subtitle?: string;
  starts_at: string;
  ends_at: string;
  season_num?: number;
  episode_num?: number;
}

export interface LiveTVTuner {
  id: string;
  type: string;
  name: string;
  config: Record<string, unknown>;
  tune_count: number;
  enabled: boolean;
  last_seen_at?: string;
  created_at: string;
  updated_at: string;
}

export interface LiveTVProgram {
  id: string;
  channel_id: string;
  title: string;
  subtitle?: string;
  description?: string;
  category?: string[];
  rating?: string;
  season_num?: number;
  episode_num?: number;
  original_air_date?: string;
  starts_at: string;
  ends_at: string;
}

export const liveTvApi = {
  channels: () => api.requestList<LiveTVChannel>('/tv/channels'),
  nowNext: () => api.requestList<LiveTVNowNext>('/tv/channels/now-next'),
  // Window is RFC3339 UTC; missing args means "now → now+4h" server-side.
  guide: (from?: string, to?: string) => {
    const qs = new URLSearchParams();
    if (from) qs.set('from', from);
    if (to) qs.set('to', to);
    const path = '/tv/guide' + (qs.toString() ? '?' + qs.toString() : '');
    return api.requestList<LiveTVProgram>(path);
  },
  // Stream URLs are used directly by the player; not fetched as JSON.
  streamUrl: (channelId: string) => `/api/v1/tv/channels/${channelId}/stream.m3u8`,

  // Admin endpoints for the settings UI.
  listTuners: () => api.requestList<LiveTVTuner>('/tv/tuners'),
  createTuner: (body: { type: string; name: string; config: Record<string, unknown>; tune_count?: number }) =>
    api.post<LiveTVTuner>('/tv/tuners', body),
  updateTuner: (id: string, body: { name: string; config: Record<string, unknown>; tune_count?: number; enabled?: boolean }) =>
    api.patch<LiveTVTuner>(`/tv/tuners/${id}`, body),
  deleteTuner: (id: string) => api.delete(`/tv/tuners/${id}`),
  rescanTuner: (id: string) => api.post<{ channel_count: number }>(`/tv/tuners/${id}/rescan`, {}),
  // POST (UDP broadcast has side effects) but returns a list envelope.
  // api.post unwraps .data — and since this handler uses respond.List,
  // .data is the array itself.
  discoverTuners: () =>
    api.post<Array<{ device_id: string; base_url: string; tune_count: number; model?: string }>>(
      '/tv/tuners/discover', {}),

  // EPG sources.
  listEPGSources: () => api.requestList<LiveTVEPGSource>('/tv/epg-sources'),
  createEPGSource: (body: { type: string; name: string; config: Record<string, unknown>; refresh_interval_min?: number }) =>
    api.post<LiveTVEPGSource>('/tv/epg-sources', body),
  deleteEPGSource: (id: string) => api.delete(`/tv/epg-sources/${id}`),
  refreshEPGSource: (id: string) =>
    api.post<{ programs_ingested: number; channels_auto_matched: number; unmapped_channels: number; skipped: number }>(
      `/tv/epg-sources/${id}/refresh`, {}),
  setChannelEPGID: (channelId: string, epgChannelID: string | null) =>
    api.patch<void>(`/tv/channels/${channelId}/epg-id`, { epg_channel_id: epgChannelID }),
  listUnmappedChannels: () =>
    api.requestList<{ id: string; number: string; callsign?: string; name: string; logo_url?: string }>('/tv/channels/unmapped'),
  listEPGIDs: () => api.requestList<string>('/tv/epg-ids'),
  // Include disabled channels in the listing (admin view).
  listAllChannels: () => api.requestList<LiveTVChannel>('/tv/channels?enabled=false'),
  setChannelEnabled: (channelId: string, enabled: boolean) =>
    api.patch<void>(`/tv/channels/${channelId}`, { enabled }),
  reorderChannels: (channelIDs: string[]) =>
    api.put<void>('/tv/channels/order', { channel_ids: channelIDs }),

  // Live broadcasts ("go live" / RTMP ingest). A broadcast is an rtmp tuner
  // device; delete it with deleteTuner(tuner_id).
  listBroadcasts: () => api.requestList<LiveTVBroadcast>('/tv/broadcasts'),
  createBroadcast: (name: string) =>
    api.post<LiveTVBroadcast>('/tv/broadcasts', { name }),

  // DVR.
  listSchedules: () => api.requestList<LiveTVSchedule>('/tv/schedules'),
  createSchedule: (body: Partial<LiveTVSchedule> & { type: string }) =>
    api.post<LiveTVSchedule>('/tv/schedules', body),
  deleteSchedule: (id: string) => api.delete(`/tv/schedules/${id}`),
  listRecordings: (status?: string) =>
    api.requestList<LiveTVRecording>('/tv/recordings' + (status ? `?status=${status}` : '')),
  cancelRecording: (id: string) => api.delete(`/tv/recordings/${id}`),
};

export interface LiveTVBroadcast {
  tuner_id: string;
  channel_id?: string;
  name: string;
  stream_key: string;
  ingest_url: string;
  enabled: boolean;
  is_live: boolean;
}

export interface LiveTVSchedule {
  id: string;
  type: 'once' | 'series' | 'channel_block';
  program_id?: string;
  channel_id?: string;
  title_match?: string;
  new_only: boolean;
  time_start?: string;
  time_end?: string;
  padding_pre_sec: number;
  padding_post_sec: number;
  priority: number;
  retention_days?: number;
  enabled: boolean;
}

export interface LiveTVRecording {
  id: string;
  schedule_id?: string;
  channel_id: string;
  channel_number: string;
  channel_name: string;
  channel_logo?: string;
  program_id?: string;
  title: string;
  subtitle?: string;
  season_num?: number;
  episode_num?: number;
  status: 'scheduled' | 'recording' | 'completed' | 'failed' | 'cancelled' | 'superseded';
  starts_at: string;
  ends_at: string;
  item_id?: string;
  error?: string;
}

export interface LiveTVEPGSource {
  id: string;
  type: string;
  name: string;
  config: Record<string, unknown>;
  refresh_interval_min: number;
  enabled: boolean;
  last_pull_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

// ── Analytics ─────────────────────────────────────────────────────────────────

export interface AnalyticsOverview {
  total_items: number;
  total_files: number;
  total_size_bytes: number;
  total_plays: number;
  total_watch_time_ms: number;
}

export interface LibraryAnalytics {
  id: string;
  name: string;
  type: string;
  item_count: number;
  total_size_bytes: number;
  res_4k: number;
  res_1080p: number;
  res_720p: number;
  res_sd: number;
}

export interface CodecCount   { codec: string;     count: number; }
export interface ContainerCount { container: string; count: number; }
export interface DayCount     { date: string;       count: number; }
export interface DayBytes     { date: string;       bytes: number; }

export interface TopPlayedItem {
  id: string;
  title: string;
  year?: number;
  type: string;
  poster_path?: string;
  parent_title?: string; // show/artist for episodes/tracks
  play_count: number;
}

export interface RecentPlay {
  title: string;
  year?: number;
  type: string;
  parent_title?: string;
  user_name?: string;
  occurred_at: string; // RFC 3339, UTC
  client_name?: string;
  duration_ms?: number;
}

export interface TopUser {
  user_id: string;
  username: string;
  play_count: number;
  watch_time_ms: number;
}

export interface ClientCount { client: string; count: number; }
export interface HourCount  { hour: number;  count: number; }

export interface CompletionStats {
  plays: number;      // plays of media with a known runtime
  completed: number;  // final position reached >= 90%
}

export interface DayStreamTypes {
  date: string;
  direct_play?: number;   // directPlay (absent on servers before the split)
  direct_stream?: number; // directStream + remux (absent on servers before the split)
  transcode: number;
  unknown: number;        // no decision attributed (older history, or a client that didn't report one)
  direct: number;         // direct_play + direct_stream — kept for backward compatibility
}

// Same four-way split as DayStreamTypes, per client, over the page's range.
export interface ClientStreamTypes {
  client: string;         // client_name, or 'Unknown client'
  direct_play: number;
  direct_stream: number;
  transcode: number;
  unknown: number;
  total: number;
}

export interface StreamTotals {
  direct_play: number;
  direct_stream: number;
  transcode: number;
  unknown: number;
}

export interface AnalyticsData {
  range_days: number;
  overview: AnalyticsOverview;
  libraries: LibraryAnalytics[];
  video_codecs: CodecCount[];
  containers: ContainerCount[];
  plays_by_day: DayCount[];
  bandwidth_by_day: DayBytes[];
  top_played: TopPlayedItem[];
  recent_plays: RecentPlay[];
  top_users: TopUser[];
  clients: ClientCount[];
  plays_by_hour: HourCount[];
  completion: CompletionStats;
  stream_types_by_day: DayStreamTypes[];
  stream_types_by_client?: ClientStreamTypes[]; // top 8 by total; absent on older servers
  stream_totals?: StreamTotals;                 // absent on older servers
}

export const analyticsApi = {
  // tz makes the server bucket the per-day charts in the viewer's local days;
  // days selects the 7/30/90 window; refresh bypasses the server-side
  // 5-minute cache (manual refresh button).
  get: (opts?: { refresh?: boolean; days?: number }) => {
    const params = new URLSearchParams();
    try {
      params.set('tz', Intl.DateTimeFormat().resolvedOptions().timeZone);
    } catch { /* no Intl zone — server defaults to UTC */ }
    if (opts?.days) params.set('days', String(opts.days));
    if (opts?.refresh) params.set('refresh', 'true');
    return api.get<AnalyticsData>(`/analytics?${params}`);
  }
};

// ── Webhooks ──────────────────────────────────────────────────────────────────

export interface WebhookEndpoint {
  id: string;
  url: string;
  events: string[];
  enabled: boolean;
}

export const webhookApi = {
  list: () => api.requestList<WebhookEndpoint>('/webhooks'),
  create: (body: { url: string; secret?: string; events: string[] }) =>
    api.post<WebhookEndpoint>('/webhooks', body),
  update: (id: string, body: { url?: string; secret?: string; events?: string[]; enabled?: boolean }) =>
    api.patch<WebhookEndpoint>(`/webhooks/${id}`, body),
  del: (id: string) => api.del(`/webhooks/${id}`),
  test: (id: string) => api.post<void>(`/webhooks/${id}/test`)
};

// ── Notification agents (admin) ───────────────────────────────────────────────
// Server-wide Discord / Telegram / ntfy / Gotify / email channels
// (Settings → Notifications). Secrets are write-only: responses carry
// secret_configured, never the value.

export type NotificationAgentKind = 'discord' | 'telegram' | 'ntfy' | 'gotify' | 'email';

export interface NotificationAgentConfig {
  chat_id?: string;
  server_url?: string;
  topic?: string;
  recipients?: string[];
}

export interface NotificationAgent {
  id: string;
  kind: NotificationAgentKind;
  name: string;
  enabled: boolean;
  events: string[];
  config: NotificationAgentConfig;
  secret_configured: boolean;
  allow_private_network: boolean;
  last_success_at: string | null;
  last_error: string | null;
  last_error_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface NotificationAgentEvent {
  key: string;
  label: string;
  description: string;
  group: 'requests' | 'library' | 'server';
  default_for: NotificationAgentKind[];
}

export interface NotificationAgentCreateInput {
  kind: NotificationAgentKind;
  name: string;
  enabled?: boolean;
  events?: string[];
  config?: NotificationAgentConfig;
  secret?: string;
  allow_private_network?: boolean;
}

export interface NotificationAgentUpdateInput {
  name?: string;
  enabled?: boolean;
  events?: string[];
  config?: NotificationAgentConfig;
  /** Blank or omitted keeps the stored secret. */
  secret?: string;
  clear_secret?: boolean;
  allow_private_network?: boolean;
}

export interface NotificationAgentTestResult {
  ok: boolean;
  error?: string;
}

export const notificationAgentApi = {
  list: () => api.requestList<NotificationAgent>('/admin/notification-agents'),
  events: () => api.get<NotificationAgentEvent[]>('/admin/notification-agents/events'),
  create: (body: NotificationAgentCreateInput) =>
    api.post<NotificationAgent>('/admin/notification-agents', body),
  update: (id: string, body: NotificationAgentUpdateInput) =>
    api.patch<NotificationAgent>(`/admin/notification-agents/${id}`, body),
  del: (id: string) => api.del(`/admin/notification-agents/${id}`),
  test: (id: string) => api.post<NotificationAgentTestResult>(`/admin/notification-agents/${id}/test`)
};

// ── Plugins (admin) ───────────────────────────────────────────────────────────

export type PluginRole = 'notification' | 'metadata' | 'task';

export interface Plugin {
  id: string;
  name: string;
  role: PluginRole;
  transport: string;
  endpoint_url: string;
  allowed_hosts: string[];
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface PluginCreateInput {
  name: string;
  role: PluginRole;
  endpoint_url: string;
  allowed_hosts?: string[];
  enabled?: boolean;
}

export interface PluginUpdateInput {
  name?: string;
  endpoint_url?: string;
  allowed_hosts?: string[];
  enabled?: boolean;
}

export const pluginApi = {
  list: () => api.requestList<Plugin>('/admin/plugins'),
  create: (body: PluginCreateInput) => api.post<Plugin>('/admin/plugins', body),
  update: (id: string, body: PluginUpdateInput) => api.patch<Plugin>(`/admin/plugins/${id}`, body),
  del: (id: string) => api.del(`/admin/plugins/${id}`),
  test: (id: string) => api.post<void>(`/admin/plugins/${id}/test`)
};

// ── Active Sessions ───────────────────────────────────────────────────────────

export interface ActiveSession {
  id: string;
  decision: string;
  position_ms: number;
  client_name?: string;
  started_at: string;
  title: string;
  year?: number;
  type?: string;
  poster_path?: string;
  parent_title?: string; // show/artist for episodes/tracks
  duration_ms?: number;
  bitrate_kbps?: number;
  selected_rendition?: string; // ABR rung the player settled on
  // ── Who / where / why (absent on older servers) ──
  user_id?: string;
  username?: string;        // owning account (for a managed profile, its owner)
  profile_name?: string;    // managed profile name, when the viewer is one
  client_ip?: string;       // admins only
  location?: 'lan' | 'remote';
  source?: StreamFormat;
  output?: StreamFormat;    // absent for direct play (the output IS the source)
  transcode_reasons?: string[];
  /** Whether POST /sessions/{id}/stop can act on this card. */
  can_stop?: boolean;
}

/** One side of a Now Playing card's source → output line. Codec/container
 *  values are scanner identifiers ("hevc", "truehd", "matroska"; "hls"). */
export interface StreamFormat {
  container?: string;
  video_codec?: string;
  audio_codec?: string;
  width?: number;
  height?: number;
  bitrate_kbps?: number;
  audio_channels?: number;
  hdr?: string;
}

export const sessionsApi = {
  list: () => api.get<ActiveSession[]>('/sessions'),
  /** Stop a session from the dashboard. No seg token needed — the Bearer
   *  identifies the caller; owners may stop their own sessions and ADMINS
   *  may stop anyone's (audit-logged server-side). The server revokes the
   *  seg token from the session record. */
  stop: (sessionId: string) => api.del(`/transcode/sessions/${sessionId}`),
  /** Admin "stop this stream" for EVERY playback mode (direct play too):
   *  tears down a server session, messages the viewer over SSE and briefly
   *  refuses that device's stream. 404 = the stream already ended. The id is
   *  percent-encoded (direct-play ids embed the client address, e.g. an IPv6
   *  "::1|item|user"); the server unescapes it before matching. */
  adminStop: (sessionId: string, message?: string) =>
    api.post<void>(`/sessions/${encodeURIComponent(sessionId)}/stop`, message ? { message } : {})
};

// ── Hub (home page) ──────────────────────────────────────────────────────────

export interface HubItem {
  id: string;
  title: string;
  /** Parent show name — set only for episode tiles in recently-added strips. */
  show_title?: string;
  type: string;
  year?: number;
  poster_path?: string;
  fanart_path?: string;
  thumb_path?: string;
  view_offset_ms?: number;
  duration_ms?: number;
  updated_at: number;
  /** Episode tiles in Next Up: position within the show. */
  season_number?: number;
  episode_number?: number;
  show_id?: string;
}

export interface HubLibraryRow {
  library_id: string;
  library_name: string;
  library_type: string;
  items: HubItem[];
}

/** A promoted collection on the home screen (layout key collection:<id>). */
export interface HubCollectionRow {
  collection_id: string;
  name: string;
  items: HubItem[];
}

export interface HubData {
  // Legacy combined feed; clients should prefer the split arrays
  // below. Kept for backward compatibility — same items as
  // continue_watching_tv + continue_watching_movies + continue_watching_other.
  continue_watching: HubItem[];
  // Pre-split rows: TV episodes (one tile per show), movies, and
  // a fallback bucket for audiobooks / podcasts / anything else
  // that ends up in-progress. Empty arrays when there's nothing.
  continue_watching_tv?: HubItem[];
  continue_watching_movies?: HubItem[];
  continue_watching_other?: HubItem[];
  recently_added: HubItem[];
  recently_added_by_library: HubLibraryRow[];
  // Global "what others are watching" row aggregated from watch_events
  // over the last 7 days. Same content for every user (no
  // personalisation), filtered to library access + parental ceiling.
  trending: HubItem[];
  // The next unwatched episode of each show the caller is part-way
  // through (last episode finished, next one not started).
  next_up?: HubItem[];
  // Items the caller marked Plan to Watch, newest first.
  plan_to_watch?: HubItem[];
  // Collections an admin put on the home screen, with the members the
  // caller can see (v2.6+).
  collection_rows?: HubCollectionRow[];
}

// What the Play button on a show or season should start.
//   resume  — an episode is part-watched
//   next    — the episode after the last one finished
//   start   — nothing watched yet (first episode)
//   rewatch — everything watched (first episode again)
//   none    — no playable episodes
export interface UpNext {
  mode: 'resume' | 'next' | 'start' | 'rewatch' | 'none';
  episode?: {
    id: string;
    title: string;
    season_id: string;
    season_number: number;
    episode_number: number;
    view_offset_ms?: number;
    duration_ms?: number;
    thumb_path?: string;
  };
}

export const hubApi = {
  get: () => api.get<HubData>('/hub')
};

// ── Search ────────────────────────────────────────────────────────────────────

export interface SearchResult {
  id: string;
  library_id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  thumb_path?: string;
}

export const searchApi = {
  search: (query: string, libraryId?: string, limit = 20) => {
    const params = new URLSearchParams({ q: query, limit: String(limit) });
    if (libraryId) params.set('library_id', libraryId);
    return api.get<SearchResult[]>(`/search?${params}`);
  }
};

// ── Watch History ─────────────────────────────────────────────────────────

export interface WatchHistoryItem {
  id: string;
  media_id: string;
  title: string;
  type: string;
  year?: number;
  thumb_path?: string;
  client_name?: string;
  duration_ms?: number;
  occurred_at: string;
}

export const historyApi = {
  list: (limit = 50, offset = 0) =>
    api.requestList<WatchHistoryItem>(`/history?limit=${limit}&offset=${offset}`)
};

// ── Transcode ─────────────────────────────────────────────────────────────────

export interface TranscodeSession {
  session_id: string;
  playlist_url: string;
  token: string;
  start_offset_sec: number;
  // Seconds to seek forward in the stream on startup to skip the
  // silent head of seg 0 when mid-stream AAC re-encode needs
  // warmup. Scrubber offset stays at start_offset_sec; the player
  // just starts playback at (start_offset_sec + seg0_audio_gap_sec).
  seg0_audio_gap_sec: number;
}

export const transcodeApi = {
  start: (itemId: string, height: number, positionMs: number, fileId?: string, videoCopy?: boolean, audioStreamIndex?: number, supportsHEVC?: boolean, supportsAV1?: boolean) =>
    api.post<TranscodeSession>(`/items/${itemId}/transcode`, {
      file_id: fileId,
      height,
      position_ms: positionMs,
      video_copy: videoCopy ?? false,
      audio_stream_index: audioStreamIndex ?? null,
      supports_hevc: supportsHEVC ?? false,
      supports_av1: supportsAV1 ?? false
    }),
  // Server-authoritative play decision (capability profiles). Returns the
  // server's verdict for a file given the client's X-Client-Capabilities header.
  // Currently consumed in shadow mode (compared against the local decision) —
  // see the watch page. docs/capability-profiles.md.
  decide: (itemId: string, fileId?: string) =>
    api.post<{ decision: string; file_id: string }>(
      `/items/${itemId}/playback-decision`, fileId ? { file_id: fileId } : {}),
  stop: (sessionId: string, token: string) =>
    api.del(`/transcode/sessions/${sessionId}?token=${encodeURIComponent(token)}`)
};

// ── Audit Log ─────────────────────────────────────────────────────────────────

export interface AuditLogEntry {
  id: string;
  user_id: string | null;
  action: string;
  target: string | null;
  detail: any;
  ip_addr: string | null;
  created_at: string;
}

export const auditApi = {
  list: (limit = 50, offset = 0) =>
    api.get<AuditLogEntry[]>(`/audit?limit=${limit}&offset=${offset}`)
};

// ── Notifications ────────────────────────────────────────────────────────────

export interface Notification {
  id: string;
  type: string;
  title: string;
  body: string;
  item_id?: string;
  read: boolean;
  created_at: number;
}

export const notificationApi = {
  list: (limit = 20, offset = 0) =>
    api.get<Notification[]>(`/notifications?limit=${limit}&offset=${offset}`),
  unreadCount: () =>
    api.get<{ count: number }>('/notifications/unread-count'),
  markRead: (id: string) =>
    api.post<void>(`/notifications/${id}/read`, {}),
  markAllRead: () =>
    api.post<void>('/notifications/read-all', {}),
};

// ── Scheduled Tasks (admin) ──────────────────────────────────────────────────

export interface ScheduledTask {
  id: string;
  name: string;
  task_type: string;
  config: Record<string, unknown>;
  cron_expr: string;
  enabled: boolean;
  last_run_at: string | null;
  next_run_at: string;
  last_status: string;
  last_error: string;
  created_at: string;
  updated_at: string;
}

export interface TaskRun {
  id: string;
  task_id: string;
  started_at: string;
  ended_at: string | null;
  status: string;
  output: string;
  error: string;
}

export interface CreateTaskBody {
  name: string;
  task_type: string;
  cron_expr: string;
  config?: Record<string, unknown>;
  enabled?: boolean;
}

export interface UpdateTaskBody {
  name?: string;
  task_type?: string;
  cron_expr?: string;
  config?: Record<string, unknown>;
  enabled?: boolean;
}

export const tasksApi = {
  list: () => api.get<ScheduledTask[]>('/admin/tasks'),
  types: () => api.get<string[]>('/admin/tasks/types'),
  create: (body: CreateTaskBody) => api.post<ScheduledTask>('/admin/tasks', body),
  update: (id: string, body: UpdateTaskBody) =>
    api.patch<ScheduledTask>(`/admin/tasks/${id}`, body),
  del: (id: string) => api.del(`/admin/tasks/${id}`),
  runNow: (id: string) => api.post<{ queued: boolean }>(`/admin/tasks/${id}/run`, {}),
  runs: (id: string, limit = 50) =>
    api.get<TaskRun[]>(`/admin/tasks/${id}/runs?limit=${limit}`)
};

// ── Discover (TMDB search for the request UI) ────────────────────────────────

export interface DiscoverItem {
  type: 'movie' | 'show';
  tmdb_id: number;
  title: string;
  year?: number;
  overview?: string;
  rating?: number;
  poster_url?: string;
  fanart_url?: string;
  in_library: boolean;
  library_item_id?: string;
  has_active_request: boolean;
  active_request_id?: string;
  active_request_status?: string;
  // Shows in the caller's library only: aired seasons the library doesn't
  // fully hold ([] = nothing missing). Absent otherwise.
  missing_seasons?: number[];
  // In the library with aired seasons missing ("Request more seasons").
  partially_available?: boolean;
}

/** One season of a show for the request season picker
 *  (GET /discover/tv/{tmdb_id}/seasons). Specials (0) come last. */
export interface SeasonInfo {
  season_number: number;
  name: string;
  episode_count: number;
  aired_episodes: number;
  air_date: string | null; // YYYY-MM-DD
  poster_url?: string;
  // Episodes on disk in libraries the caller can see.
  owned_episodes: number;
  // Status of the caller's active request covering this season, or null.
  requested: 'pending' | 'approved' | 'downloading' | null;
  request_id?: string;
}

export const discoverApi = {
  search: (q: string, limit = 20) =>
    api.get<DiscoverItem[]>(`/discover/search?q=${encodeURIComponent(q)}&limit=${limit}`),
  /** A show's seasons with library + request state, for the season picker. */
  seasons: (tmdbId: number) => api.get<SeasonInfo[]>(`/discover/tv/${encodeURIComponent(String(tmdbId))}/seasons`),
};

// ── Media Requests ───────────────────────────────────────────────────────────

export type RequestStatus =
  | 'pending'
  | 'approved'
  | 'declined'
  | 'downloading'
  | 'available'
  | 'failed';

export interface MediaRequest {
  id: string;
  user_id: string;
  type: 'movie' | 'show';
  tmdb_id: number;
  title: string;
  year?: number;
  poster_url?: string;
  overview?: string;
  status: RequestStatus;
  seasons?: number[];
  requested_service_id?: string;
  quality_profile_id?: number;
  root_folder?: string;
  service_id?: string;
  decline_reason?: string;
  decided_by?: string;
  decided_at?: string;
  fulfilled_item_id?: string;
  fulfilled_at?: string;
  // True when the server approved the request on creation (the requester
  // has auto-approve for this type, or is an admin) and handed it to
  // Radarr/Sonarr. A failed hand-off leaves it pending and false.
  auto_approved: boolean;
  created_at: string;
  updated_at: string;
  // Who asked. Set on admin (scope=all) listings.
  username?: string;
  // Live Radarr/Sonarr state for approved/downloading requests, refreshed
  // by the server's arr sync. Absent until the first sync after approval.
  download?: RequestDownload;
  // Show requests: requested seasons already complete in the library while
  // the rest are still coming ("partially available"; status is unchanged).
  seasons_available?: number[];
}

// searching      — monitored in *arr, no release grabbed yet
// queued         — grabbed, waiting in the download client
// downloading    — transferring (progress/eta set)
// import_pending — downloaded, waiting for *arr to import
// stalled        — the download client reports no progress / a warning
// failed         — the grab or import failed (message says why)
export interface RequestDownload {
  state: 'searching' | 'queued' | 'downloading' | 'import_pending' | 'stalled' | 'failed';
  progress?: number; // 0..1
  eta?: string; // RFC3339
  size_bytes?: number;
  message?: string;
  updated_at: string;
}

export interface CreateRequestBody {
  type: 'movie' | 'show';
  tmdb_id: number;
  seasons?: number[];
  requested_service_id?: string;
  quality_profile_id?: number;
  root_folder?: string;
}

export interface ApproveRequestBody {
  service_id?: string;
  quality_profile_id?: number;
  root_folder?: string;
}

function buildRequestsQuery(params: {
  scope?: 'mine' | 'all';
  status?: RequestStatus;
  limit?: number;
  offset?: number;
}): string {
  const qs = new URLSearchParams();
  if (params.scope === 'all') qs.set('scope', 'all');
  if (params.status) qs.set('status', params.status);
  qs.set('limit', String(params.limit ?? 50));
  qs.set('offset', String(params.offset ?? 0));
  return qs.toString();
}

export const requestsApi = {
  list: (params: { status?: RequestStatus; limit?: number; offset?: number } = {}) =>
    api.requestList<MediaRequest>(`/requests?${buildRequestsQuery({ scope: 'mine', ...params })}`),
  get: (id: string) => api.get<MediaRequest>(`/requests/${id}`),
  create: (body: CreateRequestBody) => api.post<CreatedRequest>('/requests', body),
  cancel: (id: string) => api.post<void>(`/requests/${id}/cancel`),
  /** The caller's own request allowance (can_request + per-type quota). */
  quota: () => api.get<RequestQuota>('/requests/quota'),
};

/** POST /requests response: the request plus whether it went over the
 *  requester's quota (and so waits for an admin instead of auto-approving). */
export type CreatedRequest = MediaRequest & { over_quota?: boolean };

export interface RequestQuotaUsage {
  limit: number; // 0 = unlimited
  used: number; // non-declined requests of this type in the window
  remaining: number | null; // null = unlimited
}

export interface RequestQuota {
  can_request: boolean;
  window_days: number;
  movies: RequestQuotaUsage;
  tv: RequestQuotaUsage;
}

export const requestsAdminApi = {
  list: (params: { status?: RequestStatus; limit?: number; offset?: number } = {}) =>
    api.requestList<MediaRequest>(`/requests?${buildRequestsQuery({ scope: 'all', ...params })}`),
  approve: (id: string, body: ApproveRequestBody = {}) =>
    api.post<MediaRequest>(`/admin/requests/${id}/approve`, body),
  decline: (id: string, reason?: string) =>
    api.post<MediaRequest>(`/admin/requests/${id}/decline`, { reason: reason ?? '' }),
  del: (id: string) => api.del(`/admin/requests/${id}`),
  /** Requests waiting for approval (admin nav badge). */
  pendingCount: () => api.get<{ count: number }>('/requests/pending-count'),
};

// ── Arr Services (admin) ─────────────────────────────────────────────────────

export type ArrServiceKind = 'radarr' | 'sonarr';

export interface ArrService {
  id: string;
  name: string;
  kind: ArrServiceKind;
  base_url: string;
  api_key_set: boolean;
  default_quality_profile_id?: number;
  default_root_folder?: string;
  default_tags: number[];
  minimum_availability?: string;
  series_type?: string;
  season_folder?: boolean;
  language_profile_id?: number;
  is_default: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ArrServiceCreate {
  name: string;
  kind: ArrServiceKind;
  base_url: string;
  api_key: string;
  default_quality_profile_id?: number | null;
  default_root_folder?: string | null;
  default_tags?: number[];
  minimum_availability?: string | null;
  series_type?: string | null;
  season_folder?: boolean | null;
  language_profile_id?: number | null;
  is_default?: boolean;
  enabled?: boolean;
}

export interface ArrServiceUpdate {
  name?: string;
  base_url?: string;
  api_key?: string;
  default_quality_profile_id?: number | null;
  default_root_folder?: string | null;
  default_tags?: number[];
  minimum_availability?: string | null;
  series_type?: string | null;
  season_folder?: boolean | null;
  language_profile_id?: number | null;
  enabled?: boolean;
}

export interface ArrQualityProfile { id: number; name: string; }
export interface ArrRootFolder     { id: number; path: string; free_space?: number; }
export interface ArrTag            { id: number; label: string; }
export interface ArrLanguageProfile { id: number; name: string; }

export interface ArrProbeResult {
  status: string;
  version?: string;
  app_name?: string;
  quality_profiles: ArrQualityProfile[];
  root_folders: ArrRootFolder[];
  tags: ArrTag[];
  language_profiles: ArrLanguageProfile[];
}

export interface ArrProbeBody {
  kind?: ArrServiceKind;
  base_url?: string;
  api_key?: string;
  service_id?: string;
}

export const arrServicesApi = {
  list: () => api.requestList<ArrService>('/admin/arr-services'),
  get: (id: string) => api.get<ArrService>(`/admin/arr-services/${id}`),
  create: (body: ArrServiceCreate) => api.post<ArrService>('/admin/arr-services', body),
  update: (id: string, body: ArrServiceUpdate) =>
    api.patch<ArrService>(`/admin/arr-services/${id}`, body),
  del: (id: string) => api.del(`/admin/arr-services/${id}`),
  setDefault: (id: string) =>
    api.post<ArrService>(`/admin/arr-services/${id}/set-default`, {}),
  probe: (body: ArrProbeBody) => api.post<ArrProbeResult>('/admin/arr-services/probe', body),
  /** Reachability, the instance's own health checks, disk space, queue size. */
  health: (id: string) => api.get<ArrServiceHealth>(`/admin/arr-services/${id}/health`),
};

export interface ArrHealthCheck {
  type: 'notice' | 'warning' | 'error';
  source: string;
  message: string;
  wiki_url?: string; // http(s) only
}

export interface ArrDisk {
  path: string;
  label: string;
  free_bytes: number;
  total_bytes: number;
}

export interface ArrServiceHealth {
  reachable: boolean;
  version?: string;
  error?: string; // short generic reason when !reachable
  checks: ArrHealthCheck[];
  disks: ArrDisk[]; // mounts behind the root folders
  queue_count: number | null; // null = queue unreadable
}

// ── Upcoming (arr calendar) ──────────────────────────────────────────────────

export type UpcomingKind = 'movie' | 'episode';
export type UpcomingReleaseType = 'cinema' | 'digital' | 'physical' | 'airing';
// downloaded = has a file; missing = date passed with no file; upcoming = still ahead.
export type UpcomingStatus = 'downloaded' | 'missing' | 'upcoming';

export interface UpcomingItem {
  id: string;                        // "radarr:<serviceId>:<movieId>:digital" etc. — unique per entry
  kind: UpcomingKind;
  title: string;                     // movie title, or the series title for episodes
  subtitle: string;                  // episodes: "S02E05 · Episode Title"; movies: ""
  release_type: UpcomingReleaseType;
  date: string;                      // RFC3339 UTC
  all_day: boolean;                  // true = a release DATE (UTC day); false = an air time (local day)
  status: UpcomingStatus;
  poster_url: string | null;         // https only
  year: number | null;
  overview: string;
  certification: string;
  network: string;                   // episodes only
  service: string;                   // the arr service's display name
  item_id: string | null;            // the movie / show already in OnScreen, when matched
}

export interface UpcomingService {
  name: string;
  kind: ArrServiceKind;
  ok: boolean;
  error: string;                     // short generic message when !ok
}

export interface UpcomingResponse {
  from: string;                      // YYYY-MM-DD
  to: string;                        // YYYY-MM-DD
  items: UpcomingItem[];             // sorted by date, then title
  services: UpcomingService[];       // one per enabled service; empty when none configured
}

export const upcomingApi = {
  // from / to are inclusive YYYY-MM-DD calendar days; the server caps the span at 62 days.
  get: (from: string, to: string) =>
    api.get<UpcomingResponse>(
      `/upcoming?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
};
