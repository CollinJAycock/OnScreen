import { describe, it, expect, vi, afterEach } from 'vitest';
import { itemApi, watchlistApi, watchlistQueryString } from './api';

function mockFetch(body: unknown, status = 200) {
  return vi.fn().mockResolvedValue({
    ok: status < 400,
    status,
    json: () => Promise.resolve(body),
  });
}

describe('watchlistApi', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('builds the list query with defaults', () => {
    expect(watchlistQueryString()).toBe('status=plan_to_watch&limit=50&offset=0');
    expect(watchlistQueryString({ status: 'all', type: 'movie', sort: 'title', limit: 10, offset: 20 }))
      .toBe('status=all&type=movie&sort=title&limit=10&offset=20');
  });

  it('lists a page and returns items + total', async () => {
    const fetch = mockFetch({ data: [{ id: 'a', title: 'Alien' }], meta: { total: 7 } });
    vi.stubGlobal('fetch', fetch);
    const res = await watchlistApi.list({ status: 'watching', limit: 60 });
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/watchlist?status=watching&limit=60&offset=0');
    expect(res).toEqual({ items: [{ id: 'a', title: 'Alien' }], total: 7 });
  });

  it('unwraps the counts envelope', async () => {
    vi.stubGlobal('fetch', mockFetch({ data: { total: 1, statuses: {} } }));
    expect(await watchlistApi.counts()).toEqual({ total: 1, statuses: {} });
  });
});

// The watch-status handler writes a bare body, not the {data} envelope.
// Reading `.data` off it returned undefined, so the detail page's Status
// menu never showed a saved status and every change reported an error.
describe('itemApi watch status', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('reads the bare GET body', async () => {
    vi.stubGlobal('fetch', mockFetch({ status: 'watching', created_at: 'x', updated_at: 'y' }));
    expect((await itemApi.getWatchStatus('i1')).status).toBe('watching');
  });

  it('reads the bare PUT body and sends the status', async () => {
    const fetch = mockFetch({ status: 'completed', created_at: 'x', updated_at: 'y' });
    vi.stubGlobal('fetch', fetch);
    expect((await itemApi.setWatchStatus('i1', 'completed')).status).toBe('completed');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/items/i1/watch-status');
    expect(fetch.mock.calls[0][1]).toEqual(expect.objectContaining({ method: 'PUT', body: '{"status":"completed"}' }));
  });

  it('still accepts an enveloped body', async () => {
    vi.stubGlobal('fetch', mockFetch({ data: { status: 'dropped' } }));
    expect((await itemApi.getWatchStatus('i1')).status).toBe('dropped');
  });

  it('throws the server error message', async () => {
    vi.stubGlobal('fetch', mockFetch({ error: { code: 'NOT_FOUND', message: 'resource not found' } }, 404));
    await expect(itemApi.getWatchStatus('i1')).rejects.toThrow('resource not found');
  });
});
