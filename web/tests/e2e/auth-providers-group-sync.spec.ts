// Group sync — locks the security boundary that maps IdP group membership
// to OnScreen's `is_admin` flag. Two flows:
//
//   1. OIDC: Keycloak's groups claim mapper adds a `groups` array to the
//      ID token; OnScreen reads `oidc.groups_claim` from settings and
//      sets is_admin=true if `oidc.admin_group` appears in that array.
//   2. SAML: Keycloak's group-list attribute mapper adds a `groups`
//      attribute to the assertion; OnScreen does the same lookup via
//      `saml.groups_attribute` + `saml.admin_group`.
//
// The test sets up everything itself — Keycloak group, mapper, OnScreen
// settings — so it's idempotent across runs. JIT-provisioned testuser is
// deleted from OnScreen in `afterEach` so re-running starts clean.
//
// Required env:
//   E2E_USERNAME / E2E_PASSWORD                    OnScreen admin (for setup + verification)
//   E2E_OIDC_GROUP_SYNC_ENABLED                    set to anything non-empty for the OIDC test
//   E2E_SAML_GROUP_SYNC_ENABLED                    set to anything non-empty for the SAML test
//   E2E_OIDC_TEST_USERNAME / E2E_OIDC_TEST_PASSWORD  Keycloak creds (default 'testuser'/'testpass')
//
// Required infra (already verified in `auth-providers-browser.spec.ts`):
//   docker/docker-compose.idp.yml stack up; OnScreen OIDC + SAML
//   configured to point at the realm.

import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

const KC_URL = process.env.KC_URL ?? 'http://localhost:8080';
const KC_REALM = process.env.KC_REALM ?? 'onscreen-test';
const KC_ADMIN_USER = process.env.KC_ADMIN_USER ?? 'admin';
const KC_ADMIN_PASS = process.env.KC_ADMIN_PASS ?? 'admin';

const ONSCREEN_USERNAME = process.env.E2E_USERNAME ?? 'admin';
const ONSCREEN_PASSWORD = process.env.E2E_PASSWORD ?? '';

const TEST_USER = process.env.E2E_OIDC_TEST_USERNAME ?? 'testuser';
const TEST_PASS = process.env.E2E_OIDC_TEST_PASSWORD ?? 'testpass';
const ADMIN_GROUP = 'onscreen-admins';

// ── Keycloak admin helpers ────────────────────────────────────────────────

async function kcAdminToken(req: APIRequestContext): Promise<string> {
  const r = await req.post(`${KC_URL}/realms/master/protocol/openid-connect/token`, {
    form: {
      username: KC_ADMIN_USER,
      password: KC_ADMIN_PASS,
      grant_type: 'password',
      client_id: 'admin-cli',
    },
  });
  if (!r.ok()) throw new Error(`Keycloak admin login failed: ${r.status()} ${await r.text()}`);
  const { access_token } = await r.json();
  return access_token;
}

async function ensureKeycloakGroup(req: APIRequestContext, kcToken: string, groupName: string): Promise<string> {
  const listR = await req.get(`${KC_URL}/admin/realms/${KC_REALM}/groups?search=${encodeURIComponent(groupName)}`, {
    headers: { Authorization: `Bearer ${kcToken}` },
  });
  if (listR.ok()) {
    const groups = await listR.json();
    const existing = (groups as any[]).find((g) => g.name === groupName);
    if (existing) return existing.id;
  }
  const createR = await req.post(`${KC_URL}/admin/realms/${KC_REALM}/groups`, {
    headers: { Authorization: `Bearer ${kcToken}` },
    data: { name: groupName },
  });
  if (createR.status() !== 201) throw new Error(`createGroup ${groupName}: ${createR.status()} ${await createR.text()}`);
  // Re-fetch to get the new group's id.
  const fetchR = await req.get(`${KC_URL}/admin/realms/${KC_REALM}/groups?search=${encodeURIComponent(groupName)}`, {
    headers: { Authorization: `Bearer ${kcToken}` },
  });
  return ((await fetchR.json()) as any[]).find((g) => g.name === groupName).id;
}

async function getKeycloakUserID(req: APIRequestContext, kcToken: string, username: string): Promise<string> {
  const r = await req.get(`${KC_URL}/admin/realms/${KC_REALM}/users?username=${encodeURIComponent(username)}&exact=true`, {
    headers: { Authorization: `Bearer ${kcToken}` },
  });
  if (!r.ok()) throw new Error(`getKeycloakUserID: ${r.status()} ${await r.text()}`);
  const users = (await r.json()) as any[];
  if (users.length === 0) throw new Error(`Keycloak user "${username}" not found in realm ${KC_REALM}`);
  return users[0].id;
}

async function addUserToGroup(req: APIRequestContext, kcToken: string, userID: string, groupID: string): Promise<void> {
  const r = await req.put(`${KC_URL}/admin/realms/${KC_REALM}/users/${userID}/groups/${groupID}`, {
    headers: { Authorization: `Bearer ${kcToken}` },
  });
  if (r.status() !== 204 && r.status() !== 200) {
    throw new Error(`addUserToGroup: ${r.status()} ${await r.text()}`);
  }
}

async function getKeycloakClientUUID(req: APIRequestContext, kcToken: string, clientID: string): Promise<string> {
  const r = await req.get(`${KC_URL}/admin/realms/${KC_REALM}/clients?clientId=${encodeURIComponent(clientID)}`, {
    headers: { Authorization: `Bearer ${kcToken}` },
  });
  if (!r.ok()) throw new Error(`getKeycloakClientUUID: ${r.status()}`);
  const clients = (await r.json()) as any[];
  if (clients.length === 0) throw new Error(`Keycloak client "${clientID}" not found`);
  return clients[0].id;
}

// Idempotent: lists existing protocol-mappers on the client, returns
// silently if one with the given name already exists. Otherwise POSTs.
async function ensureProtocolMapper(
  req: APIRequestContext,
  kcToken: string,
  clientUUID: string,
  mapper: any,
): Promise<void> {
  const listR = await req.get(`${KC_URL}/admin/realms/${KC_REALM}/clients/${clientUUID}/protocol-mappers/models`, {
    headers: { Authorization: `Bearer ${kcToken}` },
  });
  if (listR.ok()) {
    const existing = ((await listR.json()) as any[]).find((m) => m.name === mapper.name);
    if (existing) return;
  }
  const createR = await req.post(`${KC_URL}/admin/realms/${KC_REALM}/clients/${clientUUID}/protocol-mappers/models`, {
    headers: { Authorization: `Bearer ${kcToken}` },
    data: mapper,
  });
  if (createR.status() !== 201) {
    throw new Error(`ensureProtocolMapper ${mapper.name}: ${createR.status()} ${await createR.text()}`);
  }
}

// ── OnScreen admin helpers ────────────────────────────────────────────────

async function onscreenAdminToken(req: APIRequestContext): Promise<string> {
  const r = await req.post('/api/v1/auth/login', {
    data: { username: ONSCREEN_USERNAME, password: ONSCREEN_PASSWORD },
  });
  if (!r.ok()) throw new Error(`OnScreen admin login: ${r.status()} ${await r.text()}`);
  return (await r.json()).data.access_token;
}

async function findOnScreenUser(
  req: APIRequestContext,
  adminToken: string,
  username: string,
): Promise<{ id: string; is_admin: boolean } | null> {
  const r = await req.get('/api/v1/users', { headers: { Authorization: `Bearer ${adminToken}` } });
  if (!r.ok()) return null;
  const { data } = await r.json();
  const u = (data as any[]).find((x) => x.username === username);
  return u ? { id: u.id, is_admin: !!u.is_admin } : null;
}

async function deleteOnScreenUser(req: APIRequestContext, adminToken: string, userID: string): Promise<void> {
  await req.delete(`/api/v1/users/${userID}`, { headers: { Authorization: `Bearer ${adminToken}` } }).catch(() => {});
}

// ── Shared browser login through Keycloak ─────────────────────────────────

async function submitKeycloakLogin(page: Page, username: string, password: string): Promise<void> {
  await expect(page).toHaveURL(/localhost:8080/, { timeout: 15_000 });
  await page.locator('#username').fill(username);
  await page.locator('#password').fill(password);
  await page.locator('input[type="submit"], button[type="submit"]').first().click();
}

// ── OIDC group sync ───────────────────────────────────────────────────────

test.describe('Auth providers — OIDC admin group sync', () => {
  test.skip(!process.env.E2E_OIDC_GROUP_SYNC_ENABLED, 'set E2E_OIDC_GROUP_SYNC_ENABLED to run');
  test.skip(!ONSCREEN_PASSWORD, 'set E2E_PASSWORD');

  test('Keycloak group membership maps to is_admin=true via the groups claim', async ({ page, request }) => {
    // ── Keycloak fixtures ─────────────────────────────────────────────
    const kcToken = await kcAdminToken(request);
    const groupID = await ensureKeycloakGroup(request, kcToken, ADMIN_GROUP);
    const kcUserID = await getKeycloakUserID(request, kcToken, TEST_USER);
    await addUserToGroup(request, kcToken, kcUserID, groupID);

    // Add a "groups" claim mapper to the OIDC client. Keycloak's stock
    // group-membership mapper emits a `groups` claim in the ID token.
    const oidcClientUUID = await getKeycloakClientUUID(request, kcToken, 'onscreen-test');
    await ensureProtocolMapper(request, kcToken, oidcClientUUID, {
      name: 'groups',
      protocol: 'openid-connect',
      protocolMapper: 'oidc-group-membership-mapper',
      config: {
        'claim.name': 'groups',
        'full.path': 'false', // emit "onscreen-admins", not "/onscreen-admins"
        'id.token.claim': 'true',
        'access.token.claim': 'true',
        'userinfo.token.claim': 'true',
      },
    });

    // ── OnScreen settings: tell it where to find the groups claim ────
    const adminToken = await onscreenAdminToken(request);
    const settingsR = await request.patch('/api/v1/settings', {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: { oidc: { groups_claim: 'groups', admin_group: ADMIN_GROUP } },
    });
    expect(settingsR.status(), `settings PATCH: ${await settingsR.text()}`).toBeLessThan(300);

    // Make sure prior runs' JIT-created user doesn't carry stale state.
    const stale = await findOnScreenUser(request, adminToken, TEST_USER);
    if (stale) await deleteOnScreenUser(request, adminToken, stale.id);

    try {
      // ── Browser login flow ─────────────────────────────────────────
      await page.goto('/login', { waitUntil: 'domcontentloaded' });
      await Promise.all([
        page.waitForURL(/localhost:8080.*openid-connect\/auth/, { timeout: 15_000 }),
        page.locator('button.sso-btn').first().click(),
      ]);
      await submitKeycloakLogin(page, TEST_USER, TEST_PASS);
      await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 });

      // ── Verify: is_admin=true on the JIT-provisioned user ─────────
      const provisioned = await findOnScreenUser(request, adminToken, TEST_USER);
      expect(provisioned, `OnScreen must have JIT-created the OIDC user "${TEST_USER}"`).not.toBeNull();
      expect(
        provisioned!.is_admin,
        `OIDC group membership in "${ADMIN_GROUP}" must map to is_admin=true on the JIT user`,
      ).toBe(true);
    } finally {
      const u = await findOnScreenUser(request, adminToken, TEST_USER);
      if (u) await deleteOnScreenUser(request, adminToken, u.id);
    }
  });
});

// ── SAML group sync ───────────────────────────────────────────────────────

test.describe('Auth providers — SAML admin group sync', () => {
  test.skip(!process.env.E2E_SAML_GROUP_SYNC_ENABLED, 'set E2E_SAML_GROUP_SYNC_ENABLED to run');
  test.skip(!ONSCREEN_PASSWORD, 'set E2E_PASSWORD');

  test('Keycloak group membership maps to is_admin=true via the groups SAML attribute', async ({ page, request }) => {
    const kcToken = await kcAdminToken(request);
    const groupID = await ensureKeycloakGroup(request, kcToken, ADMIN_GROUP);
    const kcUserID = await getKeycloakUserID(request, kcToken, TEST_USER);
    await addUserToGroup(request, kcToken, kcUserID, groupID);

    const samlClientUUID = await getKeycloakClientUUID(
      request,
      kcToken,
      'http://localhost:7070/api/v1/auth/saml/metadata',
    );
    // (a) Group list attribute mapper — emits the user's group names.
    await ensureProtocolMapper(request, kcToken, samlClientUUID, {
      name: 'groups-attr',
      protocol: 'saml',
      protocolMapper: 'saml-group-membership-mapper',
      config: {
        'attribute.name': 'groups',
        'attribute.nameformat': 'Basic',
        'friendly.name': 'groups',
        single: 'false',
        'full.path': 'false',
      },
    });
    // (b) Username attribute mapper — without this, OnScreen falls back
    // to the SAML NameID, which Keycloak emits as a UUID-with-G-prefix
    // and the JIT user lands with username "G-<uuid>" rather than the
    // user's actual username.
    await ensureProtocolMapper(request, kcToken, samlClientUUID, {
      name: 'username-attr',
      protocol: 'saml',
      protocolMapper: 'saml-user-property-mapper',
      config: {
        'user.attribute': 'username',
        'attribute.name': 'Username',
        'attribute.nameformat': 'Basic',
        'friendly.name': 'username',
      },
    });
    // (c) Email attribute mapper — same reason; mirrors typical SAML SP setup.
    await ensureProtocolMapper(request, kcToken, samlClientUUID, {
      name: 'email-attr',
      protocol: 'saml',
      protocolMapper: 'saml-user-property-mapper',
      config: {
        'user.attribute': 'email',
        'attribute.name': 'Email',
        'attribute.nameformat': 'Basic',
        'friendly.name': 'email',
      },
    });

    const adminToken = await onscreenAdminToken(request);
    const settingsR = await request.patch('/api/v1/settings', {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: {
        saml: {
          username_attribute: 'Username',
          email_attribute: 'Email',
          groups_attribute: 'groups',
          admin_group: ADMIN_GROUP,
        },
      },
    });
    expect(settingsR.status(), `settings PATCH: ${await settingsR.text()}`).toBeLessThan(300);

    const stale = await findOnScreenUser(request, adminToken, TEST_USER);
    if (stale) await deleteOnScreenUser(request, adminToken, stale.id);

    try {
      await page.goto('/login', { waitUntil: 'domcontentloaded' });
      await Promise.all([
        page.waitForURL(/localhost:8080.*\/protocol\/saml/, { timeout: 15_000 }),
        page.locator('button.sso-btn').nth(1).click(),
      ]);
      await submitKeycloakLogin(page, TEST_USER, TEST_PASS);
      await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 });

      const provisioned = await findOnScreenUser(request, adminToken, TEST_USER);
      expect(provisioned, `OnScreen must have JIT-created the SAML user "${TEST_USER}"`).not.toBeNull();
      expect(
        provisioned!.is_admin,
        `SAML group membership in "${ADMIN_GROUP}" must map to is_admin=true on the JIT user`,
      ).toBe(true);
    } finally {
      const u = await findOnScreenUser(request, adminToken, TEST_USER);
      if (u) await deleteOnScreenUser(request, adminToken, u.id);
    }
  });
});

// ── OIDC second-login dedup ───────────────────────────────────────────────

test.describe('Auth providers — OIDC second-login dedup', () => {
  test.skip(!process.env.E2E_OIDC_GROUP_SYNC_ENABLED, 'set E2E_OIDC_GROUP_SYNC_ENABLED to run');
  test.skip(!ONSCREEN_PASSWORD, 'set E2E_PASSWORD');

  test('logging in twice via OIDC must not create a duplicate user row', async ({ page, request }) => {
    const adminToken = await onscreenAdminToken(request);
    const stale = await findOnScreenUser(request, adminToken, TEST_USER);
    if (stale) await deleteOnScreenUser(request, adminToken, stale.id);

    try {
      // First login — full UI walk.
      await page.goto('/login', { waitUntil: 'domcontentloaded' });
      await Promise.all([
        page.waitForURL(/localhost:8080.*openid-connect\/auth/, { timeout: 15_000 }),
        page.locator('button.sso-btn').first().click(),
      ]);
      await submitKeycloakLogin(page, TEST_USER, TEST_PASS);
      await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 });

      const firstHit = await findOnScreenUser(request, adminToken, TEST_USER);
      expect(firstHit, 'first OIDC login must JIT-create the user').not.toBeNull();
      const firstID = firstHit!.id;

      // Clear cookies (both OnScreen + Keycloak SSO state) so the second
      // run goes through the same code path as a fresh user.
      await page.context().clearCookies();

      // Second login. Keycloak may either show the form again (if its
      // SSO cookie was cleared) or auto-redirect (if it still has one);
      // handle both cases.
      await page.goto('/login', { waitUntil: 'domcontentloaded' });
      await Promise.all([
        page.waitForURL(/localhost:8080|localhost:7070/, { timeout: 15_000 }),
        page.locator('button.sso-btn').first().click(),
      ]);
      if (await page.locator('#username').isVisible({ timeout: 1000 }).catch(() => false)) {
        await submitKeycloakLogin(page, TEST_USER, TEST_PASS);
      }
      await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 });

      // Verify: exactly ONE user with this username, same id as before.
      const usersR = await request.get('/api/v1/users', { headers: { Authorization: `Bearer ${adminToken}` } });
      expect(usersR.status()).toBe(200);
      const matches = ((await usersR.json()).data as any[]).filter((u) => u.username === TEST_USER);
      expect(
        matches.length,
        `second OIDC login must NOT create a duplicate row; found ${matches.length}: ${matches.map((m) => m.id).join(', ')}`,
      ).toBe(1);
      expect(matches[0].id, 'second login must reuse the same user id').toBe(firstID);
    } finally {
      const u = await findOnScreenUser(request, adminToken, TEST_USER);
      if (u) await deleteOnScreenUser(request, adminToken, u.id);
    }
  });
});

// ── SAML AuthnRequest single-use replay ───────────────────────────────────

test.describe('Auth providers — SAML AuthnRequest single-use', () => {
  test.skip(!process.env.E2E_SAML_GROUP_SYNC_ENABLED, 'set E2E_SAML_GROUP_SYNC_ENABLED to run');
  test.skip(!ONSCREEN_PASSWORD, 'set E2E_PASSWORD');

  test('replaying the same SAML Response at /acs is rejected (RequestTracker single-use)', async ({
    page,
    request,
    baseURL,
  }) => {
    const adminToken = await onscreenAdminToken(request);
    const stale = await findOnScreenUser(request, adminToken, TEST_USER);
    if (stale) await deleteOnScreenUser(request, adminToken, stale.id);

    // Intercept the form-POST that Keycloak submits to /api/v1/auth/saml/acs
    // and capture its body so we can replay it after the test completes
    // the round-trip naturally. We let the original request through unchanged.
    let capturedBody: string | null = null;
    await page.route('**/api/v1/auth/saml/acs', async (route) => {
      const post = route.request().postData();
      if (post && !capturedBody) capturedBody = post;
      await route.continue();
    });

    try {
      // Walk the SAML browser flow (succeeds — first consumption of the
      // AuthnRequest ID, so RequestTracker accepts).
      await page.goto('/login', { waitUntil: 'domcontentloaded' });
      await Promise.all([
        page.waitForURL(/localhost:8080.*\/protocol\/saml/, { timeout: 15_000 }),
        page.locator('button.sso-btn').nth(1).click(),
      ]);
      await submitKeycloakLogin(page, TEST_USER, TEST_PASS);
      await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 });

      expect(capturedBody, 'must have captured the form-POST to /acs').not.toBeNull();

      // Replay the IDENTICAL form body to /acs from an anonymous request
      // context (no cookies). The RequestTracker should reject because
      // the AuthnRequest's InResponseTo id has already been consumed.
      // 4xx is the canonical reject; a 302 to /login (failure-redirect)
      // is also acceptable. Anything that lets the caller in (302 → /,
      // 200 with session) fails.
      const replayR = await request.post(`${baseURL}/api/v1/auth/saml/acs`, {
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        data: capturedBody!,
        maxRedirects: 0,
      });

      const status = replayR.status();
      const loc = replayR.headers()['location'] ?? '';
      const isRedirectToLogin = status === 302 && /\/login/.test(loc);
      const isClientError = status >= 400 && status < 500;
      expect(
        isClientError || isRedirectToLogin,
        `replayed AuthnRequest must NOT succeed; got status=${status} location=${loc}`,
      ).toBe(true);
    } finally {
      const u = await findOnScreenUser(request, adminToken, TEST_USER);
      if (u) await deleteOnScreenUser(request, adminToken, u.id);
    }
  });
});
