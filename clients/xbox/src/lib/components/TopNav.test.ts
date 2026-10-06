import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';
import { get } from 'svelte/store';

// TopNav reads the route through SvelteKit, which isn't here; it only
// subscribes to page.url to re-check the hash.
vi.mock('$app/state', () => ({ page: { url: new URL('http://tv.local/') } }));

import TopNav from './TopNav.svelte';
import { api } from '../api/client';
import { NAV_GATES_CLOSED, navGates, resetNavGates } from '../navGates';

// The nav rendered on the server (no DOM, so no focusable action): the pills
// it shows for what the real API client gets back. Nothing here calls
// ensureNavGates: each render asks for its pills itself, as the app's bars
// do, and the sign-outs below reset them through api.clearTokens alone.

const ORIGIN = 'https://onscreen.example';
const OTHER_ORIGIN = 'https://other.example';

function memoryStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
}

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

type Answer = Response | Error;

/** What the server answers on the two endpoints the gates ask. */
let server: { capabilities: Answer; quota: Answer };
let fetchMock: ReturnType<typeof vi.fn>;

const ok = (data: unknown) => respond(200, { data });
const fail = (status: number, code: string, message: string) =>
  respond(status, { error: { code, message } });

const features = (f: Record<string, boolean>) => ok({ features: f });
const REVIEW_SERVER = features({ requests: true, live_tv: true, dvr: true, live_tv_configured: false });
const CONFIGURED = features({ requests: true, live_tv: true, dvr: true, live_tv_configured: true });
const OLDER = features({ requests: true, live_tv: true, dvr: true });
const quota = (canRequest: boolean) =>
  ok({
    can_request: canRequest,
    window_days: 7,
    movies: { limit: 0, used: 0, remaining: null },
    tv: { limit: 0, used: 0, remaining: null },
  });

function signIn(userId: string, isAdmin = false) {
  api.setTokens({
    access_token: `access-${userId}`,
    refresh_token: `refresh-${userId}`,
    asset_token: `asset-${userId}`,
    expires_at: '2026-11-01T00:00:00Z',
    user_id: userId,
    username: userId,
    is_admin: isAdmin,
  });
}

/** Lets every load under way finish (the fetch mock answers at once). */
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

/** Renders the bar, which asks for its pills, and lets that load finish
 *  (first any a previous render started, so it answers from `server` as
 *  set now). */
async function mountAndLoad(): Promise<void> {
  await settle();
  // The render runs when its body is read.
  void render(TopNav).body;
  await settle();
}

/** The pill labels, in order. */
function pills(): string[] {
  const { body } = render(TopNav);
  // No matchAll: the app's lib is ES2019.
  const re = /<a[^>]*class="link[^"]*"[^>]*>\s*([^<]*?)\s*<\/a>/g;
  const out: string[] = [];
  for (let m = re.exec(body); m; m = re.exec(body)) out.push(m[1]);
  return out;
}

/** The label of the pill marked as this page's, or null. */
function currentPill(): string | null {
  const { body } = render(TopNav);
  const m = /<a[^>]*aria-current="page"[^>]*>\s*([^<]*?)\s*<\/a>/.exec(body);
  return m ? m[1] : null;
}

const ALWAYS = ['Home', 'Libraries', 'Search', 'Favorites', 'History', 'Settings'];
const EVERY = [
  'Home',
  'Libraries',
  'Search',
  'Favorites',
  'History',
  'Discover',
  'Live TV',
  'Recordings',
  'Settings',
];

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('location', { hash: '#/hub/' });
  server = { capabilities: CONFIGURED, quota: quota(true) };
  fetchMock = vi.fn(async (url: string) => {
    const path = String(url).replace(/^https?:\/\/[^/]+/, '');
    const a =
      path === '/api/v1/system/capabilities'
        ? server.capabilities
        : path === '/api/v1/requests/quota'
          ? server.quota
          : respond(404);
    if (a instanceof Error) throw a;
    return a;
  });
  vi.stubGlobal('fetch', fetchMock);
  api.setOrigin(ORIGIN);
  signIn('u1');
  resetNavGates();
});

afterEach(async () => {
  resetNavGates();
  await settle();
  vi.unstubAllGlobals();
});

describe('TopNav pills', () => {
  it('asks for its pills when it renders, and shows the gated ones once answered', async () => {
    // Before the server has answered: only the ungated pills.
    expect(pills()).toEqual(ALWAYS);
    await settle();
    expect(fetchMock.mock.calls.map((c) => String(c[0]))).toEqual(
      expect.arrayContaining([`${ORIGIN}/api/v1/system/capabilities`, `${ORIGIN}/api/v1/requests/quota`]),
    );
    expect(pills()).toEqual(EVERY);
  });

  it('asks nothing more from the next bar once the pills are known', async () => {
    await mountAndLoad();
    const calls = fetchMock.mock.calls.length;
    expect(pills()).toEqual(EVERY);
    await settle();
    expect(fetchMock.mock.calls.length).toBe(calls);
  });

  it('hides Discover, Live TV and Recordings from the store reviewer', async () => {
    signIn('reviewer');
    server = { capabilities: REVIEW_SERVER, quota: quota(false) };
    await mountAndLoad();
    expect(pills()).toEqual(ALWAYS);
  });

  it('shows every pill to an admin on a server with a TMDB key and a tuner', async () => {
    signIn('admin', true);
    server = { capabilities: CONFIGURED, quota: quota(true) };
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);
  });

  it('shows Live TV and Recordings on a server older than live_tv_configured', async () => {
    server = { capabilities: OLDER, quota: quota(false) };
    await mountAndLoad();
    expect(pills()).toEqual([...ALWAYS.slice(0, 5), 'Live TV', 'Recordings', 'Settings']);
  });

  it('shows Discover when the server has no quota endpoint (404)', async () => {
    server = {
      capabilities: features({ requests: true, live_tv: false, dvr: false }),
      quota: fail(404, 'NOT_FOUND', 'resource not found'),
    };
    await mountAndLoad();
    expect(pills()).toEqual([...ALWAYS.slice(0, 5), 'Discover', 'Settings']);
  });

  it("shows Discover when an older server's /requests/{id} answers the quota with 400", async () => {
    server = { capabilities: OLDER, quota: fail(400, 'BAD_REQUEST', 'invalid request id') };
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);
  });

  it('shows Discover on a v2.4 server reporting requests off for a Settings TMDB key, as 0.2.0 did', async () => {
    server = {
      capabilities: features({ requests: false, live_tv: true, dvr: true }),
      quota: fail(400, 'BAD_REQUEST', 'invalid request id'),
    };
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);
  });

  it('keeps every gated pill hidden when the capabilities fetch fails', async () => {
    server = { capabilities: new TypeError('Failed to fetch'), quota: quota(true) };
    await mountAndLoad();
    expect(pills()).toEqual(ALWAYS);

    server = { capabilities: fail(503, 'ERROR', 'unavailable'), quota: quota(true) };
    await mountAndLoad();
    expect(pills()).toEqual(ALWAYS);

    // The next bar asks again.
    server = { capabilities: CONFIGURED, quota: quota(true) };
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);
  });

  it('asks the signed-in server for both answers, with the Bearer for the quota', async () => {
    await mountAndLoad();
    const quotaCall = fetchMock.mock.calls.find((c) => String(c[0]).endsWith('/requests/quota'));
    expect((quotaCall?.[1] as RequestInit).headers).toMatchObject({ Authorization: 'Bearer access-u1' });
  });
});

describe('TopNav across sign-out and server change', () => {
  it('drops the pills on sign-out, and the next account gets its own', async () => {
    signIn('admin', true);
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);

    // Sign-out: clearing the stored sign-in alone closes the gates, before
    // any bar asks.
    api.clearTokens();
    expect(get(navGates)).toEqual(NAV_GATES_CLOSED);
    expect(pills()).toEqual(ALWAYS);

    signIn('reviewer');
    server = { capabilities: CONFIGURED, quota: quota(false) };
    await mountAndLoad();
    expect(pills()).toEqual([...ALWAYS.slice(0, 5), 'Live TV', 'Recordings', 'Settings']);
  });

  it('drops the pills when a refresh is rejected', async () => {
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);
    api.clearTokens('rejected');
    expect(get(navGates)).toEqual(NAV_GATES_CLOSED);
  });

  it('asks again when the same account signs back in', async () => {
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);

    // Meanwhile an admin turned requesting off and the tuner went away.
    api.clearTokens();
    signIn('u1');
    server = { capabilities: REVIEW_SERVER, quota: quota(false) };
    await mountAndLoad();
    expect(pills()).toEqual(ALWAYS);
  });

  it('asks the new server after Change server', async () => {
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);

    // Change server: sign-out, a new address, a new sign-in.
    api.clearTokens();
    api.setOrigin(OTHER_ORIGIN);
    signIn('u1');
    server = { capabilities: REVIEW_SERVER, quota: quota(false) };
    fetchMock.mockClear();
    await mountAndLoad();
    expect(pills()).toEqual(ALWAYS);
    expect(fetchMock.mock.calls.map((c) => String(c[0]))).toEqual(
      expect.arrayContaining([`${OTHER_ORIGIN}/api/v1/system/capabilities`]),
    );
  });

  it('starts over for a different user even without a sign-out', async () => {
    signIn('admin', true);
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);

    signIn('reviewer');
    server = { capabilities: REVIEW_SERVER, quota: quota(false) };
    // Closed at once, not the admin's pills while the reviewer's load.
    expect(pills()).toEqual(ALWAYS);
    await settle();
    expect(pills()).toEqual(ALWAYS);
  });

  it("keeps the pills when the stored address moves from http to https (the client's TLS upgrade)", async () => {
    api.setOrigin('http://onscreen.example:7070');
    await mountAndLoad();
    expect(pills()).toEqual(EVERY);

    api.setOrigin('https://onscreen.example');
    fetchMock.mockClear();
    expect(pills()).toEqual(EVERY);
    await settle();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe('TopNav current-page pill', () => {
  it('marks the open page among the gated pills', async () => {
    vi.stubGlobal('location', { hash: '#/livetv/' });
    await mountAndLoad();
    expect(currentPill()).toBe('Live TV');
  });

  it('marks nothing on a page whose pill is hidden (reached another way)', async () => {
    vi.stubGlobal('location', { hash: '#/discover/' });
    server = { capabilities: REVIEW_SERVER, quota: quota(false) };
    await mountAndLoad();
    expect(currentPill()).toBeNull();
  });

  it('still marks an ungated page with fewer pills', async () => {
    vi.stubGlobal('location', { hash: '#/settings/' });
    server = { capabilities: REVIEW_SERVER, quota: quota(false) };
    await mountAndLoad();
    expect(currentPill()).toBe('Settings');
  });
});
