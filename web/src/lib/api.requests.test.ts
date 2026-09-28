import { describe, it, expect, vi, afterEach } from 'vitest';
import { requestsApi, requestsAdminApi, userApi } from './api';

// Request quotas, the can-request switch and the admin pending badge.

function mockFetch(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  });
}

describe('request quota API', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('quota calls GET /requests/quota', async () => {
    const quota = {
      can_request: true,
      window_days: 7,
      movies: { limit: 3, used: 1, remaining: 2 },
      tv: { limit: 0, used: 0, remaining: null },
    };
    const fetch = mockFetch(200, { data: quota });
    vi.stubGlobal('fetch', fetch);
    const got = await requestsApi.quota();
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/requests/quota');
    expect(got.movies.remaining).toBe(2);
    expect(got.tv.remaining).toBeNull();
  });

  it('pendingCount calls GET /requests/pending-count', async () => {
    const fetch = mockFetch(200, { data: { count: 4 } });
    vi.stubGlobal('fetch', fetch);
    expect(await requestsAdminApi.pendingCount()).toEqual({ count: 4 });
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/requests/pending-count');
  });

  it('create returns over_quota from the server', async () => {
    vi.stubGlobal('fetch', mockFetch(201, { data: { id: 'r1', status: 'pending', over_quota: true } }));
    const created = await requestsApi.create({ type: 'movie', tmdb_id: 949 });
    expect(created.over_quota).toBe(true);
  });

  it('setRequestPermissions sends only the given fields, with null quotas intact', async () => {
    const fetch = mockFetch(204, null);
    vi.stubGlobal('fetch', fetch);
    await userApi.setRequestPermissions('user-7', { can_request: false, quota_movies: null, quota_tv: 3 });
    const [url, opts] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/users/user-7/request-permissions');
    expect(opts.method).toBe('PUT');
    expect(JSON.parse(opts.body as string)).toEqual({ can_request: false, quota_movies: null, quota_tv: 3 });
  });
});
