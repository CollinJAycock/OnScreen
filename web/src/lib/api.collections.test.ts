import { describe, it, expect, vi, afterEach } from 'vitest';
import { collectionApi } from './api';

function mockFetch(body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: () => Promise.resolve(body),
  });
}

describe('collectionApi.list', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('leaves franchise collections out by default (server opt-in)', async () => {
    const fetch = mockFetch({ data: [] });
    vi.stubGlobal('fetch', fetch);
    await collectionApi.list();
    expect(fetch).toHaveBeenCalledWith('/api/v1/collections', expect.objectContaining({ method: 'GET' }));
  });

  it('asks for franchises with ?include=franchise when requested', async () => {
    const fetch = mockFetch({ data: [] });
    vi.stubGlobal('fetch', fetch);
    await collectionApi.list({ includeFranchise: true });
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/collections?include=franchise',
      expect.objectContaining({ method: 'GET' }),
    );
  });
});

describe('collectionApi manual collections', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function bodyOf(fetch: ReturnType<typeof vi.fn>) {
    return JSON.parse(fetch.mock.calls[0][1].body);
  }

  it('createManual posts type manual with the order', async () => {
    const fetch = mockFetch({ data: { id: 'c1' } });
    vi.stubGlobal('fetch', fetch);
    await collectionApi.createManual('Spooky', undefined, 'release');
    expect(fetch).toHaveBeenCalledWith('/api/v1/collections', expect.objectContaining({ method: 'POST' }));
    expect(bodyOf(fetch)).toEqual({ name: 'Spooky', type: 'manual', item_order: 'release' });
  });

  it('createManual defaults to the custom order', async () => {
    const fetch = mockFetch({ data: { id: 'c1' } });
    vi.stubGlobal('fetch', fetch);
    await collectionApi.createManual('Spooky');
    expect(bodyOf(fetch).item_order).toBe('custom');
  });

  it('updateSettings patches only the given fields', async () => {
    const fetch = mockFetch({ data: { id: 'c1' } });
    vi.stubGlobal('fetch', fetch);
    await collectionApi.updateSettings('c1', { item_order: 'title', poster_item_id: '' });
    expect(fetch).toHaveBeenCalledWith('/api/v1/collections/c1', expect.objectContaining({ method: 'PATCH' }));
    expect(bodyOf(fetch)).toEqual({ item_order: 'title', poster_item_id: '' });
  });

  it('addItems posts the batch', async () => {
    const fetch = mockFetch({ data: null });
    vi.stubGlobal('fetch', fetch);
    await collectionApi.addItems('c1', ['a', 'b']);
    expect(fetch).toHaveBeenCalledWith('/api/v1/collections/c1/items', expect.objectContaining({ method: 'POST' }));
    expect(bodyOf(fetch)).toEqual({ media_item_ids: ['a', 'b'] });
  });

  it('reorder puts every member in order', async () => {
    const fetch = mockFetch({ data: null });
    vi.stubGlobal('fetch', fetch);
    await collectionApi.reorder('c1', ['b', 'a']);
    expect(fetch).toHaveBeenCalledWith('/api/v1/collections/c1/items/order', expect.objectContaining({ method: 'PUT' }));
    expect(bodyOf(fetch)).toEqual({ item_ids: ['b', 'a'] });
  });
});
