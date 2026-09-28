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
