import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The capability header probes the TV's media stack; nothing to probe here.
vi.mock('./capabilities', () => ({ clientCapabilitiesHeader: () => '' }));

import { ApiClient, TIMED_OUT_MESSAGE, UNREACHABLE_MESSAGE, Unauthorized, type TokenPair } from './client';

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
    await expect(client.get('/api/v1/hub')).rejects.toThrow(UNREACHABLE_MESSAGE);
  });

  it('a request the server never answers times out with its own message', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn((_u: string, init: RequestInit) => new Promise((_r, reject) => {
      init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    })));
    const client = new ApiClient();
    client.setOrigin(ORIGIN);
    client.setTokens(pair(1));
    const req = expect(client.get('/api/v1/hub')).rejects.toThrow(TIMED_OUT_MESSAGE);
    await vi.advanceTimersByTimeAsync(60_000);
    await req;
  });
});
