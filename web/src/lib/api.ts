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

/** Set or clear the in-memory bearer token. The Tauri shell also
 *  persists it to its store, but this in-memory copy is what the
 *  fetch wrapper reads on every call so the persistence layer
 *  isn't on the hot path. */
export function setBearerToken(access: string | null, refresh: string | null = null) {
  bearerToken = access;
  refreshTokenStore = refresh;
}

/** Read the cached bearer token. Used by the native audio engine
 *  wiring to send Authorization on Rust-initiated HTTP fetches
 *  (cpal pipeline can't read api.ts internals through fetch since
 *  it owns its own ureq client). Returns null in browser builds
 *  where bearer auth isn't used. */
export function getBearerToken(): string | null { return bearerToken; }

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
 *    the configured server origin AND appends `&token=<bearer>` (or
 *    `?token=` if no other params) when a bearer is cached. The
 *    server's RequiredAllowQueryToken middleware accepts the query-
 *    string token specifically for asset routes (where `<img>` /
 *    `<audio>` can't carry an Authorization header). The token-in-
 *    URL trade-off is scoped to assets — leaks (logs, Referer)
 *    don't grant general API access because regular API routes
 *    still require Bearer/cookie.
 *
 *  Caller-supplied tokens (e.g. transcode segment tokens) take
 *  precedence — the helper doesn't overwrite an existing `token=`
 *  parameter.
 */
export function assetUrl(path: string): string {
  if (apiBase.startsWith('/')) return path;
  const base = apiBase.replace(/\/api\/v1\/?$/, '') + path;
  if (!bearerToken || /[?&]token=/.test(base)) return base;
  return base + (base.includes('?') ? '&' : '?') + 'token=' + encodeURIComponent(bearerToken);
}

/** Build the request headers, attaching Authorization when a bearer
 *  token is cached. Browser builds skip the bearer (cookies cover
 *  auth there). */
function authHeaders(): Record<string, string> {
  const h: Record<string, string> = { 'Content-Type': 'application/json' };
  if (bearerToken) h.Authorization = `Bearer ${bearerToken}`;
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
async function persistTokensIfTauri(access: string, refresh: string): Promise<void> {
  setBearerToken(access, refresh);
  try {
    const { isTauri, setStoredTokens } = await import('./native');
    if (isTauri()) await setStoredTokens(access, refresh);
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
      this.setUser(null);
      window.location.href = '/login';
      return undefined as T;
    }

    return parseResponse(resp);
  }

  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
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
          throw new Error(err.error?.message ?? `HTTP ${resp.status}`);
        }
        return (json as ApiResponse<T>).data;
      }
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
          throw new Error(err.error?.message ?? `HTTP ${resp.status}`);
        }
        const envelope = json as ApiListResponse<T>;
        return { items: envelope.data ?? [], total: envelope.meta?.total ?? 0 };
      }
    );
  }

  private async tryRefresh(): Promise<boolean> {
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
      const resp = await fetch(apiBase + '/auth/refresh', {
        method: 'POST',
        headers: authHeaders(),
        credentials: credentialsMode(),
        body: refreshTokenStore ? JSON.stringify(body) : undefined,
      });
      if (!resp.ok) {
        return false;
      }
      const json = (await resp.json()) as ApiResponse<TokenPair>;
      const pair = json.data;
      // Update stored user metadata + bearer cache. The persistent
      // store update is fire-and-forget — if it fails we still have
      // the in-memory copy and the user can re-login next launch.
      this.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
      void persistTokensIfTauri(pair.access_token, pair.refresh_token);
      return true;
    } catch {
      return false;
    }
  }

  get = <T>(path: string) => this.request<T>('GET', path);
  post = <T>(path: string, body?: unknown) => this.request<T>('POST', path, body);
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

// ── Auth ──────────────────────────────────────────────────────────────────────

export interface TokenPair {
  access_token: string;
  refresh_token: string;
  expires_at: string;
  user_id: string;
  username: string;
  is_admin: boolean;
}

/** Capture the access + refresh tokens from a successful auth call
 *  so subsequent requests carry the bearer header. Browser builds
 *  short-circuit (the in-memory cache is unread there since the
 *  cookie path covers auth). */
async function captureTokens(pair: TokenPair): Promise<TokenPair> {
  await persistTokensIfTauri(pair.access_token, pair.refresh_token);
  return pair;
}

export const authApi = {
  setupStatus: () => api.get<{ setup_required: boolean }>('/setup/status'),
  login: async (username: string, password: string) =>
    captureTokens(await api.post<TokenPair>('/auth/login', { username, password })),
  register: (username: string, password: string, email?: string) =>
    api.post<{ id: string; username: string }>('/auth/register', { username, password, email }),
  logout: async () => {
    try {
      await api.post('/auth/logout');
    } finally {
      // Clear bearer + persisted tokens regardless of whether the
      // server-side logout succeeded — a leaked refresh token is a
      // worse outcome than a transient API error.
      setBearerToken(null, null);
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
    captureTokens(await api.post<TokenPair>('/auth/ldap/login', { username, password })),
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
    api.put<void>(`/users/${userId}/libraries`, { library_ids: libraryIds })
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
}

// ── Libraries ─────────────────────────────────────────────────────────────────

export interface Library {
  id: string;
  name: string;
  type: 'movie' | 'show' | 'music' | 'photo' | 'dvr' | 'audiobook' | 'podcast' | 'home_video' | 'book';
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
  capabilities: string[];
}

export interface FleetStatus {
  embedded_enabled: boolean;
  embedded_disabled_by_env: boolean;
  embedded_encoder: string;
  embedded_online: boolean;
  embedded_active_sessions: number;
  embedded_max_sessions: number;
  embedded_capabilities: string[];
  workers: FleetWorkerStatus[];
}

export interface TranscodeConfig {
  nvenc_preset: string;
  nvenc_tune: string;
  nvenc_rc: string;
  maxrate_ratio: number;
}

export const settingsApi = {
  get: () => api.get<ServerSettings>('/settings'),
  update: (body: Partial<ServerSettings>) => api.patch<void>('/settings', body),
  getEncoders: () => api.get<EncoderInfo>('/settings/encoders'),
  getWorkers: () => api.get<WorkerInfo[]>('/settings/workers'),
  getFleet: () => api.get<FleetStatus>('/settings/fleet'),
  updateFleet: (body: FleetConfig) => api.put<void>('/settings/fleet', body),
  getTranscodeConfig: () => api.get<TranscodeConfig>('/settings/transcode-config'),
  updateTranscodeConfig: (body: TranscodeConfig) => api.put<void>('/settings/transcode-config', body),
  testEmail: (to: string) => api.post<{ message: string }>('/email/test', { to }),
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
  enrichItem: (id: string) =>
    api.post<void>(`/items/${id}/enrich`)
};

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
  year?: number;
  summary?: string;
  rating?: number;
  duration_ms?: number;
  poster_path?: string;
  fanart_path?: string;
  content_rating?: string;
  genres: string[];
  parent_id?: string;
  index?: number;
  view_offset_ms: number;
  updated_at: number;
  is_favorite: boolean;
  files: ItemFile[];
  markers?: Marker[];
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
      state
    }),
  searchMatch: (id: string, query: string) =>
    api.get<MatchCandidate[]>(`/items/${id}/match/search?query=${encodeURIComponent(query)}`),
  applyMatch: (id: string, tmdbId: number) =>
    api.post<void>(`/items/${id}/match`, { tmdb_id: tmdbId }),
  addFavorite: (id: string) => api.post<void>(`/items/${id}/favorite`, {}),
  removeFavorite: (id: string) => api.delete(`/items/${id}/favorite`),
  listMarkers: (id: string) => api.requestList<Marker>(`/items/${id}/markers`),
  upsertMarker: (id: string, kind: 'intro' | 'credits', startMs: number, endMs: number) =>
    api.put<Marker>(`/items/${id}/markers/${kind}`, { start_ms: startMs, end_ms: endMs }),
  deleteMarker: (id: string, kind: 'intro' | 'credits') =>
    api.delete(`/items/${id}/markers/${kind}`)
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
  continue_watching: HubItem[];
  recently_added: HubItem[];
  recently_added_by_library: HubLibraryRow[];
  // Global "what others are watching" row aggregated from watch_events
  // over the last 7 days. Same content for every user (no
  // personalisation), filtered to library access + parental ceiling.
  trending: HubItem[];
  // Per-user recommendations: for each of the user's most recent
  // completed items (the seeds), the top items most cooccurrent with
  // it. One row per seed, rendered as "Because you watched {seed.title}"
  // on the home hub. Empty array for users who haven't completed
  // anything yet (the section just doesn't render).
  because_you_watched: HubBecauseYouWatched[];
}

export interface HubBecauseYouWatched {
  seed: HubSeedItem;
  items: HubItem[];
}

export interface HubSeedItem {
  id: string;
  title: string;
  poster_path?: string;
  thumb_path?: string;
  updated_at: number;
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
  start: (itemId: string, height: number, positionMs: number, fileId?: string, videoCopy?: boolean, audioStreamIndex?: number, supportsHEVC?: boolean) =>
    api.post<TranscodeSession>(`/items/${itemId}/transcode`, {
      file_id: fileId,
      height,
      position_ms: positionMs,
      video_copy: videoCopy ?? false,
      audio_stream_index: audioStreamIndex ?? null,
      supports_hevc: supportsHEVC ?? false
    }),
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
