// OnScreen API client for the webOS TV app.
//
// Differences from web/src/lib/api.ts:
// - Bearer tokens (not cookies) — TV runs cross-origin, can't share cookies.
// - Configurable origin — user enters their server URL on first launch.
// - No /login redirect — on 401, surface to caller; route decides.

const ORIGIN_KEY = 'onscreen.api_origin';
const TOKEN_KEY = 'onscreen.access_token';
const REFRESH_KEY = 'onscreen.refresh_token';
const USER_KEY = 'onscreen.user';

export interface UserMeta {
  user_id: string;
  username: string;
  is_admin: boolean;
}

export interface TokenPair {
  access_token: string;
  refresh_token: string;
  expires_at: string;
  user_id: string;
  username: string;
  is_admin: boolean;
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
    localStorage.removeItem(USER_KEY);
  }

  async login(username: string, password: string): Promise<TokenPair> {
    const pair = await this.raw<TokenPair>('POST', '/api/v1/auth/login', { username, password });
    this.setTokens(pair);
    return pair;
  }

  async logout(): Promise<void> {
    const refresh = localStorage.getItem(REFRESH_KEY);
    try {
      if (refresh) await this.raw('POST', '/api/v1/auth/logout', { refresh_token: refresh });
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

  private async raw<T>(method: string, path: string, body?: unknown, auth = false): Promise<T> {
    const origin = this.getOrigin();
    if (!origin) throw new Error('API origin not configured');

    const headers: Record<string, string> = {};
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (auth) {
      const tok = this.getToken();
      if (tok) headers['Authorization'] = `Bearer ${tok}`;
    }

    const resp = await fetch(`${origin}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body)
    });

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
