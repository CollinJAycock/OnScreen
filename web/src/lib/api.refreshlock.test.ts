import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  ApiClient,
  getBearerToken,
  setApiBase,
  setBearerToken,
  authApi,
  REFRESH_UNAVAILABLE_CODE,
  trackAuthBootstrap,
  waitForAuthBootstrap,
  resetAuthBootstrap,
} from './api';

// Refresh tokens are one-shot: the server treats a superseded refresh token
// coming back as theft and deletes EVERY session of the user. In the browser
// the refresh token is an httpOnly cookie shared by all tabs, so two tabs that
// hit a 401 together must not both POST /auth/refresh with it. Each
// ApiClient below stands in for one tab; they share localStorage and the fake
// cookie jar the way tabs of one origin do.

const LEASE_KEY = 'onscreen_auth_refresh_lease';
const MARKER_KEY = 'onscreen_auth_refreshed_at';

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

function res(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, json: () => Promise.resolve(body) };
}

const UNAUTHORIZED = { error: { code: 'UNAUTHORIZED', message: 'unauthorized', request_id: 'r' } };

/** A fake OnScreen server plus the browser's cookie jar. Mirrors the auth
 *  rules that make reuse possible: a body refresh_token wins over the cookie
 *  (auth.go Refresh), an Authorization header wins over the access cookie
 *  (middleware extractClaims), refresh tokens rotate on every use, and
 *  presenting a superseded one is reuse — every session dies. Logout deletes
 *  the session only under its current refresh token (a superseded one finds
 *  nothing) and clears the cookies either way.
 *  Options are read per request, so a test can flip them mid-way (the
 *  network coming back). rotateOnArrival: the server rotates the moment the
 *  refresh arrives, while its response — and the Set-Cookie with it — only
 *  lands after refreshDelayMs. refreshBadBody: a 2xx (rotated, cookies set)
 *  whose body can't be read. hangRefreshes / hangLogouts: that many POSTs
 *  never answer (and never reach the server's logic); only the caller's
 *  abort ends one, rejecting the way fetch does. */
function fakeServer(
  opts: {
    refreshDelayMs?: number;
    refreshStatus?: number;
    refreshNetworkError?: boolean;
    hangRefreshes?: number;
    rotateOnArrival?: boolean;
    refreshBadBody?: boolean;
    logoutDelayMs?: number;
    hangLogouts?: number;
  } = {},
) {
  let hangs = opts.hangRefreshes ?? 0;
  let logoutHangs = opts.hangLogouts ?? 0;
  const hangUntilAborted = (signal?: AbortSignal | null) =>
    new Promise<never>((_, reject) => {
      signal?.addEventListener(
        'abort',
        () => reject(new DOMException('The operation was aborted.', 'AbortError')),
        { once: true },
      );
    });
  const srv = {
    jarAccess: 'a0', // expired access cookie every tab sends
    jarRefresh: 'r1',
    accessCurrent: 'a1',
    refreshCurrent: 'r1',
    gen: 1,
    refreshPosts: [] as Array<{ sent: string; via: 'body' | 'cookie' }>,
    logoutPosts: [] as string[],
    reuse: 0,
  };
  /** Rotate server-side; the returned setCookies lands the pair in the jar. */
  const rotate = () => {
    srv.gen++;
    const access = (srv.accessCurrent = `a${srv.gen}`);
    const refresh = (srv.refreshCurrent = `r${srv.gen}`);
    return {
      body: {
        data: {
          access_token: access, refresh_token: refresh, asset_token: '',
          expires_at: '', user_id: 'u1', username: 'alice', is_admin: false,
        },
      },
      setCookies: () => {
        srv.jarAccess = access;
        srv.jarRefresh = refresh;
      },
    };
  };
  const issue = () => {
    const { body, setCookies } = rotate();
    setCookies();
    return body;
  };
  const fetch = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.endsWith('/auth/refresh')) {
      const bodyToken = init?.body ? (JSON.parse(init.body as string).refresh_token as string) : undefined;
      // The cookie is attached when the request is SENT.
      const sent = bodyToken ?? srv.jarRefresh;
      srv.refreshPosts.push({ sent, via: bodyToken ? 'body' : 'cookie' });
      if (opts.refreshNetworkError) throw new TypeError('Failed to fetch');
      if (hangs > 0) {
        hangs--;
        await hangUntilAborted(init?.signal);
      }
      if (opts.rotateOnArrival && !opts.refreshStatus && sent && sent === srv.refreshCurrent) {
        const { body, setCookies } = rotate();
        await sleep(opts.refreshDelayMs ?? 20);
        setCookies();
        return res(200, body);
      }
      await sleep(opts.refreshDelayMs ?? 20);
      if (opts.refreshStatus) return res(opts.refreshStatus, UNAUTHORIZED);
      // No cookie at all (signed out): a plain 401, nothing to detect.
      if (!sent) return res(401, UNAUTHORIZED);
      if (sent !== srv.refreshCurrent) {
        srv.reuse++;
        srv.refreshCurrent = srv.accessCurrent = 'revoked';
        return res(401, UNAUTHORIZED);
      }
      // Set-Cookie lands in the jar before the fetch promise resolves.
      const body = issue();
      if (opts.refreshBadBody) {
        return { ok: true, status: 200, json: () => Promise.reject(new SyntaxError('Unexpected end of JSON input')) };
      }
      return res(200, body);
    }
    if (url.endsWith('/auth/login')) return res(200, issue());
    if (url.endsWith('/auth/logout')) {
      const bodyToken = init?.body ? (JSON.parse(init.body as string).refresh_token as string) : undefined;
      const sent = bodyToken ?? srv.jarRefresh;
      srv.logoutPosts.push(sent);
      if (logoutHangs > 0) {
        logoutHangs--;
        await hangUntilAborted(init?.signal);
      }
      await sleep(opts.logoutDelayMs ?? 0);
      if (sent && sent === srv.refreshCurrent) srv.refreshCurrent = srv.accessCurrent = 'logged-out';
      srv.jarAccess = srv.jarRefresh = '';
      return res(204, null);
    }
    const auth = (init?.headers as Record<string, string> | undefined)?.Authorization;
    const presented = auth ? auth.replace(/^Bearer /, '') : srv.jarAccess;
    return presented === srv.accessCurrent ? res(200, { data: { ok: true } }) : res(401, UNAUTHORIZED);
  });
  vi.stubGlobal('fetch', fetch);
  return { srv, fetch };
}

/** Minimal navigator.locks: one exclusive FIFO lock, honouring
 *  options.signal for requests still in the queue — enough of the Web Locks
 *  contract for the tabs to share it like an origin shares the real one. */
function fakeLocks() {
  let held = false;
  let queue: Array<() => void> = [];
  const mgr = {
    requests: 0,
    isHeld: () => held,
    async request<T>(
      name: string,
      a: { signal?: AbortSignal } | ((lock: unknown) => T),
      b?: (lock: unknown) => T,
    ): Promise<Awaited<T>> {
      const cb = (typeof a === 'function' ? a : b)!;
      const signal = typeof a === 'function' ? undefined : a.signal;
      mgr.requests++;
      if (held) {
        await new Promise<void>((resolve, reject) => {
          const grant = () => {
            signal?.removeEventListener('abort', onAbort);
            resolve();
          };
          const onAbort = () => {
            queue = queue.filter((g) => g !== grant);
            reject(new DOMException('aborted', 'AbortError'));
          };
          signal?.addEventListener('abort', onAbort, { once: true });
          queue.push(grant);
        });
      }
      held = true;
      try {
        return await cb({ name, mode: 'exclusive' });
      } finally {
        const next = queue.shift();
        if (next) next();
        else held = false;
      }
    },
  };
  Object.defineProperty(navigator, 'locks', { value: mgr, configurable: true });
  return mgr;
}

/** tryRefresh is private; the error-path tests call it directly to pin its
 *  contract (throw on network error, false on rejection). */
const tryRefresh = (c: ApiClient) => (c as unknown as { tryRefresh(): Promise<boolean> }).tryRefresh();

/** Observe a promise without awaiting it, so fake-timer tests can check it is
 *  STILL pending at some point. Handles the rejection up front, so one that
 *  lands mid-advance isn't reported as unhandled. */
function track<T>(p: Promise<T>) {
  const t: { state: 'pending' | 'resolved' | 'rejected'; value?: T; error?: unknown } = { state: 'pending' };
  p.then(
    (v) => { t.state = 'resolved'; t.value = v; },
    (e) => { t.state = 'rejected'; t.error = e; },
  );
  return t;
}

/** Put hooks on the lease key's reads and writes by swapping in a
 *  localStorage that delegates to the real one (unstubbed in afterEach);
 *  every other key passes through untouched. */
function onLeaseStorage(hooks: {
  get?: (real: () => string | null) => string | null;
  set?: (value: string, real: () => void) => void;
}) {
  const store = localStorage;
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => {
      const real = () => store.getItem(key);
      return key === LEASE_KEY && hooks.get ? hooks.get(real) : real();
    },
    setItem: (key: string, value: string) => {
      const real = () => store.setItem(key, value);
      if (key === LEASE_KEY && hooks.set) hooks.set(value, real);
      else real();
    },
    removeItem: (key: string) => store.removeItem(key),
    clear: () => store.clear(),
    key: (i: number) => store.key(i),
    get length() { return store.length; },
  });
}

beforeEach(() => {
  localStorage.clear();
  setApiBase('/api/v1');
  setBearerToken(null, null, null);
});

afterEach(() => {
  delete (navigator as unknown as { locks?: unknown }).locks;
  delete (window as Window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
  setApiBase('/api/v1');
  setBearerToken(null, null, null);
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('fake server sanity', () => {
  it('two uncoordinated refreshes with one cookie are reuse', async () => {
    const { srv, fetch } = fakeServer();
    const post = () => fetch('/api/v1/auth/refresh', { method: 'POST' });
    await Promise.all([post(), post()]);
    expect(srv.reuse).toBe(1);
  });

  it('an uncoordinated logout that overtakes a refresh revokes nothing, and the refresh signs the browser back in', async () => {
    const { srv, fetch } = fakeServer({ rotateOnArrival: true, refreshDelayMs: 50 });
    const refresh = fetch('/api/v1/auth/refresh', { method: 'POST' });
    await sleep(10);
    await fetch('/api/v1/auth/logout', { method: 'POST' });
    await refresh;
    expect(srv.logoutPosts).toEqual(['r1']);
    expect(srv.jarRefresh).toBe('r2');
    expect(srv.refreshCurrent).toBe('r2');
  });
});

describe.each([
  ['Web Locks', () => { fakeLocks(); }],
  ['localStorage lease fallback (no navigator.locks)', () => {}],
])('cross-tab refresh — %s', (_name, setup) => {
  beforeEach(() => setup());

  it('two tabs hitting 401 together cause ONE /auth/refresh and both retries succeed', async () => {
    const { srv } = fakeServer();
    const tabA = new ApiClient();
    const tabB = new ApiClient();

    const [a, b] = await Promise.all([tabA.get('/items/1'), tabB.get('/items/2')]);

    expect(a).toEqual({ ok: true });
    expect(b).toEqual({ ok: true });
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('a tab that queued behind a failed refresh tries on its own (no marker change)', async () => {
    const { srv } = fakeServer({ refreshStatus: 401 });
    const tabA = new ApiClient();
    const tabB = new ApiClient();

    const [a, b] = await Promise.all([tryRefresh(tabA), tryRefresh(tabB)]);

    expect([a, b]).toEqual([false, false]);
    expect(srv.refreshPosts).toHaveLength(2);
    expect(localStorage.getItem(MARKER_KEY)).toBeNull();
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('401 from /auth/refresh still resolves false (caller tears down) and frees the lock', async () => {
    const { srv } = fakeServer({ refreshStatus: 401 });
    const tab = new ApiClient();

    await expect(tryRefresh(tab)).resolves.toBe(false);
    // Nothing left held: an immediate second refresh isn't blocked.
    await expect(tryRefresh(tab)).resolves.toBe(false);

    expect(srv.refreshPosts).toHaveLength(2);
    expect(localStorage.getItem(MARKER_KEY)).toBeNull();
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
    expect((navigator as unknown as { locks?: { isHeld(): boolean } | null }).locks?.isHeld() ?? false).toBe(false);
  });

  it('a network error still throws (caller keeps tokens) and frees the lock', async () => {
    const { srv } = fakeServer({ refreshNetworkError: true });
    const tab = new ApiClient();

    await expect(tryRefresh(tab)).rejects.toThrow('Failed to fetch');
    await expect(tryRefresh(tab)).rejects.toThrow('Failed to fetch');

    expect(srv.refreshPosts).toHaveLength(2);
    expect(localStorage.getItem(MARKER_KEY)).toBeNull();
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
    expect((navigator as unknown as { locks?: { isHeld(): boolean } | null }).locks?.isHeld() ?? false).toBe(false);
  });

  it('a holder whose POST hangs is aborted after 20 s, freeing the lock for the next tab', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer({ hangRefreshes: 1 });
    const a = track(tryRefresh(new ApiClient()));
    const b = track(tryRefresh(new ApiClient()));

    await vi.advanceTimersByTimeAsync(19_000);
    expect([a.state, b.state]).toEqual(['pending', 'pending']);
    expect(srv.refreshPosts).toHaveLength(1);

    await vi.advanceTimersByTimeAsync(2_000);
    // A fails like a network error (the caller's usual catch handles it)...
    expect(a.state).toBe('rejected');
    expect((a.error as DOMException).name).toBe('AbortError');
    // ...and B, next in line, refreshes. A's POST never reached the server,
    // so the cookie B sends is still the current one.
    expect(b).toMatchObject({ state: 'resolved', value: true });
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }, { sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
    expect((navigator as unknown as { locks?: { isHeld(): boolean } | null }).locks?.isHeld() ?? false).toBe(false);
  });

  it.each([401, 403])('a refresh the server rejects (%i) tears the tab down to /login as before', async (status) => {
    fakeServer({ refreshStatus: status });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    await tab.get('/items/1');

    expect(loc.href).toBe('/login');
    expect(tab.getUser()).toBeNull();
  });

  // A refresh that got no verdict on the tokens must not sign the tab out:
  // the teardown clears the onscreen_user every tab of the origin shares.
  it('a refresh that throws fails just the request: user kept, no redirect, shared onscreen_user untouched', async () => {
    const opts = { refreshNetworkError: true };
    const { srv } = fakeServer(opts);
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });
    const shared = localStorage.getItem('onscreen_user');

    await expect(tab.get('/items/1')).rejects.toMatchObject({
      name: 'ApiRequestError',
      status: 401,
      code: REFRESH_UNAVAILABLE_CODE,
    });

    expect(loc.href).toBe('/');
    expect(localStorage.getItem('onscreen_user')).toBe(shared);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
    expect((navigator as unknown as { locks?: { isHeld(): boolean } | null }).locks?.isHeld() ?? false).toBe(false);
    // Nothing torn down, nothing left held: once the network is back the
    // same tab simply carries on.
    opts.refreshNetworkError = false;
    await expect(tab.get('/items/1')).resolves.toEqual({ ok: true });
    expect(srv.reuse).toBe(0);
  });

  it.each([500, 502, 503, 429])('a %i from /auth/refresh is transient: the request fails, the session stays', async (status) => {
    fakeServer({ refreshStatus: status });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    await expect(tryRefresh(tab)).rejects.toMatchObject({ status, code: REFRESH_UNAVAILABLE_CODE });
    await expect(tab.get('/items/1')).rejects.toMatchObject({ status: 401, code: REFRESH_UNAVAILABLE_CODE });

    expect(loc.href).toBe('/');
    expect(tab.getUser()).toEqual({ user_id: 'u1', username: 'alice', is_admin: false });
    expect(localStorage.getItem(MARKER_KEY)).toBeNull();
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
    expect((navigator as unknown as { locks?: { isHeld(): boolean } | null }).locks?.isHeld() ?? false).toBe(false);
  });

  it('a 2xx whose body can\'t be read is transient in cookie mode: the jar already holds the new pair', async () => {
    const { srv } = fakeServer({ refreshBadBody: true });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    await expect(tab.get('/items/1')).rejects.toMatchObject({ code: REFRESH_UNAVAILABLE_CODE });
    expect(loc.href).toBe('/');
    expect(tab.getUser()).not.toBeNull();

    // The rotation's Set-Cookie landed, so the next request just works.
    await expect(tab.get('/items/1')).resolves.toEqual({ ok: true });
    expect(srv.refreshPosts).toHaveLength(1);
    expect(srv.reuse).toBe(0);
  });

  it('a request whose refresh POST hangs fails alone after 20 s, still signed in', async () => {
    vi.useFakeTimers();
    fakeServer({ hangRefreshes: 1 });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    const t = track(tab.get('/items/1'));
    await vi.advanceTimersByTimeAsync(19_000);
    expect(t.state).toBe('pending');
    await vi.advanceTimersByTimeAsync(2_000);

    expect(t.state).toBe('rejected');
    expect(t.error).toMatchObject({ status: 401, code: REFRESH_UNAVAILABLE_CODE });
    expect(loc.href).toBe('/');
    expect(tab.getUser()).not.toBeNull();
  });
});

// A wait that runs out means the holder may still be mid-POST with the very
// cookie the waiter would send — so the waiter never POSTs on its own.
describe('bounded waits never end in a POST', () => {
  it('Web Locks: a waiter behind a holder that never lets go gives up after 35 s without posting', async () => {
    vi.useFakeTimers();
    const locks = fakeLocks();
    const { srv } = fakeServer();
    // Another tab holds the lock and never lets go.
    void locks.request('onscreen-auth-refresh', () => new Promise(() => {}));

    const t = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(34_000);
    expect(t.state).toBe('pending');

    await vi.advanceTimersByTimeAsync(2_000);
    expect(t.state).toBe('rejected');
    expect(String(t.error)).toMatch(/timed out waiting for another tab/);
    expect(srv.refreshPosts).toHaveLength(0);
  });

  it('Web Locks: ...so the request that hit 401 fails alone — no redirect, the user stays signed in', async () => {
    vi.useFakeTimers();
    const locks = fakeLocks();
    const { srv } = fakeServer();
    void locks.request('onscreen-auth-refresh', () => new Promise(() => {}));
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    const t = track(tab.get('/items/1'));
    await vi.advanceTimersByTimeAsync(36_000);

    expect(t.state).toBe('rejected');
    expect(t.error).toMatchObject({ name: 'ApiRequestError', status: 401, code: REFRESH_UNAVAILABLE_CODE });
    expect(loc.href).toBe('/');
    expect(tab.getUser()).toEqual({ user_id: 'u1', username: 'alice', is_admin: false });
    expect(srv.refreshPosts).toHaveLength(0);
    expect(srv.reuse).toBe(0);
  });

  it('Web Locks: a sign-out whose wait runs out posts anyway (a logout cannot be reuse)', async () => {
    vi.useFakeTimers();
    const locks = fakeLocks();
    const { srv } = fakeServer();
    void locks.request('onscreen-auth-refresh', () => new Promise(() => {}));

    const t = track(new ApiClient().revokeSession());
    await vi.advanceTimersByTimeAsync(34_000);
    expect(srv.logoutPosts).toHaveLength(0);
    expect(localStorage.getItem(MARKER_KEY)).toBeNull();

    await vi.advanceTimersByTimeAsync(2_000);
    expect(t.state).toBe('resolved');
    expect(srv.logoutPosts).toEqual(['r1']);
    expect(srv.refreshPosts).toHaveLength(0);
    // Marked like a sign-out that got its turn: a tab that queues behind it
    // must not mistake an earlier refresh for a jar that still holds cookies.
    expect(localStorage.getItem(MARKER_KEY)).toMatch(/^logout:/);
  });

  it('Web Locks: a waiter that times out still takes a refresh that landed meanwhile', async () => {
    vi.useFakeTimers();
    const locks = fakeLocks();
    const { srv } = fakeServer();
    void locks.request('onscreen-auth-refresh', () => new Promise(() => {}));

    const p = new ApiClient().get('/items/1');
    await vi.advanceTimersByTimeAsync(10_000);
    // A refresh lands while the lock stays held: fresh cookies in the jar,
    // marker bumped.
    srv.accessCurrent = srv.jarAccess = 'a9';
    srv.refreshCurrent = srv.jarRefresh = 'r9';
    localStorage.setItem(MARKER_KEY, 'other-tab-refresh');

    await vi.advanceTimersByTimeAsync(26_000);
    await expect(p).resolves.toEqual({ ok: true });
    expect(srv.refreshPosts).toHaveLength(0);
  });

  it('lease fallback: a waiter behind a holder that keeps its lease live gives up after 35 s without posting', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer();
    const renew = () =>
      localStorage.setItem(LEASE_KEY, JSON.stringify({ owner: 'other-tab', exp: Date.now() + 30_000 }));
    renew();
    const renewing = setInterval(renew, 10_000);

    try {
      const t = track(tryRefresh(new ApiClient()));
      await vi.advanceTimersByTimeAsync(34_000);
      expect(t.state).toBe('pending');

      await vi.advanceTimersByTimeAsync(2_000);
      expect(t.state).toBe('rejected');
      expect(String(t.error)).toMatch(/timed out waiting for another tab/);
      expect(srv.refreshPosts).toHaveLength(0);
      // Not ours to remove.
      expect(JSON.parse(localStorage.getItem(LEASE_KEY)!).owner).toBe('other-tab');
    } finally {
      clearInterval(renewing);
    }
  });

  it('lease fallback: a lease claimed impossibly far ahead (clock set back, corruption) counts as lapsed', async () => {
    const { srv } = fakeServer();
    // No real claim expires more than REFRESH_LEASE_MS (30 s) from now; the
    // slack for a clock set back is another 30 s.
    localStorage.setItem(LEASE_KEY, JSON.stringify({ owner: 'other-tab', exp: Date.now() + 10 * 60_000 }));

    await expect(new ApiClient().get('/items/1')).resolves.toEqual({ ok: true });

    // Overwritten and refreshed at once, not waited on for ten minutes.
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('lease fallback: a live holder\'s lease still holds after the clock steps back mid-POST', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer({ refreshDelayMs: 5_000 });
    const a = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(1_000);
    expect(srv.refreshPosts).toHaveLength(1);

    // An NTP correction sets the clock back 10 s while A's POST is in flight:
    // A's lease now looks ~39 s ahead, past what a fresh claim can be.
    vi.setSystemTime(Date.now() - 10_000);
    const b = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(10_000);

    // B waited for A instead of taking its lease for bogus and posting the
    // same cookie; A's refresh landed and B took it.
    expect(a).toMatchObject({ state: 'resolved', value: true });
    expect(b).toMatchObject({ state: 'resolved', value: true });
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('lease fallback: a bogus lease inside the slack wedges refreshes for at most its life, never into a POST', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer();
    // Left under a clock since set back ~25 s (or corrupt), yet within
    // REFRESH_LEASE_MS + slack of now: indistinguishable from a live holder.
    localStorage.setItem(LEASE_KEY, JSON.stringify({ owner: 'gone-tab', exp: Date.now() + 55_000 }));

    const first = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(36_000);
    expect(first.state).toBe('rejected');
    expect(String(first.error)).toMatch(/timed out waiting for another tab/);
    expect(srv.refreshPosts).toHaveLength(0);

    // Once it lapses, refreshes go through again.
    await vi.advanceTimersByTimeAsync(20_000);
    const next = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(1_000);
    expect(next).toMatchObject({ state: 'resolved', value: true });
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
  });

  it('lease fallback: a tab that died holding the lease blocks only until it expires', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer();
    localStorage.setItem(LEASE_KEY, JSON.stringify({ owner: 'dead-tab', exp: Date.now() + 30_000 }));

    const p = new ApiClient().get('/items/1');
    await vi.advanceTimersByTimeAsync(29_000);
    expect(srv.refreshPosts).toHaveLength(0);

    await vi.advanceTimersByTimeAsync(2_000);
    await expect(p).resolves.toEqual({ ok: true });
    expect(srv.refreshPosts).toHaveLength(1);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('lease fallback: waiting ends as soon as another tab publishes a refresh', async () => {
    const { srv } = fakeServer();
    localStorage.setItem(LEASE_KEY, JSON.stringify({ owner: 'other-tab', exp: Date.now() + 30_000 }));

    const p = new ApiClient().get('/items/1');
    await sleep(150);
    // The other tab finishes: new cookies in the jar, marker bumped, lease freed.
    srv.accessCurrent = srv.jarAccess = 'a9';
    srv.refreshCurrent = srv.jarRefresh = 'r9';
    localStorage.setItem(MARKER_KEY, 'other-tab-refresh');
    localStorage.removeItem(LEASE_KEY);

    await expect(p).resolves.toEqual({ ok: true });
    expect(srv.refreshPosts).toHaveLength(0);
  });
});

// localStorage has no compare-and-set, and a write in one tab reaches another
// tab's view of it with a lag. These pin what the claim read-back does about it.
describe('lease fallback: claim races', () => {
  it('two tabs that both claim a free lease before either reads back → exactly ONE posts', async () => {
    const { srv } = fakeServer();
    // Each tab's first look at the lease still sees it free (the other tab's
    // claim hasn't reached it yet), so both write a claim.
    const events: string[] = [];
    let staleLooks = 2;
    onLeaseStorage({
      get: (real) => {
        events.push('look');
        if (staleLooks > 0) {
          staleLooks--;
          return null;
        }
        return real();
      },
      set: (value, real) => {
        events.push(`claim:${(JSON.parse(value) as { owner: string }).owner}`);
        real();
      },
    });

    const [a, b] = await Promise.all([tryRefresh(new ApiClient()), tryRefresh(new ApiClient())]);

    // The race really happened: both tabs claimed before either read back.
    const [look1, claim1, look2, claim2] = events;
    expect([look1, look2]).toEqual(['look', 'look']);
    expect(claim1).toMatch(/^claim:/);
    expect(claim2).toMatch(/^claim:/);
    expect(claim1).not.toBe(claim2);
    // The read-back let only the surviving claim through; the other tab went
    // back to waiting, saw that refresh land and skipped its own.
    expect([a, b]).toEqual([true, true]);
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('a claim that aged while its tab was throttled is renewed before the POST', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer({ refreshDelayMs: 5_000 });
    // Tab A claims the free lease, then its 50 ms settle stretches to 29 s,
    // as a background tab's timers can.
    let stretched = false;
    onLeaseStorage({
      set: (_value, real) => {
        real();
        if (!stretched) {
          stretched = true;
          vi.setSystemTime(Date.now() + 29_000);
        }
      },
    });

    const a = track(tryRefresh(new ApiClient()));
    // Tab B shows up after A's first claim would have lapsed. Had A posted on
    // that claim, B would find the lease free and POST the same cookie
    // while A's POST was still in flight.
    await vi.advanceTimersByTimeAsync(1_500);
    const b = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(10_000);

    expect(stretched).toBe(true);
    expect(a).toMatchObject({ state: 'resolved', value: true });
    expect(b).toMatchObject({ state: 'resolved', value: true });
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });
});

// A claim of ours still standing when the wait ends without the lease would
// hold off every other tab until it lapsed.
describe('lease fallback: a claim that did not take is withdrawn on every exit', () => {
  it('when another tab\'s refresh lands while the claim aged', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer();
    let first = true;
    onLeaseStorage({
      set: (_value, real) => {
        real();
        if (first) {
          first = false;
          // The settle stretches until the claim is past use, and meanwhile
          // another tab's refresh lands.
          vi.setSystemTime(Date.now() + 29_000);
          localStorage.setItem(MARKER_KEY, 'other-tab-refresh');
        }
      },
    });

    const t = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(1_000);

    expect(t).toMatchObject({ state: 'resolved', value: true });
    expect(srv.refreshPosts).toHaveLength(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('when the wait runs out', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer();
    // Every read-back finds our claim nearly spent, round after round.
    onLeaseStorage({
      get: (real) => {
        const raw = real();
        return raw ? JSON.stringify({ ...JSON.parse(raw), exp: Date.now() + 1_000 }) : raw;
      },
    });

    const t = track(tryRefresh(new ApiClient()));
    await vi.advanceTimersByTimeAsync(36_000);

    expect(t.state).toBe('rejected');
    expect(String(t.error)).toMatch(/timed out waiting for another tab/);
    expect(srv.refreshPosts).toHaveLength(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });
});

describe('refreshSession (SSO callback bootstrap)', () => {
  it('queues with another tab\'s 401 refresh → ONE /auth/refresh, and getUser() is filled', async () => {
    fakeLocks();
    const { srv } = fakeServer();
    const tabA = new ApiClient();
    const tabB = new ApiClient();

    const [a, b] = await Promise.all([tabA.get('/items/1'), tabB.refreshSession()]);

    expect(a).toEqual({ ok: true });
    expect(b).toBe(true);
    expect(srv.refreshPosts).toHaveLength(1);
    expect(srv.reuse).toBe(0);
    expect(tabB.getUser()).toEqual({ user_id: 'u1', username: 'alice', is_admin: false });
  });

  it('joins a refresh already running in the same tab', async () => {
    const locks = fakeLocks();
    const { srv } = fakeServer();
    const tab = new ApiClient();

    const [a, b] = await Promise.all([tab.get('/items/1'), tab.refreshSession()]);

    expect(a).toEqual({ ok: true });
    expect(b).toBe(true);
    expect(locks.requests).toBe(1);
    expect(srv.refreshPosts).toHaveLength(1);
  });

  it('resolves false on a rejected refresh and leaves teardown to the caller', async () => {
    fakeLocks();
    fakeServer({ refreshStatus: 401 });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    await expect(tab.refreshSession()).resolves.toBe(false);

    expect(loc.href).toBe('/');
    expect(tab.getUser()).not.toBeNull();
  });

  it('throws on a refresh that got no verdict (5xx), and leaves the user alone', async () => {
    fakeLocks();
    fakeServer({ refreshStatus: 503 });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tab = new ApiClient();
    tab.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    await expect(tab.refreshSession()).rejects.toMatchObject({ status: 503, code: REFRESH_UNAVAILABLE_CODE });

    expect(loc.href).toBe('/');
    expect(tab.getUser()).not.toBeNull();
  });
});

// The home page's auth gate on an SSO landing waits on the layout's
// bootstrap refresh (trackAuthBootstrap), which can queue behind another
// tab's refresh far longer than the fixed 3 s it used to allow.
describe('waitForAuthBootstrap', () => {
  afterEach(() => resetAuthBootstrap());

  it('waits on the registered bootstrap past the old 3 s window and returns once it settles', async () => {
    vi.useFakeTimers();
    let finish!: (ok: boolean) => void;
    trackAuthBootstrap(new Promise<boolean>((resolve) => { finish = resolve; }));

    const w = track(waitForAuthBootstrap(() => false));
    await vi.advanceTimersByTimeAsync(30_000);
    expect(w.state).toBe('pending');

    finish(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(w.state).toBe('resolved');
  });

  it('a bootstrap that fails ends the wait too', async () => {
    vi.useFakeTimers();
    let fail!: (e: unknown) => void;
    const refresh = trackAuthBootstrap(new Promise<boolean>((_, reject) => { fail = reject; }));
    refresh.catch(() => {}); // the layout's own catch

    const w = track(waitForAuthBootstrap(() => false));
    fail(new Error('auth refresh: timed out waiting for another tab'));
    await vi.advanceTimersByTimeAsync(0);
    expect(w.state).toBe('resolved');
  });

  it('is bounded: a bootstrap that never settles is given up on after a minute', async () => {
    vi.useFakeTimers();
    trackAuthBootstrap(new Promise(() => {}));

    const w = track(waitForAuthBootstrap(() => false));
    await vi.advanceTimersByTimeAsync(59_000);
    expect(w.state).toBe('pending');
    await vi.advanceTimersByTimeAsync(2_000);
    expect(w.state).toBe('resolved');
  });

  it('with no bootstrap registered keeps the old 3 s window, ending early once ready() holds', async () => {
    vi.useFakeTimers();
    const idle = track(waitForAuthBootstrap(() => false));
    await vi.advanceTimersByTimeAsync(2_900);
    expect(idle.state).toBe('pending');
    await vi.advanceTimersByTimeAsync(200);
    expect(idle.state).toBe('resolved');

    let ready = false;
    const early = track(waitForAuthBootstrap(() => ready));
    await vi.advanceTimersByTimeAsync(500);
    expect(early.state).toBe('pending');
    ready = true;
    await vi.advanceTimersByTimeAsync(100);
    expect(early.state).toBe('resolved');
  });

  it('moves on to a bootstrap that registers during that window', async () => {
    vi.useFakeTimers();
    const w = track(waitForAuthBootstrap(() => false));
    await vi.advanceTimersByTimeAsync(500);
    let finish!: (ok: boolean) => void;
    trackAuthBootstrap(new Promise<boolean>((resolve) => { finish = resolve; }));

    await vi.advanceTimersByTimeAsync(10_000);
    expect(w.state).toBe('pending');
    finish(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(w.state).toBe('resolved');
  });

  it('end to end: resolves once refreshSession has stored the user', async () => {
    fakeLocks();
    const { srv } = fakeServer({ refreshDelayMs: 100 });
    const otherTab = new ApiClient();
    const landing = new ApiClient();

    const other = otherTab.get('/items/1');
    trackAuthBootstrap(landing.refreshSession());
    await waitForAuthBootstrap(() => false);

    expect(landing.getUser()).toEqual({ user_id: 'u1', username: 'alice', is_admin: false });
    await expect(other).resolves.toEqual({ ok: true });
    expect(srv.refreshPosts).toHaveLength(1);
    expect(srv.reuse).toBe(0);
  });
});

describe('same-origin browser keeps no in-memory token copy', () => {
  // The stale-copy variant of reuse: a tab that logged in held the pair in
  // memory and posted THAT refresh token in the body (which the server
  // prefers over the cookie) even after another tab had rotated it.
  it('after another tab rotates, a tab that logged in refreshes with the jar cookie, not a stale copy', async () => {
    const { srv } = fakeServer();
    await authApi.login('alice', 'pw');
    expect(getBearerToken()).toBeNull();

    // Another tab (its own module state in a real browser) rotates the pair,
    // and later that access token expires too.
    const rotated = 'r-other-tab';
    srv.refreshCurrent = srv.jarRefresh = rotated;
    srv.accessCurrent = 'a-other-tab';
    srv.jarAccess = 'expired';

    await expect(new ApiClient().get('/items/1')).resolves.toEqual({ ok: true });
    expect(srv.refreshPosts).toEqual([{ sent: rotated, via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
  });
});

describe('native (bearer) mode', () => {
  beforeEach(() => {
    setApiBase('http://server:7070/api/v1');
    (window as Window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = {};
    setBearerToken('a-native', 'r-native', null);
  });

  it('refreshes with the in-memory token in the body, outside the cross-tab lock', async () => {
    const locks = fakeLocks();
    const { fetch } = fakeServer({ refreshStatus: 401 });

    await tryRefresh(new ApiClient());

    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('http://server:7070/api/v1/auth/refresh');
    expect(JSON.parse(init.body as string)).toEqual({ refresh_token: 'r-native' });
    expect(locks.requests).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });

  it('revokeSession rotates, then revokes the rotated refresh token', async () => {
    const { srv, fetch } = fakeServer();
    srv.refreshCurrent = 'r-native';

    await new ApiClient().revokeSession();

    const calls = fetch.mock.calls as Array<[string, RequestInit]>;
    expect(calls.map(([u]) => u)).toEqual([
      'http://server:7070/api/v1/auth/refresh',
      'http://server:7070/api/v1/auth/logout',
    ]);
    expect(JSON.parse(calls[1][1].body as string)).toEqual({ refresh_token: 'r2' });
    expect(srv.refreshCurrent).toBe('logged-out');
  });

  it('a refresh with no verdict (5xx) fails the request and keeps the in-memory tokens', async () => {
    const locks = fakeLocks();
    fakeServer({ refreshStatus: 503 });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);

    await expect(new ApiClient().get('/items/1')).rejects.toMatchObject({ code: REFRESH_UNAVAILABLE_CODE });

    expect(getBearerToken()).toBe('a-native');
    expect(loc.href).toBe('/');
    expect(locks.requests).toBe(0);
  });

  it('a 2xx whose body can\'t be read leaves the held token superseded: dead to us, never resent', async () => {
    const { srv } = fakeServer({ refreshBadBody: true });
    srv.refreshCurrent = 'r-native';

    await expect(tryRefresh(new ApiClient())).resolves.toBe(false);
    expect(srv.refreshCurrent).toBe('r2');
  });

  // A sign-out that never settles leaves the layout's "Signing out…" button
  // disabled, so each POST revokeSession makes is capped like cookie mode's.
  it('sign-out: a rotation that hangs is aborted after 20 s and the sign-out settles', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer({ hangRefreshes: 1 });
    srv.refreshCurrent = 'r-native';

    const t = track(new ApiClient().revokeSession());
    await vi.advanceTimersByTimeAsync(19_000);
    expect(t.state).toBe('pending');

    await vi.advanceTimersByTimeAsync(2_000);
    expect(t.state).toBe('rejected');
    expect((t.error as DOMException).name).toBe('AbortError');
    expect(srv.refreshPosts).toHaveLength(1);
    expect(srv.logoutPosts).toHaveLength(0);
  });

  it('sign-out: a revoke POST that hangs is aborted after 20 s and the sign-out settles', async () => {
    vi.useFakeTimers();
    const { srv, fetch } = fakeServer({ hangLogouts: 1 });
    srv.refreshCurrent = 'r-native';

    const t = track(new ApiClient().revokeSession());
    await vi.advanceTimersByTimeAsync(19_000);
    expect(t.state).toBe('pending');
    expect(srv.logoutPosts).toEqual(['r2']);

    await vi.advanceTimersByTimeAsync(2_000);
    expect(t.state).toBe('rejected');
    expect((t.error as DOMException).name).toBe('AbortError');
    const logoutInit = (fetch.mock.calls as Array<[string, RequestInit]>).find(([u]) => u.endsWith('/auth/logout'))![1];
    expect(logoutInit.signal?.aborted).toBe(true);
  });

  it('sign-out: joins a refresh already running (never a second rotation of the same token), past the 20 s cap', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer({ refreshDelayMs: 30_000 });
    srv.refreshCurrent = 'r-native';
    const tab = new ApiClient();

    const request = track(tab.get('/items/1')); // 401 → a slow refresh
    await vi.advanceTimersByTimeAsync(1_000);
    const t = track(tab.revokeSession());

    // Not given up at the cap: the refresh may still land, and a sign-out
    // that had moved on would have it store its pair and user again.
    await vi.advanceTimersByTimeAsync(25_000);
    expect(t.state).toBe('pending');
    expect(srv.refreshPosts).toEqual([{ sent: 'r-native', via: 'body' }]);

    await vi.advanceTimersByTimeAsync(5_000);
    expect(t.state).toBe('resolved');
    expect(request).toMatchObject({ state: 'resolved', value: { ok: true } });
    expect(srv.logoutPosts).toEqual(['r2']);
    expect(srv.refreshCurrent).toBe('logged-out');
    expect(srv.reuse).toBe(0);
  });
});

// A cross-origin browser embed: its own token copy, browser credentials, no
// cross-tab lock — and the same cap on its logout POST.
describe('cross-origin embed (bearer copy, credentials: include)', () => {
  beforeEach(() => {
    setApiBase('http://server:7070/api/v1');
    setBearerToken('a-embed', 'r-embed', null);
  });

  it('sign-out: a logout POST that hangs is aborted after 20 s and the sign-out settles', async () => {
    vi.useFakeTimers();
    const { srv, fetch } = fakeServer({ hangLogouts: 1 });

    const t = track(new ApiClient().revokeSession());
    await vi.advanceTimersByTimeAsync(19_000);
    expect(t.state).toBe('pending');

    await vi.advanceTimersByTimeAsync(2_000);
    expect(t.state).toBe('rejected');
    expect((t.error as DOMException).name).toBe('AbortError');
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('http://server:7070/api/v1/auth/logout');
    expect(init.credentials).toBe('include');
    expect(srv.refreshPosts).toHaveLength(0);
  });
});

describe.each([
  ['Web Locks', () => { fakeLocks(); }],
  ['localStorage lease fallback (no navigator.locks)', () => {}],
])('cookie-mode revokeSession — %s', (_name, setup) => {
  beforeEach(() => setup());

  const lockHeld = () =>
    (navigator as unknown as { locks?: { isHeld(): boolean } | null }).locks?.isHeld() ?? false;

  it('posts /auth/logout once, never refreshes, and frees the lock', async () => {
    const { fetch, srv } = fakeServer();

    await new ApiClient().revokeSession();

    expect((fetch.mock.calls as Array<[string]>).map(([u]) => u)).toEqual(['/api/v1/auth/logout']);
    expect(srv.logoutPosts).toEqual(['r1']);
    expect(srv.refreshPosts).toHaveLength(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
    expect(lockHeld()).toBe(false);
  });

  it('a sign-out behind another tab\'s refresh revokes the rotated session instead of being undone by it', async () => {
    // The server rotates the moment the refresh arrives; its Set-Cookie
    // lands only with the response (see the fake-server sanity case for
    // what an uncoordinated logout does here).
    const { srv } = fakeServer({ rotateOnArrival: true, refreshDelayMs: 100 });
    const refreshing = tryRefresh(new ApiClient());
    while (srv.refreshPosts.length === 0) await sleep(5);

    await Promise.all([refreshing, new ApiClient().revokeSession()]);

    expect(srv.logoutPosts).toEqual(['r2']);
    expect(srv.refreshCurrent).toBe('logged-out');
    expect(srv.jarRefresh).toBe('');
    expect(srv.reuse).toBe(0);
  });

  it('publishes a sign-out marker, not a refresh: a tab queued behind the sign-out finds the cleared jar and signs out too', async () => {
    const { srv } = fakeServer({ logoutDelayMs: 100 });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const other = new ApiClient();
    other.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    const signingOut = new ApiClient().revokeSession();
    while (srv.logoutPosts.length === 0) await sleep(5);
    await other.get('/items/1'); // 401 while the sign-out holds the lock
    await signingOut;

    expect(srv.refreshPosts).toEqual([{ sent: '', via: 'cookie' }]);
    expect(localStorage.getItem(MARKER_KEY)).toMatch(/^logout:/);
    expect(loc.href).toBe('/login');
    expect(other.getUser()).toBeNull();
    expect(srv.reuse).toBe(0);
  });

  it('marks the sign-out even when its POST fails; a later refresh just rotates', async () => {
    const { srv, fetch } = fakeServer();
    fetch.mockImplementationOnce(async () => { throw new TypeError('Failed to fetch'); });

    await expect(new ApiClient().revokeSession()).rejects.toThrow('Failed to fetch');
    expect(localStorage.getItem(MARKER_KEY)).toMatch(/^logout:/);
    expect(lockHeld()).toBe(false);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();

    // The jar still holds the session, and the next refresh just rotates it.
    await expect(tryRefresh(new ApiClient())).resolves.toBe(true);
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }]);
    expect(srv.reuse).toBe(0);
  });
});

// The queue that used to strand a tab: tab C refreshes (holding the lock),
// tab A signs out, tab B hits a 401. B snapshotted the marker before C's
// refresh changed it; had it taken that change for "the jar holds a fresh
// pair", it would have skipped its POST, retried against the jar A emptied,
// got a plain 401 and never been torn down.
describe('a refresh queued behind another tab\'s refresh and a sign-out', () => {
  it('Web Locks: C refreshes, A signs out, B posts to the emptied jar and is signed out too', async () => {
    const locks = fakeLocks();
    const { srv } = fakeServer({ refreshDelayMs: 100 });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tabC = new ApiClient();
    const tabA = new ApiClient();
    const tabB = new ApiClient();
    tabB.setUser({ user_id: 'u1', username: 'alice', is_admin: false });

    const refreshing = tryRefresh(tabC);
    while (srv.refreshPosts.length === 0) await sleep(5);
    const signingOut = tabA.revokeSession();
    while (locks.requests < 2) await sleep(1);
    const b = track(tabB.get('/items/1'));
    while (locks.requests < 3) await sleep(1);

    await Promise.all([refreshing, signingOut]);
    await vi.waitFor(() => expect(b.state).not.toBe('pending'));

    // C rotated r1 → r2; A revoked r2 and emptied the jar; B posted what was
    // left (nothing), got the server's verdict, and tore down.
    expect(srv.refreshPosts).toEqual([{ sent: 'r1', via: 'cookie' }, { sent: '', via: 'cookie' }]);
    expect(srv.logoutPosts).toEqual(['r2']);
    expect(srv.refreshCurrent).toBe('logged-out');
    expect(loc.href).toBe('/login');
    expect(tabB.getUser()).toBeNull();
    expect(srv.reuse).toBe(0);
    expect(locks.isHeld()).toBe(false);
  });

  it('lease fallback: a waiter that finds a sign-out marker keeps waiting through the logout, then posts', async () => {
    const { srv } = fakeServer();
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tabB = new ApiClient();
    tabB.setUser({ user_id: 'u1', username: 'alice', is_admin: false });
    const lease = (owner: string) =>
      localStorage.setItem(LEASE_KEY, JSON.stringify({ owner, exp: Date.now() + 30_000 }));
    lease('tab-c');

    const b = track(tabB.get('/items/1'));
    await sleep(150);
    // Tab C's refresh lands and tab A takes the lease for its sign-out, which
    // marks itself before its POST — all before B's next look.
    srv.refreshCurrent = srv.jarRefresh = 'r2';
    srv.accessCurrent = srv.jarAccess = 'a2';
    localStorage.setItem(MARKER_KEY, 'c-refresh');
    lease('tab-a');
    localStorage.setItem(MARKER_KEY, 'logout:a');
    await sleep(250);
    // B saw the marker change, but to a sign-out's: still waiting, no POST.
    expect(b.state).toBe('pending');
    expect(srv.refreshPosts).toHaveLength(0);

    // A's logout lands: session revoked, jar emptied, lease released.
    srv.refreshCurrent = srv.accessCurrent = 'logged-out';
    srv.jarRefresh = srv.jarAccess = '';
    localStorage.removeItem(LEASE_KEY);
    await vi.waitFor(() => expect(b.state).not.toBe('pending'));

    expect(srv.refreshPosts).toEqual([{ sent: '', via: 'cookie' }]);
    expect(loc.href).toBe('/login');
    expect(tabB.getUser()).toBeNull();
    expect(srv.reuse).toBe(0);
  });

  it('lease fallback: a real sign-out marks itself BEFORE its POST, so a waiter looking during the POST keeps waiting', async () => {
    vi.useFakeTimers();
    const { srv } = fakeServer({ logoutDelayMs: 1_000 });
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    const tabA = new ApiClient();
    const tabB = new ApiClient();
    tabB.setUser({ user_id: 'u1', username: 'alice', is_admin: false });
    // Tab C holds the lease: its refresh is in flight.
    localStorage.setItem(LEASE_KEY, JSON.stringify({ owner: 'tab-c', exp: Date.now() + 30_000 }));

    // B hits a 401, snapshots the marker and looks at the lease every 100 ms
    // (t = 0, 100, 200, …).
    const b = track(tabB.get('/items/1'));
    await vi.advanceTimersByTimeAsync(105);
    // Between two of B's looks: C's refresh lands and frees the lease, and A
    // signs out at once, claiming it. A's POST starts after its claim settles
    // (t = 155) and runs 1 s, so every look B takes from t = 200 falls inside
    // it — and until A marks its sign-out, B would see C's refresh.
    srv.refreshCurrent = srv.jarRefresh = 'r2';
    srv.accessCurrent = srv.jarAccess = 'a2';
    localStorage.setItem(MARKER_KEY, 'c-refresh');
    localStorage.removeItem(LEASE_KEY);
    const signingOut = track(tabA.revokeSession());

    await vi.advanceTimersByTimeAsync(600);
    expect(srv.logoutPosts).toEqual(['r2']);
    expect(signingOut.state).toBe('pending');
    // B saw the marker change, but to a sign-out's: still waiting, no POST.
    // (Had it taken C's refresh, its retry would already have gone through
    // on the cookies A's logout is about to clear.)
    expect(b.state).toBe('pending');
    expect(srv.refreshPosts).toHaveLength(0);

    // A's logout lands and frees the lease; B posts what's left in the jar
    // (nothing), gets the server's verdict and signs out too.
    await vi.advanceTimersByTimeAsync(2_000);
    expect(signingOut.state).toBe('resolved');
    expect(b.state).toBe('resolved');
    expect(srv.refreshPosts).toEqual([{ sent: '', via: 'cookie' }]);
    expect(srv.refreshCurrent).toBe('logged-out');
    expect(loc.href).toBe('/login');
    expect(tabB.getUser()).toBeNull();
    expect(srv.reuse).toBe(0);
    expect(localStorage.getItem(LEASE_KEY)).toBeNull();
  });
});

// A waiter skips its POST only when the marker changed to ANOTHER refresh. A
// marker that is simply gone — site data cleared while it queued, cookies
// with it — says nothing of the kind: the waiter must post, find the emptied
// jar and sign out, rather than retry on cookies that aren't there and stay
// looking signed in.
describe('a marker that vanished while a tab queued is no refresh', () => {
  const clearSiteData = (srv: { jarAccess: string; jarRefresh: string }) => {
    localStorage.clear();
    srv.jarAccess = srv.jarRefresh = '';
  };

  it('Web Locks', async () => {
    const locks = fakeLocks();
    const { srv } = fakeServer();
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    localStorage.setItem(MARKER_KEY, 'earlier-refresh');
    let release!: () => void;
    void locks.request('onscreen-auth-refresh', () => new Promise<void>((resolve) => { release = resolve; }));

    const t = track(new ApiClient().get('/items/1'));
    while (locks.requests < 2) await sleep(1);
    clearSiteData(srv);
    release();
    await vi.waitFor(() => expect(t.state).not.toBe('pending'));

    expect(t.state).toBe('resolved');
    expect(srv.refreshPosts).toEqual([{ sent: '', via: 'cookie' }]);
    expect(loc.href).toBe('/login');
    expect(srv.reuse).toBe(0);
  });

  it('lease fallback', async () => {
    const { srv } = fakeServer();
    const loc = { href: '/' };
    vi.spyOn(window, 'location', 'get').mockReturnValue(loc as unknown as Location);
    localStorage.setItem(MARKER_KEY, 'earlier-refresh');
    localStorage.setItem(LEASE_KEY, JSON.stringify({ owner: 'other-tab', exp: Date.now() + 30_000 }));

    const t = track(new ApiClient().get('/items/1'));
    await sleep(150);
    clearSiteData(srv); // the lease goes with it
    await vi.waitFor(() => expect(t.state).not.toBe('pending'));

    expect(t.state).toBe('resolved');
    expect(srv.refreshPosts).toEqual([{ sent: '', via: 'cookie' }]);
    expect(loc.href).toBe('/login');
    expect(srv.reuse).toBe(0);
  });
});
