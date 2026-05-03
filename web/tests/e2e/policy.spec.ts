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

    // Step 3 — mark the library private. The PATCH endpoint requires
    // a full body (Name, ScanPaths, etc.) — sending only is_private
    // returns 500 because empty Name fails validation. Fetch the
    // current row, mutate is_private, and send the whole shape back.
    const getR = await request.get(`/api/v1/libraries/${libId}`, {
      headers: { Authorization: `Bearer ${adminTok}` },
    });
    expect(getR.status(), `GET library: ${await getR.text()}`).toBe(200);
    const { data: current } = await getR.json();
    const patchR = await request.patch(`/api/v1/libraries/${libId}`, {
      headers: { Authorization: `Bearer ${adminTok}` },
      data: {
        name: current.name,
        scan_paths: current.scan_paths,
        agent: current.agent,
        language: current.language,
        scan_interval_minutes: current.scan_interval_minutes,
        metadata_refresh_interval_ns: current.metadata_refresh_interval_ns,
        is_private: true,
      },
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
      // Step 7 — restore the library to public regardless of test outcome.
      // Same full-body shape as the set-private call above.
      const getRestoreR = await request.get(`/api/v1/libraries/${libId}`, {
        headers: { Authorization: `Bearer ${adminTok}` },
      });
      if (getRestoreR.ok()) {
        const { data: cur } = await getRestoreR.json();
        await request.patch(`/api/v1/libraries/${libId}`, {
          headers: { Authorization: `Bearer ${adminTok}` },
          data: {
            name: cur.name,
            scan_paths: cur.scan_paths,
            agent: cur.agent,
            language: cur.language,
            scan_interval_minutes: cur.scan_interval_minutes,
            metadata_refresh_interval_ns: cur.metadata_refresh_interval_ns,
            is_private: false,
          },
        });
      }

      // Step 8 — delete the test user.
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
        await request.delete(`/api/v1/users/${testUserId}`, {
          headers: { Authorization: `Bearer ${adminTok}` },
        });
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
    await page.getByRole('button', { name: /sign in|log in/i }).click();
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
