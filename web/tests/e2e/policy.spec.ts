// Per-user access policy E2E — covers the two policy rows in the v2.1
// test plan that need real server behavior to verify:
//
//   1. is_private library  — admin marks a library private; a freshly-
//      registered non-admin user must NOT see it in GET /api/v1/libraries.
//
//   2. Cooccurrence absence — the home page / hub must not show a
//      "Because you watched …" recommendation section (not yet shipped).
//
// Both blocks are gated on E2E_PASSWORD. The is_private block additionally
// requires E2E_LIBRARY_ID or falls back to the first library in the list.
// It restores the library to public after running, regardless of outcome.
//
// Required env:
//   E2E_USERNAME      OnScreen username (default 'admin')
//   E2E_PASSWORD      OnScreen password — required for all blocks
//
// Optional env:
//   E2E_LIBRARY_ID    UUID of the library to toggle private for the test
//                     (default: first library returned by /api/v1/libraries)

import { test, expect } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

// Module-scope cache for the admin token so the whole policy spec file
// (multiple tests × N browsers, each previously doing its own admin login)
// only triggers ONE auth roundtrip per worker. The dev server's
// /api/v1/auth/* rate limiter trips when the suite cumulatively exceeds
// its threshold; caching keeps us under it.
let _adminToken = '';
async function adminToken(request: import('@playwright/test').APIRequestContext): Promise<string> {
  if (_adminToken) return _adminToken;
  const r = await request.post('/api/v1/auth/login', {
    data: { username: USERNAME, password: PASSWORD },
  });
  if (!r.ok()) return '';
  const { data } = await r.json();
  _adminToken = data.access_token;
  return _adminToken;
}

// ── is_private library access control ─────────────────────────────────────

test.describe('Policy — is_private library', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run policy specs');

  test('non-admin user cannot see a private library', async ({ request }) => {
    // Step 1 — authenticate as admin (cached at module scope to share
    // across tests / browser projects).
    const adminTok = await adminToken(request);
    expect(adminTok, 'admin login failed').toBeTruthy();

    // Step 2 — resolve the library ID to toggle.
    let libId = process.env.E2E_LIBRARY_ID ?? '';
    if (!libId) {
      const libsR = await request.get('/api/v1/libraries', {
        headers: { Authorization: `Bearer ${adminTok}` },
      });
      expect(libsR.status()).toBe(200);
      const { data: libs } = await libsR.json();
      if (!Array.isArray(libs) || libs.length === 0) {
        test.skip(true, 'No libraries available — seed media first');
        return;
      }
      libId = libs[0].id;
    }

    // Step 3 — mark the library private. PATCH supports partial-body
    // updates (every field is optional and falls back to current state),
    // so we can send just the flag we want to flip.
    const patchR = await request.patch(`/api/v1/libraries/${libId}`, {
      headers: { Authorization: `Bearer ${adminTok}` },
      data: { is_private: true },
    });
    expect(patchR.status(), `PATCH library to set is_private: ${await patchR.text()}`).toBeLessThan(300);

    // Step 4 — register a non-admin test user. Username must be 2-32
    // chars and alphanumeric+underscore only (no hyphens).
    const ts = Date.now();
    const testUser = `e2e_policy_${ts}`;
    const testPass = `PoliCy${ts}!`;
    let testUserId = '';

    try {
      const regR = await request.post('/api/v1/auth/register', {
        data: { username: testUser, password: testPass, email: `${testUser}@example.invalid` },
      });
      if (regR.status() === 403 || regR.status() === 404) {
        test.skip(true, 'self-registration disabled — cannot create non-admin test user');
        return;
      }
      // Some servers return 200, others 201 Created — both are success.
      expect([200, 201], `register: ${await regR.text()}`).toContain(regR.status());
      const { data: regData } = await regR.json();
      testUserId = regData.id ?? '';

      // Step 5 — log in as the new non-admin user.
      const userLogin = await request.post('/api/v1/auth/login', {
        data: { username: testUser, password: testPass },
      });
      expect(userLogin.status()).toBe(200);
      const { data: userData } = await userLogin.json();
      const userToken: string = userData.access_token;

      // Step 6 — the private library must NOT appear in the user's library list.
      const userLibsR = await request.get('/api/v1/libraries', {
        headers: { Authorization: `Bearer ${userToken}` },
      });
      expect(userLibsR.status()).toBe(200);
      const { data: userLibs } = await userLibsR.json();
      const found = Array.isArray(userLibs) && userLibs.some((l: any) => l.id === libId);
      expect(found, `Private library ${libId} must NOT be visible to non-admin user`).toBe(false);
    } finally {
      // Step 7 — restore library to public + delete the throwaway user.
      await request
        .patch(`/api/v1/libraries/${libId}`, {
          headers: { Authorization: `Bearer ${adminTok}` },
          data: { is_private: false },
        })
        .catch(() => {});
      if (!testUserId) {
        // Resolve ID via admin user list if registration didn't return it.
        const listR = await request.get('/api/v1/users', {
          headers: { Authorization: `Bearer ${adminTok}` },
        });
        if (listR.ok()) {
          const { data: users } = await listR.json();
          const found = (users as any[]).find((u: any) => u.username === testUser);
          if (found) testUserId = found.id;
        }
      }
      if (testUserId) {
        await request
          .delete(`/api/v1/users/${testUserId}`, {
            headers: { Authorization: `Bearer ${adminTok}` },
          })
          .catch(() => {});
      }
    }
  });
});

// ── Cooccurrence absence ───────────────────────────────────────────────────

test.describe('Policy — cooccurrence absence', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run cooccurrence specs');

  test('home page does not show a "Because you watched" section', async ({ page }) => {
    // "Cooccurrence" recommendations ("Because you watched X") are not yet
    // shipped. This test guards against accidentally enabling the UI surface.
    // If the feature ships intentionally, update the test plan and remove
    // this check.
    await page.goto('/login', { waitUntil: 'domcontentloaded' });
    await page.getByLabel(/username/i).fill(USERNAME);
    await page.getByLabel(/password/i).fill(PASSWORD);
    // Use button[type="submit"] — the page may also have a "Sign in
    // with LDAP" toggle button whose accessible name matches /sign in/i.
    await page.locator('button[type="submit"]').first().click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 10_000 });

    // Use 'domcontentloaded' — the notification SSE stream stays open and
    // 'networkidle' would hang until the test timeout. Then poll briefly
    // to give hub sections a chance to render.
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(2000);

    // Check that no heading or section label containing "because you watched"
    // is visible anywhere on the page. This wording is the cooccurrence-
    // specific marker — generic "trending" / "popular" sections are fine.
    const cooccurrenceText = page.getByText(/because you watched/i);
    await expect(cooccurrenceText, '"Because you watched" section must not exist yet').toHaveCount(0);
  });
});

// ── Auto-grant template ───────────────────────────────────────────────────

test.describe('Policy — auto-grant template', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run auto-grant specs');

  test('library marked auto_grant_new_users + is_private appears on new users\' library list', async ({ request }) => {
    // Sets up a private library with auto-grant ON, registers a new
    // non-admin user via the admin path, asserts that user's library
    // list includes the auto-granted library (private libraries don't
    // appear without explicit grant — auto-grant IS the explicit
    // grant). Cleans up library back to public + deletes test user.
    const adminTok = await adminToken(request);
    expect(adminTok).toBeTruthy();

    // Resolve the library to use as the auto-grant target.
    let libId = process.env.E2E_LIBRARY_ID ?? '';
    if (!libId) {
      const r = await request.get('/api/v1/libraries', {
        headers: { Authorization: `Bearer ${adminTok}` },
      });
      const { data: libs } = await r.json();
      if (!Array.isArray(libs) || libs.length === 0) {
        test.skip(true, 'No libraries available — seed media first');
        return;
      }
      libId = libs[0].id;
    }

    // Capture original is_private + auto_grant_new_users so the finally
    // block can restore them. PATCH supports partial updates so we only
    // need the two flags we're flipping (and reading back).
    const getR = await request.get(`/api/v1/libraries/${libId}`, {
      headers: { Authorization: `Bearer ${adminTok}` },
    });
    expect(getR.status()).toBe(200);
    const original = (await getR.json()).data as { is_private?: boolean; auto_grant_new_users?: boolean };

    // Mark library private + auto-grant ON. Partial PATCH — no need
    // to re-send name / scan_paths / etc.
    const patchR = await request.patch(`/api/v1/libraries/${libId}`, {
      headers: { Authorization: `Bearer ${adminTok}` },
      data: { is_private: true, auto_grant_new_users: true },
    });
    expect(patchR.status(), `set is_private+auto_grant: ${await patchR.text()}`).toBeLessThan(300);

    const ts = Date.now();
    const tempUser = `e2e_autogrant_${ts}`;
    const tempPass = `AutoGrant${ts}!`;
    let tempUserId = '';

    try {
      // Register the new user — auto-grant must fire as part of the
      // user-creation path, granting the just-flipped library.
      const regR = await request.post('/api/v1/auth/register', {
        headers: { Authorization: `Bearer ${adminTok}` },
        data: { username: tempUser, password: tempPass, email: `${tempUser}@example.invalid` },
      });
      expect([200, 201], `register: ${await regR.text()}`).toContain(regR.status());
      tempUserId = (await regR.json()).data.id;

      // Log in as that user, fetch their visible libraries.
      const userLogin = await request.post('/api/v1/auth/login', {
        data: { username: tempUser, password: tempPass },
      });
      expect(userLogin.status()).toBe(200);
      const userTok = (await userLogin.json()).data.access_token;

      const userLibsR = await request.get('/api/v1/libraries', {
        headers: { Authorization: `Bearer ${userTok}` },
      });
      expect(userLibsR.status()).toBe(200);
      const { data: userLibs } = await userLibsR.json();
      const found = Array.isArray(userLibs) && userLibs.some((l: any) => l.id === libId);
      expect(
        found,
        `auto_grant_new_users library ${libId} must appear in the new user's library list (libs returned: ${(userLibs as any[])?.map((l) => l.id).join(', ')})`,
      ).toBe(true);
    } finally {
      // Restore the two flags we flipped. Partial PATCH; no need to
      // touch name / scan_paths.
      await request
        .patch(`/api/v1/libraries/${libId}`, {
          headers: { Authorization: `Bearer ${adminTok}` },
          data: {
            is_private: original.is_private ?? false,
            auto_grant_new_users: original.auto_grant_new_users ?? false,
          },
        })
        .catch(() => {});
      if (tempUserId) {
        await request
          .delete(`/api/v1/users/${tempUserId}`, {
            headers: { Authorization: `Bearer ${adminTok}` },
          })
          .catch(() => {});
      }
    }
  });
});

// ── View-as middleware ────────────────────────────────────────────────────

test.describe('Policy — view-as', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run view-as specs');

  test('admin can GET ?view_as=<user>; non-admin gets 403; POST view_as gets 403', async ({ request }) => {
    // The view-as middleware enforces three boundaries: must be admin
    // caller, GET only (no state mutation through the impersonation
    // surface), target user must exist. Each is asserted explicitly.
    const adminTok = await adminToken(request);
    expect(adminTok).toBeTruthy();

    // Need a non-admin user to view-as. Create a throwaway one.
    const ts = Date.now();
    const tempUser = `e2e_viewas_${ts}`;
    const tempPass = `ViewAs${ts}!`;
    const regR = await request.post('/api/v1/auth/register', {
      headers: { Authorization: `Bearer ${adminTok}` },
      data: { username: tempUser, password: tempPass, email: `${tempUser}@example.invalid` },
    });
    expect([200, 201], `register: ${await regR.text()}`).toContain(regR.status());
    const tempUserId = (await regR.json()).data.id;

    try {
      // (1) admin GET with view_as → 200 (impersonation accepted).
      const adminViewAsR = await request.get(`/api/v1/hub?view_as=${tempUserId}`, {
        headers: { Authorization: `Bearer ${adminTok}` },
      });
      expect(
        adminViewAsR.status(),
        `admin must be allowed to view_as a real user (got ${adminViewAsR.status()}: ${await adminViewAsR.text()})`,
      ).toBe(200);

      // (2) non-admin GET with view_as → 403 (only admins can impersonate).
      const userLogin = await request.post('/api/v1/auth/login', {
        data: { username: tempUser, password: tempPass },
      });
      expect(userLogin.status()).toBe(200);
      const userTok = (await userLogin.json()).data.access_token;
      const userViewAsR = await request.get(`/api/v1/hub?view_as=${tempUserId}`, {
        headers: { Authorization: `Bearer ${userTok}` },
      });
      expect(
        userViewAsR.status(),
        `non-admin view_as must be 403 (got ${userViewAsR.status()})`,
      ).toBe(403);

      // (3) admin POST with view_as → 403 (GET-only). Pick an
      // endpoint that accepts POST without auth to isolate the
      // middleware behavior; /api/v1/auth/login fits — it doesn't
      // need a body shape we care about, just routes through the
      // middleware. The middleware should reject before the handler
      // even runs. Adjust: actually use any /api/v1 POST under the
      // authed router; the unauth /auth/login is OUTSIDE the
      // view_as middleware group. Use PATCH /settings (admin POST-
      // shape) instead.
      const adminPostViewAsR = await request.patch(`/api/v1/settings?view_as=${tempUserId}`, {
        headers: { Authorization: `Bearer ${adminTok}` },
        data: {},
      });
      expect(
        adminPostViewAsR.status(),
        `view_as on a non-GET request must be rejected (got ${adminPostViewAsR.status()}: ${await adminPostViewAsR.text()})`,
      ).toBe(403);

      // (4) admin GET with bogus view_as uuid → 4xx (target lookup
      // fails). Specifically the middleware returns 400 (bad uuid)
      // or 500 (lookup failed) per the auth.go branches; either is
      // acceptable — a 200 is the failure mode.
      const bogusR = await request.get(
        `/api/v1/hub?view_as=00000000-0000-0000-0000-000000000000`,
        { headers: { Authorization: `Bearer ${adminTok}` } },
      );
      expect.soft(
        [400, 404, 500],
        `view_as with bogus uuid must reject (got ${bogusR.status()})`,
      ).toContain(bogusR.status());
      expect(bogusR.status(), 'view_as with bogus uuid must NOT return 200').not.toBe(200);
    } finally {
      await request
        .delete(`/api/v1/users/${tempUserId}`, {
          headers: { Authorization: `Bearer ${adminTok}` },
        })
        .catch(() => {});
    }
  });
});

// ── Content-rating ceiling ────────────────────────────────────────────────

test.describe('Policy — content-rating ceiling', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run content-rating specs');

  test('user with PG-13 ceiling does not see an R-rated item via search', async ({ request }) => {
    // Locks the content-rating ceiling on /search. Needs an item whose
    // content_rating is already > PG-13 (e.g. R, NC-17, TV-MA, or NR
    // which the SQL function ranks as 4 — most restrictive). PATCH
    // /items/{id} only accepts title/summary/taken_at by design, so we
    // can't set the rating from the test — find an existing rated item,
    // or skip cleanly when the dev library has none.
    const adminTok = await adminToken(request);
    expect(adminTok).toBeTruthy();

    const libsR = await request.get('/api/v1/libraries', {
      headers: { Authorization: `Bearer ${adminTok}` },
    });
    const { data: libs } = await libsR.json();
    const movieLib = (libs as any[])?.find((l) => l.type === 'movie');
    if (!movieLib) {
      test.skip(true, 'No movie library available');
      return;
    }
    // Pull a generous page so we have a fair chance of hitting a
    // rated item if any exist.
    const itemsR = await request.get(
      `/api/v1/libraries/${movieLib.id}/items?limit=200`,
      { headers: { Authorization: `Bearer ${adminTok}` } },
    );
    const { data: items } = await itemsR.json();
    // Look for an item with a rating that ranks > PG-13 (SQL ranks: G/TV-Y/TV-G=0,
    // PG/TV-Y7/TV-PG=1, PG-13/TV-14=2, R/NC-17/TV-MA=3, NR/empty=4).
    const overPG13 = new Set(['R', 'NC-17', 'TV-MA']);
    const targetItem = (items as any[])?.find((i) =>
      overPG13.has(((i.content_rating ?? '') as string).toUpperCase()),
    );
    if (!targetItem) {
      test.skip(
        true,
        'No item rated above PG-13 in the library — content-rating ceiling test needs a rated fixture (and PATCH /items/{id} intentionally cannot set content_rating, so we cannot fabricate one)',
      );
      return;
    }

    const ts = Date.now();
    const tempUser = `e2e_rating_${ts}`;
    const tempPass = `RatePass${ts}!`;
    let tempUserId = '';

    try {
      const regR = await request.post('/api/v1/auth/register', {
        headers: { Authorization: `Bearer ${adminTok}` },
        data: { username: tempUser, password: tempPass, email: `${tempUser}@example.invalid` },
      });
      expect([200, 201], `register: ${await regR.text()}`).toContain(regR.status());
      tempUserId = (await regR.json()).data.id;

      // Set content-rating ceiling to PG-13 via admin endpoint. Field
      // name is `max_content_rating` (per the SetContentRating handler);
      // sending `content_rating` is silently ignored.
      const ceilingR = await request.put(`/api/v1/users/${tempUserId}/content-rating`, {
        headers: { Authorization: `Bearer ${adminTok}` },
        data: { max_content_rating: 'PG-13' },
      });
      expect(
        ceilingR.status(),
        `set user content_rating ceiling: ${await ceilingR.text()}`,
      ).toBeLessThan(300);

      // Log in as the restricted user, fetch the movie library items.
      const userLogin = await request.post('/api/v1/auth/login', {
        data: { username: tempUser, password: tempPass },
      });
      expect(userLogin.status()).toBe(200);
      const userTok = (await userLogin.json()).data.access_token;

      // Search is the canonical user-facing surface for finding items
      // by name; if a kid profile can't find an R-rated item via title
      // search, the ceiling boundary holds for the discovery path that
      // matters most. Searching by an exact title fragment ought to
      // hit the item if it were visible.
      const titleFragment = (targetItem.title as string).split(/\s+/)[0];
      const searchR = await request.get(
        `/api/v1/search?q=${encodeURIComponent(titleFragment)}`,
        { headers: { Authorization: `Bearer ${userTok}` } },
      );
      expect(searchR.status()).toBe(200);
      const searchBody = await searchR.json();
      // Search results live under data.items, data.results, or data
      // depending on the response shape — flatten everything to find
      // any object with an id that matches.
      const flat: any[] = [];
      const stack: any[] = [searchBody];
      while (stack.length) {
        const cur = stack.pop();
        if (Array.isArray(cur)) {
          for (const v of cur) stack.push(v);
        } else if (cur && typeof cur === 'object') {
          if (cur.id) flat.push(cur);
          for (const v of Object.values(cur)) stack.push(v);
        }
      }
      const found = flat.some((x: any) => x?.id === targetItem.id);
      expect(
        found,
        `R-rated item "${targetItem.title}" (${targetItem.id}) must NOT appear in search results for a PG-13-ceiling user`,
      ).toBe(false);

      // Hub check: assert the same item doesn't appear in any /hub
      // section either. The content_rating_rank() SQL function + the
      // narg('max_rating_rank') filter is wired into ListTrending +
      // ContinueWatching + RecentlyAdded queries already, so this
      // should hold. If a hub section ever leaks, we want to know.
      const hubR = await request.get('/api/v1/hub', {
        headers: { Authorization: `Bearer ${userTok}` },
      });
      expect(hubR.status()).toBe(200);
      const { data: hub } = await hubR.json();
      let leakedIn: string | null = null;
      for (const sectionKey of Object.keys(hub || {})) {
        const sectionItems = hub[sectionKey];
        if (!Array.isArray(sectionItems)) continue;
        if (sectionItems.some((i: any) => i?.id === targetItem.id)) {
          leakedIn = sectionKey;
          break;
        }
      }
      expect(
        leakedIn,
        `over-PG-13 item "${targetItem.title}" (${targetItem.id}) leaked into /hub section "${leakedIn}" for a PG-13-ceiling user`,
      ).toBeNull();
    } finally {
      if (tempUserId) {
        await request
          .delete(`/api/v1/users/${tempUserId}`, {
            headers: { Authorization: `Bearer ${adminTok}` },
          })
          .catch(() => {});
      }
    }
  });
});
