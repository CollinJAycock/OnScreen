// Hub-shape regression — the home hub is the highest-traffic API surface
// in the product (every client polls it on every screen wake), so its
// section list is a contract we don't want to silently break. v2.1 added
// a Continue-Watching split (tv / movies / other) and a Trending row;
// the cooccurrence "Because you watched" row was built and removed.
//
// Required env:
//   E2E_USERNAME   OnScreen username (default 'admin')
//   E2E_PASSWORD   OnScreen password — required; block skips otherwise

import { test, expect } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

test.describe('Home hub — section contract', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run hub specs');

  test('GET /api/v1/hub returns the v2.1 section set', async ({ request }) => {
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    const hubR = await request.get('/api/v1/hub', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(hubR.status()).toBe(200);
    const { data } = await hubR.json();

    // Hub is keyed by section name (not an array of section objects).
    // Each value is an array of items.
    expect(typeof data, 'hub.data must be an object keyed by section name').toBe('object');

    // The Continue-Watching split shipped in v2.1 — three new keys live
    // alongside the legacy combined `continue_watching` for backward
    // compat. Older clients keep working off the combined feed.
    for (const key of [
      'continue_watching',
      'continue_watching_tv',
      'continue_watching_movies',
      'continue_watching_other',
      'recently_added',
      'recently_added_by_library',
      'trending',
    ]) {
      expect(data, `hub must expose section "${key}" (v2.1 contract)`).toHaveProperty(key);
    }

    // Cooccurrence ("Because you watched") was built then removed for v2.1
    // — the table + SQL stayed dormant in case it earns a comeback. Guard
    // here so it doesn't accidentally re-enter the surface without an
    // explicit decision; if it ships intentionally, update this list.
    expect(data, 'cooccurrence row must not surface on the hub').not.toHaveProperty('cooccurrence');
    expect(data, 'cooccurrence row must not surface on the hub').not.toHaveProperty('because_you_watched');
  });

  test('trending section is an array (empty is OK on a fresh dev box)', async ({ request }) => {
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    const hubR = await request.get('/api/v1/hub', {
      headers: { Authorization: `Bearer ${token}` },
    });
    const { data } = await hubR.json();

    expect(Array.isArray(data.trending), 'trending must be an array').toBe(true);
    // Don't assert non-empty — a fresh dev box with no recent watch
    // history legitimately has nothing trending. The contract is the
    // shape, not the population.
    if (Array.isArray(data.trending) && data.trending.length > 0) {
      const first = data.trending[0];
      expect(first, 'trending items must have id + title').toHaveProperty('id');
      expect(first, 'trending items must have id + title').toHaveProperty('title');
    }
  });
});
