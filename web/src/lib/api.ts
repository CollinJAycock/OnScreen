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
  setBearerToken(access, refresh, asset || null);
  try {
    const { isTauri, setStoredTokens } = await import('./native');
    if (isTauri()) await setStoredTokens(access, refresh, asset);
  } catch {
    // Tauri not present or store IPC unavailable — in-memory tokens
    // still work for this session.
  }
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

export interface UserMeta {
  user_id: string;
  username: string;
  is_admin: boolean;
}

export class ApiClient {
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
   * it attempts a single silent token refresh before redirecting to login.
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
        if (!this.refreshPromise) {
          this.refreshPromise = this.tryRefresh().finally(() => {
            this.refreshPromise = null;
          });
        }
        refreshed = await this.refreshPromise;
      } catch {
        refreshed = false;
      }
      if (refreshed) {
        return this.requestWithRetry(path, doFetch, parseResponse, false);
      }
      // Refresh failed → tokens are dead (server-side session purge,
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
  ): Promise<T> {
    const finalPath = this.withViewAs(method, path);
    return this.requestWithRetry(
      finalPath,
      () => fetch(apiBase + finalPath, {
        method,
        headers: authHeaders(),
        credentials: credentialsMode(),
        body: body ? JSON.stringify(body) : undefined
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

  private async tryRefresh(): Promise<boolean> {
    // Network-level failure → throw so the caller's catch keeps
    // tokens around for the next retry. Server-rejection (4xx) →
    // return false; the caller treats that as "tokens are dead"
    // and tears down. Without this distinction a flaky LAN would
    // log a user out after one failed refresh, even though their
    // tokens are perfectly valid.
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
      });
    } catch (e) {
      // Network error / DNS failure / fetch aborted — propagate so
      // the caller's catch keeps the user logged in for retry. The
      // /api/v1 hub poll on app foreground will re-trigger refresh
      // when the network comes back.
      throw e;
    }
    if (!resp.ok) {
      // 401 (refresh token rejected) or 5xx (server temporarily
      // sad) — both surface as "refresh failed, tokens dead" today.
      // Distinguishing 5xx as recoverable would need a queue; for
      // now we treat all non-2xx as terminal and let the user
      // re-login. The keychain clear in the caller means they
      // start clean rather than re-loading a stale token.
      return false;
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
    } catch {
      // Malformed body — server returned 2xx but we can't parse it.
      // Treat as failed refresh; the caller's tear-down path runs.
      return false;
    }
  }

  get = <T>(path: string) => this.request<T>('GET', path);
  post = <T>(path: string, body?: unknown) => this.request<T>('POST', path, body);
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
      await api.post('/auth/logout');
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
    api.post<{ status: string; device_name: string }>('/auth/pair/claim', { pin, device_name })
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
  requests: boolean;
  live_tv: boolean;
  dvr: boolean;
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

/** Per-user external-scrobble status. The token itself is never returned by
 *  the server — only whether one is linked and whether export is on. */
export interface ScrobbleStatus {
  listenbrainz_linked: boolean;
  listenbrainz_enabled: boolean;
}

export const scrobbleApi = {
  status: () => api.get<ScrobbleStatus>('/users/me/scrobble'),
  /** Link or update the ListenBrainz token. An empty token unlinks. */
  setListenBrainz: (token: string, enabled: boolean) =>
    api.put<void>('/users/me/scrobble/listenbrainz', { token, enabled })
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
  // Optional on the wire: server returns it always, but the
  // setPreferences PUT body can omit it (server treats absent as
  // "leave unchanged" via SQL COALESCE).
  episode_use_show_poster?: boolean;
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
  created_at: string;
  updated_at: string;
}

export const libraryApi = {
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
}

export type SortField = 'title' | 'year' | 'rating' | 'created_at' | 'taken_at';

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
}

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

export interface ServerSettings {
  tmdb_api_key: string;
  tvdb_api_key: string;
  arr_api_key: string;
  arr_webhook_url: string;
  arr_path_mappings?: Record<string, string>;
  transcode_encoders: string;
  web_downloads_enabled: boolean;
  opensubtitles: OpenSubtitlesSettings;
  oidc: OIDCSettings;
  ldap: LDAPSettings;
  saml: SAMLSettings;
  smtp: SMTPSettings;
  otel: OTelSettings;
  general: GeneralSettings;
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
    return api.requestList<MediaItem>(`/libraries/${libraryId}/items?${qs.toString()}`);
  },
  genres: (libraryId: string) =>
    api.get<GenreCount[]>(`/libraries/${libraryId}/genres`),
  years: (libraryId: string) =>
    api.get<YearCount[]>(`/libraries/${libraryId}/years`),
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
  rating?: number;
  duration_ms?: number;
  poster_path?: string;
  fanart_path?: string;
  content_rating?: string;
  genres: string[];
  parent_id?: string;
  grandparent_id?: string;
  index?: number;
  view_offset_ms: number;
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
  type: 'auto_genre' | 'playlist';
  genre?: string;
  poster_path?: string;
  created_at: string;
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
  list: () => api.get<Collection[]>('/collections'),
  get: (id: string) => api.get<Collection>(`/collections/${id}`),
  create: (name: string, description?: string) =>
    api.post<Collection>('/collections', { name, description }),
  update: (id: string, name: string, description?: string) =>
    api.patch<Collection>(`/collections/${id}`, { name, description }),
  delete: (id: string) => api.delete(`/collections/${id}`),
  items: (id: string, limit = 50, offset = 0) =>
    api.requestList<CollectionItem>(`/collections/${id}/items?limit=${limit}&offset=${offset}`),
  addItem: (collectionId: string, mediaItemId: string) =>
    api.post<void>(`/collections/${collectionId}/items`, { media_item_id: mediaItemId }),
  removeItem: (collectionId: string, itemId: string) =>
    api.delete(`/collections/${collectionId}/items/${itemId}`),
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
  progress: (id: string, viewOffsetMs: number, durationMs: number, state: 'playing' | 'paused' | 'stopped') =>
    api.put<void>(`/items/${id}/progress`, {
      view_offset_ms: viewOffsetMs,
      duration_ms: durationMs,
      state,
      // client_name drives the device picker in the cross-device
      // "Play on…" remote control. Browser builds derive a label
      // from userAgent + a "Web —" prefix so the user can tell the
      // browser apart from native clients in the picker.
      client_name: getClientName(),
    }),
  searchMatch: (id: string, query: string) =>
    api.get<MatchCandidate[]>(`/items/${id}/match/search?query=${encodeURIComponent(query)}`),
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

  // DVR.
  listSchedules: () => api.requestList<LiveTVSchedule>('/tv/schedules'),
  createSchedule: (body: Partial<LiveTVSchedule> & { type: string }) =>
    api.post<LiveTVSchedule>('/tv/schedules', body),
  deleteSchedule: (id: string) => api.delete(`/tv/schedules/${id}`),
  listRecordings: (status?: string) =>
    api.requestList<LiveTVRecording>('/tv/recordings' + (status ? `?status=${status}` : '')),
  cancelRecording: (id: string) => api.delete(`/tv/recordings/${id}`),
};

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
  play_count: number;
}

export interface RecentPlay {
  title: string;
  year?: number;
  type: string;
  occurred_at: string;
  client_name?: string;
  duration_ms?: number;
}

export interface AnalyticsData {
  overview: AnalyticsOverview;
  libraries: LibraryAnalytics[];
  video_codecs: CodecCount[];
  containers: ContainerCount[];
  plays_by_day: DayCount[];
  bandwidth_by_day: DayBytes[];
  top_played: TopPlayedItem[];
  recent_plays: RecentPlay[];
}

export const analyticsApi = {
  get: () => api.get<AnalyticsData>('/analytics')
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
  duration_ms?: number;
  bitrate_kbps?: number;
}

export const sessionsApi = {
  list: () => api.get<ActiveSession[]>('/sessions')
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
}

export interface HubLibraryRow {
  library_id: string;
  library_name: string;
  library_type: string;
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
}

export const discoverApi = {
  search: (q: string, limit = 20) =>
    api.get<DiscoverItem[]>(`/discover/search?q=${encodeURIComponent(q)}&limit=${limit}`),
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
  created_at: string;
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
  create: (body: CreateRequestBody) => api.post<MediaRequest>('/requests', body),
  cancel: (id: string) => api.post<void>(`/requests/${id}/cancel`),
};

export const requestsAdminApi = {
  list: (params: { status?: RequestStatus; limit?: number; offset?: number } = {}) =>
    api.requestList<MediaRequest>(`/requests?${buildRequestsQuery({ scope: 'all', ...params })}`),
  approve: (id: string, body: ApproveRequestBody = {}) =>
    api.post<MediaRequest>(`/admin/requests/${id}/approve`, body),
  decline: (id: string, reason?: string) =>
    api.post<MediaRequest>(`/admin/requests/${id}/decline`, { reason: reason ?? '' }),
  del: (id: string) => api.del(`/admin/requests/${id}`),
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
};
