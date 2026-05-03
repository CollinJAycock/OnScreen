// Browser-driven auth-provider flows. Complements `auth-providers.spec.ts`
// (HTTP contract only) by walking the actual SSO redirect → IdP login form
// → callback → logged-in-on-OnScreen round-trip in a real browser. This
// is what catches breakage in the bits that the contract spec can't see:
// Keycloak's login form silently changes shape, OnScreen's callback
// handler crashes on the IdP's claim payload, the JIT-provisioning step
// drops the user on the floor, etc.
//
// Required: docker/docker-compose.idp.yml stack up + the same Keycloak
// realm + clients + user that `auth-providers.spec.ts` runs against.
//
// Required env (per provider, all optional):
//
//   E2E_OIDC_BROWSER_ENABLED       set to anything non-empty to run OIDC browser flow
//   E2E_OIDC_TEST_USERNAME         Keycloak username (default 'testuser')
//   E2E_OIDC_TEST_PASSWORD         Keycloak password (default 'testpass')
//
//   E2E_SAML_BROWSER_ENABLED       set to anything non-empty to run SAML browser flow
//   E2E_SAML_TEST_USERNAME         Keycloak username (default 'testuser')
//   E2E_SAML_TEST_PASSWORD         Keycloak password (default 'testpass')
//
//   E2E_FORGOT_PASSWORD_ENABLED    set to anything non-empty to run forgot-password
//                                  (Mailpit must be reachable at MAILPIT_URL)
//   MAILPIT_URL                    Mailpit web/API base (default 'http://localhost:8025')
//   E2E_FORGOT_PASSWORD_USERNAME   the OnScreen account whose email is sent (default E2E_USERNAME)
//   E2E_FORGOT_PASSWORD_EMAIL      that account's email (default '<username>@onscreen.local')

import { test, expect, type Page } from '@playwright/test';

const MAILPIT_URL = process.env.MAILPIT_URL ?? 'http://localhost:8025';

// Helper: fill the Keycloak login form. Keycloak's form is a server-
// rendered template at the IdP origin (not OnScreen's), so by the time
// we get here the page URL is `localhost:8080` and we need to interact
// with Keycloak's input fields specifically.
async function submitKeycloakLogin(page: Page, username: string, password: string): Promise<void> {
  await expect(page).toHaveURL(/localhost:8080/, { timeout: 15_000 });
  // Keycloak's stock theme uses id="username" / id="password".
  await page.locator('#username').fill(username);
  await page.locator('#password').fill(password);
  await page.locator('input[type="submit"], button[type="submit"]').first().click();
}

// ── OIDC full browser flow ─────────────────────────────────────────────────

test.describe('Auth providers — OIDC browser flow', () => {
  test.skip(!process.env.E2E_OIDC_BROWSER_ENABLED, 'set E2E_OIDC_BROWSER_ENABLED to run');

  const username = process.env.E2E_OIDC_TEST_USERNAME ?? 'testuser';
  const password = process.env.E2E_OIDC_TEST_PASSWORD ?? 'testpass';

  test('SSO button → Keycloak login → JIT-provisioned + logged in on OnScreen', async ({ page }) => {
    await page.goto('/login', { waitUntil: 'domcontentloaded' });

    // The OIDC SSO button on /login navigates to /api/v1/auth/oidc which
    // redirects to Keycloak. Click it and wait for the URL to land on
    // the Keycloak realm.
    await Promise.all([
      page.waitForURL(/localhost:8080.*openid-connect\/auth/, { timeout: 15_000 }),
      page.locator('button.sso-btn').first().click(),
    ]);

    await submitKeycloakLogin(page, username, password);

    // Keycloak redirects to /api/v1/auth/oidc/callback?code=…, OnScreen
    // exchanges the code, sets the session cookie, then SvelteKit
    // navigates to /. We assert URL no longer matches /login.
    await expect(page, 'OnScreen must navigate away from /login after OIDC callback').not.toHaveURL(
      /\/login/,
      { timeout: 20_000 },
    );

    // Confirm a session cookie is actually set (SameSite=Lax HttpOnly).
    const cookies = await page.context().cookies();
    expect(cookies.length, 'OIDC callback must set at least one cookie').toBeGreaterThan(0);

    // Round-trip: hit an authenticated API endpoint with the browser's
    // cookie context to prove the session is fully usable.
    const r = await page.request.get('/api/v1/users/me/preferences');
    expect(r.status(), 'JIT-provisioned OIDC user must be able to read /users/me').toBe(200);
  });
});

// ── SAML full browser flow ─────────────────────────────────────────────────

test.describe('Auth providers — SAML browser flow', () => {
  test.skip(!process.env.E2E_SAML_BROWSER_ENABLED, 'set E2E_SAML_BROWSER_ENABLED to run');

  const username = process.env.E2E_SAML_TEST_USERNAME ?? 'testuser';
  const password = process.env.E2E_SAML_TEST_PASSWORD ?? 'testpass';

  test('SSO button → Keycloak SAML login → ACS POST → logged in on OnScreen', async ({ page }) => {
    await page.goto('/login', { waitUntil: 'domcontentloaded' });

    // Two SSO buttons on the page (OIDC + SAML); click the second one
    // (SAML). The order is fixed in `web/src/routes/login/+page.svelte`.
    // Wait for Keycloak's SAML auth URL.
    await Promise.all([
      page.waitForURL(/localhost:8080.*\/protocol\/saml/, { timeout: 15_000 }),
      page.locator('button.sso-btn').nth(1).click(),
    ]);

    await submitKeycloakLogin(page, username, password);

    // Keycloak POSTs the signed SAML Response to OnScreen's ACS
    // (`/api/v1/auth/saml/acs`), which validates the signature, JIT-
    // provisions the user if new, sets the session cookie, then
    // navigates back to /.
    await expect(page, 'OnScreen must navigate away from /login after SAML ACS').not.toHaveURL(
      /\/login/,
      { timeout: 20_000 },
    );

    const cookies = await page.context().cookies();
    expect(cookies.length, 'SAML ACS must set at least one cookie').toBeGreaterThan(0);

    const r = await page.request.get('/api/v1/users/me/preferences');
    expect(r.status(), 'JIT-provisioned SAML user must be able to read /users/me').toBe(200);
  });
});

// ── Forgot-password via Mailpit ────────────────────────────────────────────

test.describe('Auth providers — forgot-password via Mailpit', () => {
  test.skip(
    !process.env.E2E_FORGOT_PASSWORD_ENABLED,
    'set E2E_FORGOT_PASSWORD_ENABLED to run (requires Mailpit at MAILPIT_URL + admin creds for user creation)',
  );

  const ADMIN_USERNAME = process.env.E2E_USERNAME ?? 'admin';
  const ADMIN_PASSWORD = process.env.E2E_PASSWORD ?? '';

  test('POST /auth/forgot-password → Mailpit receives a reset email with a single-use token, full consume + login round-trip', async ({
    request,
  }) => {
    // The dev OnScreen account often has no email set, and self-register
    // is admin-gated, so we create a throwaway user with a known email
    // via the admin path. We also actually consume the reset token at
    // the end (set a new password, log in with it) — this works because
    // the user is throwaway and we delete it in the finally block, so
    // the test has no collateral effect on the suite.
    if (!ADMIN_PASSWORD) {
      test.skip(true, 'set E2E_PASSWORD so the test can register a throwaway user via /auth/register');
      return;
    }

    const adminLogin = await request.post('/api/v1/auth/login', {
      data: { username: ADMIN_USERNAME, password: ADMIN_PASSWORD },
    });
    expect(adminLogin.status(), `admin login: ${await adminLogin.text()}`).toBe(200);
    const adminToken = (await adminLogin.json()).data.access_token as string;

    const ts = Date.now();
    const tempUser = `forgotpw_${ts}`;
    const tempEmail = `${tempUser}@onscreen.local`;
    const tempInitialPass = `Initial${ts}!`;
    const tempNewPass = `Reset${ts}!`;

    // Snapshot Mailpit total BEFORE so the post-trigger poll can ignore
    // the existing inbox and only watch for the new arrival.
    const beforeR = await request.get(`${MAILPIT_URL}/api/v1/messages?limit=1`);
    expect(
      beforeR.status(),
      `Mailpit must be reachable at ${MAILPIT_URL} (got ${beforeR.status()})`,
    ).toBe(200);
    const beforeTotal: number = (await beforeR.json()).total ?? 0;

    // Register the throwaway user via admin token.
    const regR = await request.post('/api/v1/auth/register', {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: { username: tempUser, password: tempInitialPass, email: tempEmail },
    });
    expect([200, 201], `register: ${await regR.text()}`).toContain(regR.status());
    const tempUserId = (await regR.json()).data.id as string;

    try {
      // Trigger the reset email.
      const fpR = await request.post('/api/v1/auth/forgot-password', {
        data: { email: tempEmail },
      });
      expect(
        [200, 202, 204],
        `forgot-password POST should accept the request (got ${fpR.status()}: ${await fpR.text()})`,
      ).toContain(fpR.status());

      // Poll Mailpit for the new message addressed to the temp user.
      let messageID = '';
      await expect
        .poll(
          async () => {
            const r = await request.get(`${MAILPIT_URL}/api/v1/messages?limit=10`);
            if (!r.ok()) return -1;
            const j = await r.json();
            if ((j.total ?? 0) <= beforeTotal) return -1;
            for (const m of j.messages ?? []) {
              const tos = (m.To ?? []).map((t: any) => t.Address?.toLowerCase?.() ?? '');
              if (tos.includes(tempEmail.toLowerCase())) {
                messageID = m.ID;
                return 1;
              }
            }
            return 0;
          },
          { timeout: 15_000, message: 'no reset email landed in Mailpit for ' + tempEmail },
        )
        .toBe(1);

      // Pull the full message body and extract the reset link.
      const msgR = await request.get(`${MAILPIT_URL}/api/v1/message/${messageID}`);
      expect(msgR.status()).toBe(200);
      const msg = await msgR.json();
      const haystack = `${msg.HTML ?? ''}\n${msg.Text ?? ''}`;
      const tokenMatch = haystack.match(/token=([A-Za-z0-9._%-]+)/);
      expect(
        tokenMatch,
        `reset email must contain a token=… link. Body sample: ${haystack.slice(0, 400)}`,
      ).not.toBeNull();
      const resetToken = decodeURIComponent(tokenMatch![1]);

      // Negative path FIRST (the single-use security boundary): a
      // wrong-token POST must reject and must NOT consume the real
      // token from the same time window.
      const badR = await request.post('/api/v1/auth/reset-password', {
        data: { token: 'definitely-not-a-valid-token-xxxx', password: 'irrelevant-1234' },
      });
      expect(
        [400, 401, 403],
        `wrong-token reset must be rejected (got ${badR.status()}: ${await badR.text()})`,
      ).toContain(badR.status());

      // Happy path: consume the real token + set new password.
      const consumeR = await request.post('/api/v1/auth/reset-password', {
        data: { token: resetToken, password: tempNewPass },
      });
      expect(
        [200, 204],
        `reset-password with the real token must succeed (got ${consumeR.status()}: ${await consumeR.text()})`,
      ).toContain(consumeR.status());

      // Verify: log in with the new password.
      const newLoginR = await request.post('/api/v1/auth/login', {
        data: { username: tempUser, password: tempNewPass },
      });
      expect(
        newLoginR.status(),
        `login with the new password must succeed (got ${newLoginR.status()}: ${await newLoginR.text()})`,
      ).toBe(200);

      // Replay protection: the same reset token must NOT work a second
      // time (single-use enforcement is the actual security boundary).
      const replayR = await request.post('/api/v1/auth/reset-password', {
        data: { token: resetToken, password: 'second-attempt-pass-1234' },
      });
      expect(
        [400, 401, 403],
        `reset token must be single-use; replay returned ${replayR.status()}`,
      ).toContain(replayR.status());
    } finally {
      // Clean up the throwaway user so reruns don't accumulate junk.
      await request
        .delete(`/api/v1/users/${tempUserId}`, {
          headers: { Authorization: `Bearer ${adminToken}` },
        })
        .catch(() => {});
    }
  });
});
