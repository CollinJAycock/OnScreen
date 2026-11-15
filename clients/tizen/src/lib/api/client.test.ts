import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The capability header probes the TV's media stack; nothing to probe here.
const caps = vi.hoisted(() => ({ clearCodecDemotions: vi.fn() }));
vi.mock('./capabilities', () => ({ clientCapabilitiesHeader: () => '', clearCodecDemotions: caps.clearCodecDemotions }));

import { ApiClient, RequestTimedOut, TIMED_OUT_MESSAGE, UNREACHABLE_MESSAGE, Unauthorized, type TokenPair } from './client';
import { setLocale } from '../i18n/index.svelte';
import { t } from '../i18n/index.svelte';

const ORIGIN = 'https://tv.example';

function memoryStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
}

const pair = (n: number): TokenPair => ({
  access_token: `access${n}`,
  refresh_token: `refresh${n}`,
  asset_token: `asset${n}`,
  expires_at: '2026-11-01T00:00:00Z',
  user_id: 'u1',
  username: 'collin',
  is_admin: false,
});

function respond(status: number, body: unknown = {}): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    type: 'basic',
    redirected: false,
    url: '',
    statusText: '',
    json: async () => body,
  } as unknown as Response;
}

describe('token refresh (ApiClient)', () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const refreshCalls = () =>
    fetchMock.mock.calls.filter((c) => String(c[0]).endsWith('/api/v1/auth/refresh')).length;

  it('stores the rotated tokens and when the asset token was minted', async () => {
    fetchMock.mockResolvedValue(respond(200, { data: pair(2) }));
    const before = Date.now();
    expect(await client.refreshTokensOutcome()).toBe('ok');
    expect(client.getToken()).toBe('access2');
    expect(client.getAssetToken()).toBe('asset2');
    expect(client.getAssetTokenIssuedAt()).toBeGreaterThanOrEqual(before);
  });

  it("a 401 is the server's verdict: the dead sign-in is dropped and the app told", async () => {
    fetchMock.mockResolvedValue(respond(401, { error: { code: 'UNAUTHORIZED' } }));
    const signedOut = vi.fn();
    client.onSignedOut(signedOut);
    expect(await client.refreshTokensOutcome()).toBe('rejected');
    expect(client.getToken()).toBeNull();
    expect(client.getAssetToken()).toBeNull();
    expect(client.getAssetTokenIssuedAt()).toBeNull();
    expect(signedOut).toHaveBeenCalledTimes(1);
    expect(signedOut).toHaveBeenCalledWith('rejected');
  });

  it("a 401 on sign-out's own rotation is part of signing out, not a rejection", async () => {
    fetchMock.mockResolvedValue(respond(401, { error: { code: 'UNAUTHORIZED' } }));
    const signedOut = vi.fn();
    client.onSignedOut(signedOut);
    await client.logout();
    expect(client.getToken()).toBeNull();
    expect(signedOut).toHaveBeenCalled();
    expect(signedOut).not.toHaveBeenCalledWith('rejected');
    // The flag doesn't outlive the sign-out: a later rejection says so.
    client.setTokens(pair(3));
    await client.refreshTokensOutcome();
    expect(signedOut).toHaveBeenLastCalledWith('rejected');
  });

  it('signing out (and so forgetting the server) drops the codec demotions, even offline', async () => {
    caps.clearCodecDemotions.mockClear();
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));
    await client.logout();
    expect(caps.clearCodecDemotions).toHaveBeenCalledTimes(1);
    client.setTokens(pair(2));
    await client.forgetServer();
    expect(caps.clearCodecDemotions).toHaveBeenCalledTimes(2);
  });

  it('a 503 (the server could not check the token), a 429 or no answer keeps the sign-in', async () => {
    const signedOut = vi.fn();
    client.onSignedOut(signedOut);
    fetchMock.mockResolvedValueOnce(respond(503, { error: { code: 'SERVICE_UNAVAILABLE' } }));
    expect(await client.refreshTokensOutcome()).toBe('failed');
    fetchMock.mockResolvedValueOnce(respond(429));
    expect(await client.refreshTokensOutcome()).toBe('failed');
    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
    expect(await client.refreshTokensOutcome()).toBe('failed');
    expect(await client.refreshTokens()).toBe(false);
    expect(client.getToken()).toBe('access1');
    expect(signedOut).not.toHaveBeenCalled();
  });

  it('keeps a sign-in made while a rejected refresh was out', async () => {
    let answer: (r: Response) => void = () => {};
    fetchMock.mockReturnValue(new Promise<Response>((r) => (answer = r)));
    const out = client.refreshTokensOutcome();
    await Promise.resolve();
    client.setTokens(pair(7));
    answer(respond(401));
    expect(await out).toBe('rejected');
    expect(client.getToken()).toBe('access7');
  });

  it('with no refresh token there is nothing to refresh with', async () => {
    client.clearTokens();
    expect(await client.refreshTokensOutcome()).toBe('rejected');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('runs one refresh at a time, shared by every caller (a second rotation of one token reads as reuse)', async () => {
    let answer: (r: Response) => void = () => {};
    fetchMock.mockImplementation((url: string) =>
      url.endsWith('/api/v1/auth/refresh')
        ? new Promise<Response>((r) => (answer = r))
        : Promise.resolve(respond(401)),
    );
    const a = client.refreshTokensOutcome();
    const b = client.refreshTokens();
    // An API call's 401 joins the refresh already out.
    const c = client.get('/api/v1/hub').catch((e) => e);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    // Let the GET's 401 reach its refresh.
    await new Promise((r) => setTimeout(r, 0));
    answer(respond(200, { data: pair(2) }));
    expect(await a).toBe('ok');
    expect(await b).toBe(true);
    // The retry with the fresh token 401s too: the caller gets Unauthorized.
    expect(await c).toBeInstanceOf(Unauthorized);
    expect(refreshCalls()).toBe(1);
  });
});

describe('request failures (ApiClient)', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("says what went wrong and what to do, not the browser's 'Failed to fetch'", async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch'); }));
    const client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
    await expect(client.get('/api/v1/hub')).rejects.toThrow(t(UNREACHABLE_MESSAGE));
  });

  it('a request the server never answers times out with its own message', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn((_u: string, init: RequestInit) => new Promise((_r, reject) => {
      init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    })));
    const client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
    const req = expect(client.get('/api/v1/hub')).rejects.toThrow(t(TIMED_OUT_MESSAGE));
    await vi.advanceTimersByTimeAsync(60_000);
    await req;
  });
});

// fetch() resolves on the headers; a body that then never finished used to
// be waited on for ever, and on /auth/refresh that wedged the whole app.
describe('a stalled body (ApiClient)', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  /** Headers in, the body never: it fails only when the request is aborted. */
  function stalledBody(init: RequestInit): Response {
    const never = () =>
      new Promise<never>((_r, reject) => {
        init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
    return {
      status: 200, ok: true, type: 'basic', redirected: false, url: '', statusText: '',
      headers: new Headers(), json: never, text: never, blob: never,
    } as unknown as Response;
  }

  function signedIn(): ApiClient {
    const client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
    return client;
  }

  it("times out a refresh whose answer never finishes, and frees the app's one refresh", async () => {
    const fetchMock = vi.fn(async (_u: string, init: RequestInit) => stalledBody(init));
    vi.stubGlobal('fetch', fetchMock);
    const client = signedIn();
    const out = client.refreshTokensOutcome();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(await out).toBe('failed');
    // No verdict on the token: the sign-in stays, and the next refresh goes out.
    expect(client.getToken()).toBe('access1');
    fetchMock.mockImplementation(async () => respond(200, { data: pair(2) }));
    expect(await client.refreshTokensOutcome()).toBe('ok');
  });

  it('authedFetch reads the body inside its timer, and hands it back read', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_u: string, init: RequestInit) => stalledBody(init)));
    const client = signedIn();
    const stalled = client.authedFetch('/api/v1/items/x/image').catch((e) => e);
    await vi.advanceTimersByTimeAsync(20_000);
    expect(await stalled).toBeInstanceOf(RequestTimedOut);

    vi.stubGlobal('fetch', vi.fn(async () => new Response('photo', { status: 200, headers: { 'Content-Type': 'image/jpeg' } })));
    const resp = await client.authedFetch('/api/v1/items/x/image');
    expect(resp.status).toBe(200);
    expect(resp.headers.get('Content-Type')).toBe('image/jpeg');
    expect(await resp.text()).toBe('photo');
  });
});

describe('a stalled GET (ApiClient)', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  // A Series X once sat 20 s on an artist page's item read; the page then
  // stayed on its error.
  it('sends a GET that never answered once more', async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn((_u: string, init: RequestInit) =>
      fetchMock.mock.calls.length === 1
        ? new Promise<Response>((_r, reject) => {
            init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
          })
        : Promise.resolve({
            status: 200, ok: true, type: 'basic', redirected: false, url: '', statusText: '',
            json: async () => ({ data: { id: 'artist' } }),
          } as unknown as Response),
    );
    vi.stubGlobal('fetch', fetchMock);
    const client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
    const req = client.get<{ id: string }>('/api/v1/items/artist');
    await vi.advanceTimersByTimeAsync(20_000);
    expect(await req).toEqual({ id: 'artist' });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('never repeats a POST', async () => {
    const fetchMock = vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    });
    vi.stubGlobal('fetch', fetchMock);
    const client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
    await expect(client.post('/api/v1/items/x/progress', {})).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

// The server translates the errors a person can see by Accept-Language
// (internal/i18n); the TV's own header is its menu language, not the app's.
describe('the app language (Accept-Language)', () => {
  afterEach(async () => {
    vi.unstubAllGlobals();
    await setLocale('en');
  });

  it("sends the language the app shows", async () => {
    vi.stubGlobal('localStorage', memoryStorage());
    const fetchMock = vi.fn().mockResolvedValue(respond(200, { data: {} }));
    vi.stubGlobal('fetch', fetchMock);
    const client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
    await client.get('/api/v1/hub');
    await setLocale('zh-Hans');
    await client.get('/api/v1/hub');
    const sent = fetchMock.mock.calls.map((c) => (c[1] as { headers: Record<string, string> }).headers['Accept-Language']);
    expect(sent).toEqual(['en', 'zh-Hans']);
  });
});
