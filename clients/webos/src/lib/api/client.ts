// OnScreen API client for the webOS TV app.
//
// Differences from web/src/lib/api.ts:
// - Bearer tokens (not cookies) — TV runs cross-origin, can't share cookies.
// - Configurable origin — user enters their server URL on first launch.
// - No /login redirect — on 401, surface to caller; route decides.

import { clientCapabilitiesHeader } from './capabilities';

const ORIGIN_KEY = 'onscreen.api_origin';
const TOKEN_KEY = 'onscreen.access_token';
const REFRESH_KEY = 'onscreen.refresh_token';
// purpose=asset token. Goes in `?token=` on asset URLs instead of the
// general access token — the server rejects a general token in a URL.
const ASSET_KEY = 'onscreen.asset_token';
const USER_KEY = 'onscreen.user';
// Wall-clock ceiling on a single API request. Long enough for a cold
// transcode-start on a slow box, short enough that a dead connection
// surfaces an error instead of hanging the UI indefinitely.
const REQUEST_TIMEOUT_MS = 20_000;

export interface UserMeta {
  user_id: string;
  username: string;
  is_admin: boolean;
}

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
   *  a second factor. Tokens are empty; post the code +
   *  login_challenge_token to totpVerify to finish. */
  totp_required?: boolean;
  login_challenge_token?: string;
}

/**
 * True when `hostname` can only be a local-network (or loopback) address:
 * RFC1918 / CGNAT (Tailscale) / link-local / loopback IPv4, ULA / link-local
 * / loopback IPv6, single-label names and the conventional local suffixes.
 * Used to decide whether a plaintext http:// origin is the usual self-hosted
 * LAN setup (allowed, flagged as unencrypted) or a server reached across the
 * internet in cleartext (passwords + refresh tokens sniffable on any hop —
 * needs explicit confirmation).
 */
export function isLocalNetworkHost(hostname: string): boolean {
  const h = hostname.toLowerCase().replace(/^\[/, '').replace(/\]$/, '').replace(/\.$/, '');
  if (!h) return false;
  if (h === 'localhost' || h.endsWith('.localhost')) return true;
  if (/\.(local|lan|home\.arpa|internal)$/.test(h)) return true;
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    return (
      a === 10 ||
      a === 127 ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 169 && b === 254) ||
      (a === 100 && b >= 64 && b <= 127)
    );
  }
  if (h.includes(':')) {
    return h === '::1' || /^f[cd][0-9a-f]{2}:/.test(h) || /^fe[89ab][0-9a-f]:/.test(h);
  }
  // Single-label names ("nas", "plexbox") only resolve via local DNS.
  return !h.includes('.');
}

/** True when `origin` is plaintext http:// to a host that is NOT on the
 *  local network — credentials would cross the internet unencrypted. */
export function isCleartextRemote(origin: string): boolean {
  try {
    const u = new URL(origin);
    return u.protocol === 'http:' && !isLocalNetworkHost(u.hostname);
  } catch {
    return false;
  }
}

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export class Unauthorized extends ApiError {
  constructor() {
    super(401, 'UNAUTHORIZED', 'not authenticated');
  }
}

export class ApiClient {
  private refreshing: Promise<boolean> | null = null;

  getOrigin(): string | null {
    return localStorage.getItem(ORIGIN_KEY);
  }

  setOrigin(origin: string) {
    localStorage.setItem(ORIGIN_KEY, origin.replace(/\/$/, ''));
  }

  getToken(): string | null {
    return localStorage.getItem(TOKEN_KEY);
  }

  /** The purpose=asset token used for `?token=` on asset URLs. */
  getAssetToken(): string | null {
    return localStorage.getItem(ASSET_KEY);
  }

  getUser(): UserMeta | null {
    const raw = localStorage.getItem(USER_KEY);
    if (!raw) return null;
    try {
      return JSON.parse(raw) as UserMeta;
    } catch {
      return null;
    }
  }

  setTokens(pair: TokenPair) {
    localStorage.setItem(TOKEN_KEY, pair.access_token);
    localStorage.setItem(REFRESH_KEY, pair.refresh_token);
    if (pair.asset_token) {
      localStorage.setItem(ASSET_KEY, pair.asset_token);
    } else {
      localStorage.removeItem(ASSET_KEY);
    }
    localStorage.setItem(
      USER_KEY,
      JSON.stringify({
        user_id: pair.user_id,
        username: pair.username,
        is_admin: pair.is_admin
      })
    );
  }

  clearTokens() {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(REFRESH_KEY);
    localStorage.removeItem(ASSET_KEY);
    localStorage.removeItem(USER_KEY);
  }

  async login(username: string, password: string): Promise<TokenPair> {
    const pair = await this.raw<TokenPair>('POST', '/api/v1/auth/login', { username, password });
    // A TOTP-gated account returns no tokens yet — the caller drives the
    // second-factor step and calls totpVerify, which persists tokens.
    if (!pair.totp_required) this.setTokens(pair);
    return pair;
  }

  /** Second step of a two-factor login: exchange the challenge token +
   *  code (or recovery code) for a real token pair. */
  async totpVerify(loginChallengeToken: string, code: string): Promise<TokenPair> {
    const pair = await this.raw<TokenPair>('POST', '/api/v1/auth/totp/verify', {
      login_challenge_token: loginChallengeToken,
      code,
    });
    this.setTokens(pair);
    return pair;
  }

  /** Revoke the session server-side, then drop local tokens.
   *
   *  The server only honours a JSON-body refresh_token on /auth/logout when
   *  the request carries a VALID bearer (Auth.Optional — an expired bearer is
   *  silently treated as anonymous and the body ignored, still 204). A bare
   *  unauthenticated POST therefore revoked nothing and the 30-day refresh
   *  token outlived "Sign out". Rotate first so the bearer is guaranteed
   *  fresh, then revoke the rotated refresh token with it. Best-effort:
   *  local tokens are always cleared, and errors never block sign-out.
   *  Must run BEFORE the caller clears/changes the origin so the revoke
   *  goes to the server that issued the token. */
  async logout(): Promise<void> {
    try {
      // Share an in-flight refresh so we don't double-rotate the token.
      if (localStorage.getItem(REFRESH_KEY) && !this.refreshing) {
        this.refreshing = this.tryRefresh().finally(() => (this.refreshing = null));
      }
      if (this.refreshing && (await this.refreshing)) {
        const refresh = localStorage.getItem(REFRESH_KEY);
        if (refresh) {
          await this.raw('POST', '/api/v1/auth/logout', { refresh_token: refresh }, true);
        }
      }
    } catch {
      // Unreachable server / network error — still sign out locally.
    } finally {
      this.clearTokens();
    }
  }

  private async tryRefresh(): Promise<boolean> {
    const refresh = localStorage.getItem(REFRESH_KEY);
    if (!refresh) return false;
    try {
      const pair = await this.raw<TokenPair>('POST', '/api/v1/auth/refresh', {
        refresh_token: refresh
      });
      this.setTokens(pair);
      return true;
    } catch {
      return false;
    }
  }

  /** Force a token refresh and report success. Used by long-lived
   *  out-of-band connections (the EventSource sync stream) that carry a
   *  `?token=` asset token which can expire mid-movie — they refresh,
   *  then reconnect with the freshly-minted asset token. Shares the
   *  in-flight refresh so a concurrent API 401 doesn't double-refresh. */
  async refreshTokens(): Promise<boolean> {
    if (!this.refreshing) {
      this.refreshing = this.tryRefresh().finally(() => (this.refreshing = null));
    }
    return this.refreshing;
  }

  async get<T>(path: string): Promise<T> {
    return this.authed<T>('GET', path);
  }
  async post<T>(path: string, body?: unknown): Promise<T> {
    return this.authed<T>('POST', path, body);
  }
  async put<T>(path: string, body?: unknown): Promise<T> {
    return this.authed<T>('PUT', path, body);
  }
  async del<T>(path: string): Promise<T> {
    return this.authed<T>('DELETE', path);
  }

  mediaUrl(path: string): string {
    const origin = this.getOrigin();
    if (!origin) throw new Error('API origin not configured');
    return `${origin}${path}`;
  }

  /**
   * URL for an asset endpoint (artwork, media stream, subtitles)
   * that `<img>` / `<video>` / `<track>` can load directly.
   *
   * Browsers can't attach Authorization headers to those element
   * fetches, so we put the purpose=asset token in `?token=<paseto>` —
   * the server's RequiredAllowQueryToken middleware accepts asset (and
   * file-bound stream) tokens there but REJECTS the general access
   * token in a URL. The asset token can't be replayed as a Bearer, so
   * a leak (logs, Referer) doesn't grant general API access.
   *
   * Returns the path unchanged when no asset token is cached (caller
   * gets a clean URL that'll 401, surface that, and trigger
   * re-auth instead of silently swapping in a stale token).
   *
   * If the path already has a `?token=` (e.g. an HLS playlist with a
   * pre-baked transcode token, or a file-bound stream token), the
   * existing one wins — we don't overwrite.
   */
  assetUrl(path: string): string {
    const origin = this.getOrigin();
    if (!origin) throw new Error('API origin not configured');
    const url = `${origin}${path}`;
    const tok = this.getAssetToken();
    if (!tok) return url;
    if (/[?&]token=/.test(url)) return url;
    return url + (url.includes('?') ? '&' : '?') + 'token=' + encodeURIComponent(tok);
  }

  /**
   * Refresh-aware fetch for endpoints that return non-JSON bodies
   * (image blobs, raw text) which can't go through `get`/`post`. Adds the
   * Bearer header, applies the same request timeout, and on a 401
   * refreshes the token once and retries — so a token that expired mid-
   * session recovers instead of dead-ending on the bare access token.
   *
   * `path` may be an absolute URL or an origin-relative path; either way
   * the caller gets the raw Response back to consume as `.blob()` /
   * `.text()` / `.json()`. An absolute URL must be on the configured server
   * origin — the general access token is never sent to another host (e.g. a
   * third-party metadata/poster URL handed in by a future caller).
   */
  async authedFetch(path: string, retry = true): Promise<Response> {
    const origin = this.getOrigin();
    if (!origin) throw new Error('API origin not configured');
    const url = /^https?:\/\//i.test(path) ? path : `${origin}${path}`;
    let sameOrigin = false;
    try {
      sameOrigin = new URL(url).origin === new URL(origin).origin;
    } catch {
      sameOrigin = false;
    }
    if (!sameOrigin) throw new Error('authedFetch: refusing to send credentials off the server origin');
    const tok = this.getToken();
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
    let resp: Response;
    try {
      resp = await fetch(url, {
        headers: tok ? { Authorization: `Bearer ${tok}` } : {},
        signal: controller.signal
      });
    } catch (e) {
      if (controller.signal.aborted) throw new Error(`Request timed out: ${url}`);
      throw new Error(`Network error: ${(e as Error)?.message ?? 'request failed'}`);
    } finally {
      clearTimeout(timer);
    }
    if (resp.status === 401) {
      if (retry && (await this.refreshTokens())) return this.authedFetch(path, false);
      throw new Unauthorized();
    }
    return resp;
  }

  private async authed<T>(method: string, path: string, body?: unknown, retry = true): Promise<T> {
    try {
      return await this.raw<T>(method, path, body, true);
    } catch (e) {
      if (e instanceof Unauthorized && retry) {
        if (!this.refreshing) {
          this.refreshing = this.tryRefresh().finally(() => (this.refreshing = null));
        }
        const ok = await this.refreshing;
        if (ok) return this.authed<T>(method, path, body, false);
      }
      throw e;
    }
  }

  /** When fetch transparently followed a cleartext→TLS redirect, persist the
   *  https origin the server actually answered on. Same-host upgrades only —
   *  a redirect can never downgrade the scheme or move us to another host.
   *  Returns true when the stored origin changed. */
  private adoptTlsUpgrade(origin: string, resp: Response): boolean {
    if (!resp.redirected || !resp.url) return false;
    try {
      const asked = new URL(origin);
      const answered = new URL(resp.url);
      if (asked.protocol !== 'http:' || answered.protocol !== 'https:') return false;
      if (asked.hostname.toLowerCase() !== answered.hostname.toLowerCase()) return false;
      this.setOrigin(answered.origin);
      return true;
    } catch {
      return false;
    }
  }

  /** Unauthenticated probe that follows redirects to discover a same-host
   *  http→https upgrade. Carries no token and no body, so following the
   *  redirect can't leak credentials; adopts the https origin if found. */
  private async probeTlsUpgrade(origin: string): Promise<boolean> {
    if (!origin.startsWith('http://')) return false;
    try {
      const resp = await fetch(`${origin}/api/v1/system/capabilities`);
      return this.adoptTlsUpgrade(origin, resp);
    } catch {
      return false;
    }
  }

  private async raw<T>(
    method: string,
    path: string,
    body?: unknown,
    auth = false,
    upgraded = false
  ): Promise<T> {
    const origin = this.getOrigin();
    if (!origin) throw new Error('API origin not configured');

    const headers: Record<string, string> = {};
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    // Declarative capability profile on every request (docs/capability-profiles.md),
    // mirroring the web client. Drives server-side transcode target selection.
    headers['X-Client-Capabilities'] = clientCapabilitiesHeader();
    if (auth) {
      const tok = this.getToken();
      if (tok) headers['Authorization'] = `Bearer ${tok}`;
    }

    // Abort a hung request so a stalled TCP connection (TV dropped Wi-Fi,
    // server wedged) doesn't leave the caller waiting forever. A timeout
    // and a transport failure both surface as a clean Error the caller
    // can catch and retry, rather than a never-settling promise.
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
    let resp: Response;
    try {
      resp = await fetch(`${origin}${path}`, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
        // Every raw() call carries a credential (password, refresh token or
        // Bearer). Following a 307/308 would replay the body (and on the old
        // TV Chromium possibly the Authorization header) to wherever the
        // Location points, including another host. The JSON API never
        // redirects; a stale http origin is healed below via a probe.
        redirect: 'manual'
      });
    } catch (e) {
      if (controller.signal.aborted) {
        throw new Error(`Request timed out after ${REQUEST_TIMEOUT_MS / 1000}s: ${method} ${path}`);
      }
      // DNS / TLS / connection-refused — normalise to a readable message.
      throw new Error(`Network error: ${(e as Error)?.message ?? 'request failed'}`);
    } finally {
      clearTimeout(timer);
    }

    if (resp.type === 'opaqueredirect' || (resp.status >= 300 && resp.status < 400)) {
      // Stale http:// origin for a server that now answers on https (the
      // pairing-screen-with-no-code field report). Rediscover the upgrade
      // with an unauthenticated probe, adopt the same-host https origin and
      // replay once against it — credentials never ride a redirect.
      if (!upgraded && (await this.probeTlsUpgrade(origin))) {
        return this.raw<T>(method, path, body, auth, true);
      }
      throw new ApiError(resp.status, 'REDIRECT', 'server redirected the request; not following it with credentials');
    }

    if (resp.status === 401) throw new Unauthorized();

    if (!resp.ok) {
      let code = 'ERROR';
      let msg = resp.statusText;
      try {
        const j = await resp.json();
        if (j?.error) {
          code = j.error.code ?? code;
          msg = j.error.message ?? msg;
        }
      } catch {
        // non-JSON error body
      }
      throw new ApiError(resp.status, code, msg);
    }

    if (resp.status === 204) return undefined as T;
    const j = await resp.json();
    // API shape: { data: ... } for success; unwrap for caller convenience.
    return (j?.data ?? j) as T;
  }
}

export const api = new ApiClient();
