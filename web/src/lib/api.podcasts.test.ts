import { describe, it, expect, vi, afterEach } from 'vitest';
import { podcastApi } from './api';

function mockFetch(body: unknown, status = 200) {
  return vi.fn().mockResolvedValue({
    ok: status < 400,
    status,
    json: () => Promise.resolve(body),
  });
}

describe('podcastApi', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("lists a library's feeds", async () => {
    const fetch = mockFetch({ data: [], meta: { total: 0 } });
    vi.stubGlobal('fetch', fetch);
    await podcastApi.listFeeds('lib-1');
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/admin/podcast-feeds?library_id=lib-1',
      expect.objectContaining({ method: 'GET' }),
    );
  });

  it('subscribes with the chosen settings', async () => {
    const fetch = mockFetch({ data: { id: 'f-1', title: 'Show' } });
    vi.stubGlobal('fetch', fetch);
    await podcastApi.subscribe({
      library_id: 'lib-1', feed_url: 'https://a/feed.xml', auto_download: false, keep_latest: 5, download_latest: 0,
    });
    expect(fetch).toHaveBeenCalledWith('/api/v1/admin/podcast-feeds', expect.objectContaining({ method: 'POST' }));
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      library_id: 'lib-1', feed_url: 'https://a/feed.xml', auto_download: false, keep_latest: 5, download_latest: 0,
    });
  });

  it('unsubscribes, deleting files only when asked', async () => {
    const fetch = mockFetch(null, 204);
    vi.stubGlobal('fetch', fetch);
    await podcastApi.unsubscribe('f-1', false);
    await podcastApi.unsubscribe('f-1', true);
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/admin/podcast-feeds/f-1');
    expect(fetch.mock.calls[1][0]).toBe('/api/v1/admin/podcast-feeds/f-1?delete_files=true');
    expect(fetch.mock.calls[1][1]).toEqual(expect.objectContaining({ method: 'DELETE' }));
  });

  it('pages episodes and starts and deletes downloads', async () => {
    const fetch = mockFetch({ data: [], meta: { total: 0 } });
    vi.stubGlobal('fetch', fetch);
    await podcastApi.episodes('f-1', 50, 100);
    await podcastApi.download('e-1');
    await podcastApi.deleteDownload('e-1');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/admin/podcast-feeds/f-1/episodes?limit=50&offset=100');
    expect(fetch.mock.calls[1][0]).toBe('/api/v1/admin/podcast-episodes/e-1/download');
    expect(fetch.mock.calls[1][1]).toEqual(expect.objectContaining({ method: 'POST' }));
    expect(fetch.mock.calls[2][1]).toEqual(expect.objectContaining({ method: 'DELETE' }));
  });
});
