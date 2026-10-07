// Manual collections — an admin-curated, shared movie / TV collection
// (migration 00037): create, fill in one batch, reorder, sort, pick a cover,
// and see it everywhere it surfaces (the collections list, its own page, the
// movie's "In collections" chips and the library's Collections tab).
//
// Self-contained: each test makes its own collection from the first two
// movies of the first movie library and deletes it afterwards. Skips when the
// server has fewer than two movies.
//
// Required env:
//   E2E_USERNAME   admin username (default 'admin')
//   E2E_PASSWORD   admin password (API specs also run with E2E_TOKEN)

import { test, expect, type APIRequestContext } from '@playwright/test';
import { CAN_API, CAN_UI, adminToken, auth, loginUI } from './_auth';

interface Movie { id: string; title: string; library_id: string }

async function twoMovies(request: APIRequestContext, token: string): Promise<Movie[]> {
  const libs = (await (await request.get('/api/v1/libraries', auth(token))).json()).data as { id: string; type: string }[];
  for (const lib of libs.filter((l) => l.type === 'movie')) {
    const r = await request.get(`/api/v1/libraries/${lib.id}/items?limit=2&offset=0`, auth(token));
    const items = ((await r.json()).data ?? []) as { id: string; title: string; type: string }[];
    const movies = items.filter((i) => i.type === 'movie');
    if (movies.length === 2) return movies.map((m) => ({ ...m, library_id: lib.id }));
  }
  return [];
}

async function createCollection(request: APIRequestContext, token: string, name: string): Promise<string> {
  const r = await request.post('/api/v1/collections', { ...auth(token), data: { name, type: 'manual' } });
  expect(r.status(), `create manual collection (${await r.text()})`).toBe(201);
  return (await r.json()).data.id as string;
}

test.describe('Manual collections — API', () => {
  test.skip(!CAN_API, 'set E2E_PASSWORD or E2E_TOKEN to run collection specs');

  test('create, fill, reorder, sort, cover, list, delete', async ({ request }) => {
    const token = await adminToken(request);
    const movies = await twoMovies(request, token);
    test.skip(movies.length < 2, 'needs a movie library with two movies');
    const [a, b] = movies;
    const id = await createCollection(request, token, `E2E collection ${Date.now()}`);
    try {
      // One batch adds both; an episode-type or unknown id would refuse it all.
      const add = await request.post(`/api/v1/collections/${id}/items`, { ...auth(token), data: { media_item_ids: [a.id, b.id] } });
      expect(add.status()).toBe(204);
      const unknown = await request.post(`/api/v1/collections/${id}/items`, {
        ...auth(token), data: { media_item_ids: ['00000000-0000-0000-0000-000000000000'] },
      });
      expect(unknown.status()).toBe(404);

      const order = async () =>
        ((await (await request.get(`/api/v1/collections/${id}/items`, auth(token))).json()).data as { id: string }[]).map((i) => i.id);
      expect(await order()).toEqual([a.id, b.id]);

      const re = await request.put(`/api/v1/collections/${id}/items/order`, { ...auth(token), data: { item_ids: [b.id, a.id] } });
      expect(re.status()).toBe(204);
      expect(await order()).toEqual([b.id, a.id]);

      const patch = await request.patch(`/api/v1/collections/${id}`, { ...auth(token), data: { item_order: 'title', poster_item_id: a.id } });
      expect(patch.status()).toBe(200);
      const patched = (await patch.json()).data;
      expect(patched.item_order).toBe('title');
      expect(patched.poster_item_id).toBe(a.id);
      expect(patched.item_count).toBe(2);
      // (Title order sorts by the server's sort title; the SQL order itself is
      // pinned by the Postgres integration test.)
      expect((await order()).length).toBe(2);

      // In the listing, on the movie, and on the library's Collections tab.
      const list = (await (await request.get('/api/v1/collections', auth(token))).json()).data as { id: string; type: string; item_count?: number }[];
      const listed = list.find((c) => c.id === id);
      expect(listed?.type).toBe('manual');
      expect(listed?.item_count).toBe(2);
      const detail = (await (await request.get(`/api/v1/items/${a.id}`, auth(token))).json()).data as { collections?: { id: string }[] };
      expect(detail.collections?.some((c) => c.id === id)).toBe(true);
      const tab = (await (await request.get(`/api/v1/libraries/${a.library_id}/collections`, auth(token))).json()).data as { id: string; type: string }[];
      expect(tab.find((c) => c.id === id)?.type).toBe('manual');
    } finally {
      await request.delete(`/api/v1/collections/${id}`, auth(token));
    }
    const gone = await request.get(`/api/v1/collections/${id}`, auth(token));
    expect(gone.status()).toBe(404);
  });

  test('POST /collections without a type still makes a private playlist', async ({ request }) => {
    const token = await adminToken(request);
    const r = await request.post('/api/v1/collections', { ...auth(token), data: { name: `E2E playlist ${Date.now()}` } });
    expect(r.status()).toBe(201);
    const pl = (await r.json()).data;
    try {
      expect(pl.type).toBe('playlist');
    } finally {
      await request.delete(`/api/v1/collections/${pl.id}`, auth(token));
    }
  });
});

test.describe('Manual collections — web', () => {
  test.skip(!CAN_UI, 'set E2E_PASSWORD to run collection UI specs');

  test('the collection page, the movie chip and the collections list', async ({ page, request }) => {
    const token = await adminToken(request);
    const movies = await twoMovies(request, token);
    test.skip(movies.length < 2, 'needs a movie library with two movies');
    const [a, b] = movies;
    const name = `E2E web collection ${Date.now()}`;
    const id = await createCollection(request, token, name);
    try {
      await request.post(`/api/v1/collections/${id}/items`, { ...auth(token), data: { media_item_ids: [a.id, b.id] } });
      await loginUI(page);

      await page.goto(`/collections/${id}`);
      await expect(page.getByRole('heading', { name })).toBeVisible();
      await expect(page.getByText(a.title, { exact: true })).toBeVisible();
      // Admin controls: sort, cover, custom-order moves.
      await expect(page.getByLabel('Sort by')).toHaveValue('custom');
      await page.getByRole('button', { name: `Move ${a.title} later` }).click();
      await expect(page.getByRole('button', { name: `Move ${b.title} earlier` })).toBeDisabled();
      await page.getByRole('button', { name: `Use ${b.title} as the cover` }).click();
      await expect(page.getByRole('button', { name: `Use ${b.title} as the cover` })).toHaveAttribute('aria-pressed', 'true');

      // The movie page links back through its "In collections" chip.
      await page.goto(`/watch/${a.id}`);
      await expect(page.getByRole('link', { name })).toHaveAttribute('href', `/collections/${id}`);

      // The collections list shows it under Collections, with its count.
      await page.goto('/collections');
      const card = page.getByRole('link', { name: new RegExp(name) });
      await expect(card).toBeVisible();
      await expect(card).toContainText('2 titles');
    } finally {
      await request.delete(`/api/v1/collections/${id}`, auth(token));
    }
  });
});
