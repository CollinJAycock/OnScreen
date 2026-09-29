import { photoAlbumApi, photoApi } from './api';

function mockFetch(body: unknown, status = 200) {
  return vi.fn().mockResolvedValue({
    ok: status < 400,
    status,
    json: () => Promise.resolve(body),
  });
}

function call(fetch: ReturnType<typeof mockFetch>) {
  const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
  return { url, method: init.method, body: init.body ? JSON.parse(init.body as string) : undefined };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('photoApi.map', () => {
  it('asks for a library and returns the points with the library-wide total', async () => {
    const fetch = mockFetch({ data: [{ id: 'p-1', lat: 1, lon: 2 }], meta: { total: 7 } });
    vi.stubGlobal('fetch', fetch);
    const r = await photoApi.map('lib-1');
    expect(call(fetch)).toMatchObject({ url: '/api/v1/photos/map?library_id=lib-1', method: 'GET' });
    expect(r).toEqual({ items: [{ id: 'p-1', lat: 1, lon: 2 }], total: 7 });
  });

  it('sends the box and limit, skipping unset or non-finite edges', async () => {
    const fetch = mockFetch({ data: [], meta: { total: 0 } });
    vi.stubGlobal('fetch', fetch);
    await photoApi.map('lib-1', { min_lat: -10.5, max_lat: 20, min_lon: 170, max_lon: NaN, limit: 25000 });
    const u = new URL(call(fetch).url, 'http://x');
    expect(u.pathname).toBe('/api/v1/photos/map');
    expect(Object.fromEntries(u.searchParams)).toEqual({
      library_id: 'lib-1', min_lat: '-10.5', max_lat: '20', min_lon: '170', limit: '25000',
    });
  });
});

describe('photoAlbumApi', () => {
  it('lists the caller\'s albums', async () => {
    const fetch = mockFetch({ data: [] });
    vi.stubGlobal('fetch', fetch);
    await photoAlbumApi.list();
    expect(call(fetch)).toMatchObject({ url: '/api/v1/photo-albums', method: 'GET' });
  });

  it('creates, renames and deletes', async () => {
    let fetch = mockFetch({ data: { id: 'a-1', name: 'Trip' } }, 201);
    vi.stubGlobal('fetch', fetch);
    await photoAlbumApi.create('Trip');
    expect(call(fetch)).toEqual({ url: '/api/v1/photo-albums', method: 'POST', body: { name: 'Trip' } });

    fetch = mockFetch({ data: { id: 'a-1', name: 'Trip 2' } });
    vi.stubGlobal('fetch', fetch);
    await photoAlbumApi.rename('a-1', 'Trip 2');
    expect(call(fetch)).toEqual({ url: '/api/v1/photo-albums/a-1', method: 'PATCH', body: { name: 'Trip 2' } });

    fetch = mockFetch(undefined, 204);
    vi.stubGlobal('fetch', fetch);
    await photoAlbumApi.delete('a-1');
    expect(call(fetch)).toMatchObject({ url: '/api/v1/photo-albums/a-1', method: 'DELETE' });
  });

  it('lists, adds and removes an album\'s photos', async () => {
    let fetch = mockFetch({ data: [{ id: 'p-1' }], meta: { total: 1 } });
    vi.stubGlobal('fetch', fetch);
    expect(await photoAlbumApi.items('a-1')).toEqual({ items: [{ id: 'p-1' }], total: 1 });
    expect(call(fetch)).toMatchObject({ url: '/api/v1/photo-albums/a-1/items', method: 'GET' });

    fetch = mockFetch(undefined, 204);
    vi.stubGlobal('fetch', fetch);
    await photoAlbumApi.addItem('a-1', 'p-1');
    expect(call(fetch)).toEqual({ url: '/api/v1/photo-albums/a-1/items', method: 'POST', body: { media_item_id: 'p-1' } });

    fetch = mockFetch(undefined, 204);
    vi.stubGlobal('fetch', fetch);
    await photoAlbumApi.removeItem('a-1', 'p-1');
    expect(call(fetch)).toMatchObject({ url: '/api/v1/photo-albums/a-1/items/p-1', method: 'DELETE' });
  });

  it('surfaces the server\'s message on failure', async () => {
    vi.stubGlobal('fetch', mockFetch({ error: { code: 'BAD_REQUEST', message: 'only photo items can be added to a photo album' } }, 400));
    await expect(photoAlbumApi.addItem('a-1', 'm-1')).rejects.toThrow('only photo items can be added to a photo album');
  });
});
