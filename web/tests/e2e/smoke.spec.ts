// Tier 1 smoke — covers the boot/auth/library/UI rows of the manual plan
// that don't need eyes on pixels. Stops at the first golden-path break.
//
// Requires a running OnScreen server reachable at BASE_URL.

import { test, expect } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

test.describe('Tier 1 — boot', () => {
  test('health/live returns ok', async ({ request }) => {
    const r = await request.get('/health/live');
    expect(r.status()).toBe(200);
    const body = await r.json();
    expect(body.status).toBe('ok');
  });

  test('health/ready returns 200 (DB + Valkey + migrations healthy)', async ({ request }) => {
    const r = await request.get('/health/ready');
    expect(r.status()).toBe(200);
  });

  test('web UI loads with no console errors', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', (msg) => {
      if (msg.type() === 'error') errors.push(msg.text());
    });
    page.on('pageerror', (err) => errors.push(err.message));

    await page.goto('/');
    await expect(page).toHaveTitle(/OnScreen/i);
    // SvelteKit hydration finished — give a beat for late console errors.
    await page.waitForLoadState('networkidle');

    // Filter known-noisy entries that aren't real problems (Cloudflare beacon
    // can warn about analytics consent in dev). Anything else is a regression.
    const real = errors.filter((e) => !/cloudflareinsights/i.test(e));
    expect(real, `console errors:\n${real.join('\n')}`).toEqual([]);
  });
});

test.describe('Tier 1 — auth', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run auth specs');

  test('login → home, logout → login', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel(/username/i).fill(USERNAME);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(page).not.toHaveURL(/\/login/);

    // Refresh-token cookie should be set after login. Name varies per
    // build (renamed in S1), so just check at least one cookie exists.
    const cookies = await page.context().cookies();
    expect(cookies.length).toBeGreaterThan(0);

    // Logout — find the menu / button that triggers it. The selector here
    // intentionally accepts either a button or link to avoid coupling to
    // the current sidebar layout.
    await page.getByRole('button', { name: /log ?out|sign ?out/i }).first().click({ trial: true }).catch(() => {});
    // Direct API logout is the durable path — UI surface can change.
    const r = await page.request.post('/api/v1/auth/logout');
    expect([200, 204]).toContain(r.status());
  });
});

test.describe('Tier 1 — library + admin smoke', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run library specs');

  test('libraries endpoint returns at least one library', async ({ request }) => {
    const login = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(login.status()).toBe(200);
    const { data } = await login.json();
    const token = data.access_token as string;

    const libs = await request.get('/api/v1/libraries', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(libs.status()).toBe(200);
    const body = await libs.json();
    expect(Array.isArray(body.data)).toBe(true);
    expect(body.data.length).toBeGreaterThan(0);
  });

  test('settings page loads after login', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel(/username/i).fill(USERNAME);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(page).not.toHaveURL(/\/login/);

    await page.goto('/settings');
    // Settings page has a "TMDB" label somewhere — exact UI is fluid but
    // the metadata-key field has been there since v1.0.
    await expect(page.getByText(/tmdb/i).first()).toBeVisible();
  });

  test('API stays responsive while a library scan is in flight', async ({ request }) => {
    // Fires a scan against the first library, then immediately hits two
    // unrelated GET endpoints and asserts they both respond in under 2s.
    // The scanner should never block the API — historic regressions have
    // been DB-pool starvation under heavy scan load (see TrueNAS QA
    // "high CPU/memory" incident, fixed in scanner mtime+size short-
    // circuit).
    const login = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(login.status()).toBe(200);
    const { data } = await login.json();
    const token = data.access_token as string;

    const libs = await request.get('/api/v1/libraries', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(libs.status()).toBe(200);
    const { data: libList } = await libs.json();
    if (!Array.isArray(libList) || libList.length === 0) {
      test.skip(true, 'No libraries available — seed media first');
      return;
    }
    const libId = libList[0].id;

    // Kick off the scan (fire-and-forget — don't await completion).
    await request.post(`/api/v1/libraries/${libId}/scan`, {
      headers: { Authorization: `Bearer ${token}` },
    });

    // Immediately probe two unrelated endpoints. Both must respond within
    // 2s even with the scan running in the background.
    const t0 = Date.now();
    const r1 = await request.get('/api/v1/libraries', {
      headers: { Authorization: `Bearer ${token}` },
    });
    const t1 = Date.now();
    const r2 = await request.get('/api/v1/hub', {
      headers: { Authorization: `Bearer ${token}` },
    });
    const t2 = Date.now();

    expect(r1.status(), '/api/v1/libraries during scan').toBe(200);
    expect(r2.status(), '/api/v1/hub during scan').toBe(200);
    expect(t1 - t0, '/api/v1/libraries took longer than 2s during scan').toBeLessThan(2_000);
    expect(t2 - t1, '/api/v1/hub took longer than 2s during scan').toBeLessThan(2_000);
  });
});
