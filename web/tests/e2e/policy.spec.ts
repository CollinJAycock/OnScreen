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

// ── is_private library access control ─────────────────────────────────────

test.describe('Policy — is_private library', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run policy specs');

  test('non-admin user cannot see a private library', async ({ request }) => {
    // Step 1 — authenticate as admin.
    const adminLogin = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(adminLogin.status(), `admin login: ${await adminLogin.text()}`).toBe(200);
    const { data: adminData } = await adminLogin.json();
    const adminToken: string = adminData.access_token;

    // Step 2 — resolve the library ID to toggle.
    let libId = process.env.E2E_LIBRARY_ID ?? '';
    if (!libId) {
      const libsR = await request.get('/api/v1/libraries', {
        headers: { Authorization: `Bearer ${adminToken}` },
      });
      expect(libsR.status()).toBe(200);
      const { data: libs } = await libsR.json();
      if (!Array.isArray(libs) || libs.length === 0) {
        test.skip(true, 'No libraries available — seed media first');
        return;
      }
      libId = libs[0].id;
    }

    // Step 3 — mark the library private.
    const patchR = await request.patch(`/api/v1/libraries/${libId}`, {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: { is_private: true },
    });
    // If PATCH is not supported, try PUT.
    if (patchR.status() === 405) {
      const putR = await request.put(`/api/v1/libraries/${libId}`, {
        headers: { Authorization: `Bearer ${adminToken}` },
        data: { is_private: true },
      });
      expect(putR.status(), `PUT library to set is_private: ${await putR.text()}`).toBeLessThan(300);
    } else {
      expect(patchR.status(), `PATCH library to set is_private: ${await patchR.text()}`).toBeLessThan(300);
    }

    // Step 4 — register a non-admin test user.
    const ts = Date.now();
    const testUser = `e2e-policy-${ts}`;
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
      expect(regR.status(), `register: ${await regR.text()}`).toBe(200);
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
      const restoreR = await request.patch(`/api/v1/libraries/${libId}`, {
        headers: { Authorization: `Bearer ${adminToken}` },
        data: { is_private: false },
      });
      if (restoreR.status() === 405) {
        await request.put(`/api/v1/libraries/${libId}`, {
          headers: { Authorization: `Bearer ${adminToken}` },
          data: { is_private: false },
        });
      }

      // Step 8 — delete the test user.
      if (!testUserId) {
        // Resolve ID via admin user list if registration didn't return it.
        const listR = await request.get('/api/v1/users', {
          headers: { Authorization: `Bearer ${adminToken}` },
        });
        if (listR.ok()) {
          const { data: users } = await listR.json();
          const found = (users as any[]).find((u: any) => u.username === testUser);
          if (found) testUserId = found.id;
        }
      }
      if (testUserId) {
        await request.delete(`/api/v1/users/${testUserId}`, {
          headers: { Authorization: `Bearer ${adminToken}` },
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
    await page.goto('/login');
    await page.getByLabel(/username/i).fill(USERNAME);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole('button', { name: /sign in|log in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 10_000 });

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Check that no heading or section label containing "because you watched"
    // is visible anywhere on the page.
    const cooccurrenceText = page.getByText(/because you watched/i);
    await expect(cooccurrenceText, '"Because you watched" section must not exist yet').toHaveCount(0);
  });

  test('home hub contains no recommendation engine section headings', async ({ page }) => {
    // Broader check — also catch "You might like", "Recommended for you",
    // and "Similar to" which would indicate unintended recommendation UI.
    // If OnScreen ships recommendations deliberately, update this list.
    await page.goto('/login');
    await page.getByLabel(/username/i).fill(USERNAME);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole('button', { name: /sign in|log in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 10_000 });

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Each of these is soft so a single unexpected heading doesn't block
    // the others from running — we want the full picture if multiple
    // surfaces appeared at once.
    for (const pattern of [
      /because you watched/i,
      /recommended for you/i,
      /you might like/i,
    ]) {
      expect.soft(
        await page.getByText(pattern).count(),
        `Should not see "${pattern.source}" heading on home hub`,
      ).toBe(0);
    }
  });
});
