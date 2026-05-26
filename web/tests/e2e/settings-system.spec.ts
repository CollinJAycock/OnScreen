// E2E for Settings ▸ System — the cluster-wide knobs that moved out of env vars
// into the admin UI this cycle (server name, retention, TMDB rate, asset cache,
// static-ABR pre-encode, scanner concurrency, missing-file grace, LAN discovery).
// The ABR ladder itself lives in Settings ▸ Transcode (see transcode-limits spec).
//
// These are read once at startup, so this spec verifies the PERSISTENCE contract
// (PUT then GET round-trips through the DB) and the response shape — not the
// restart-time effect, which the manual plan covers. It also guards the
// regression where GET returned an un-enveloped body the web client couldn't
// read.
//
// Required env: E2E_PASSWORD (admin). Each mutating test restores the original
// config so it doesn't leave the dev server reconfigured.

import { test, expect } from '@playwright/test';
import { adminToken, auth, loginUI, CAN_API, CAN_UI } from './_auth';

const SYSTEM_FIELDS = [
  'server_name',
  'retain_months',
  'tmdb_rate_limit',
  'public_asset_cache',
  'static_abr_enabled',
  'missing_file_grace_minutes',
  'scan_file_concurrency',
  'scan_library_concurrency',
  'discovery_enabled',
  'discovery_port',
] as const;

test.describe('Settings ▸ System — API', () => {
  test.skip(!CAN_API, 'set E2E_PASSWORD to run System settings specs');

  test('GET /settings/system is enveloped and exposes every moved field', async ({ request }) => {
    const token = await adminToken(request);
    const r = await request.get('/api/v1/settings/system', auth(token));
    expect(r.status()).toBe(200);

    const body = await r.json();
    // The regression this guards: GET must use the {data:…} envelope the web
    // client unwraps. A bare object would leave the form blank.
    expect(body, 'response must be enveloped').toHaveProperty('data');
    const sys = body.data;
    for (const f of SYSTEM_FIELDS) {
      expect(sys, `missing field ${f}`).toHaveProperty(f);
    }
    expect(typeof sys.server_name).toBe('string');
    expect(typeof sys.retain_months).toBe('number');
    expect(typeof sys.discovery_enabled).toBe('boolean');
  });

  test('PUT then GET round-trips every field through the DB', async ({ request }) => {
    const token = await adminToken(request);

    const orig = (await (await request.get('/api/v1/settings/system', auth(token))).json()).data;

    const desired = {
      ...orig,
      server_name: 'E2E Round Trip',
      retain_months: 18,
      tmdb_rate_limit: 7,
      public_asset_cache: !orig.public_asset_cache,
      static_abr_enabled: !orig.static_abr_enabled,
      missing_file_grace_minutes: 42,
      scan_file_concurrency: 9,
      scan_library_concurrency: 5,
      discovery_enabled: !orig.discovery_enabled,
      discovery_port: 7399,
    };

    try {
      const put = await request.put('/api/v1/settings/system', { ...auth(token), data: desired });
      expect([200, 204]).toContain(put.status());

      const got = (await (await request.get('/api/v1/settings/system', auth(token))).json()).data;
      for (const f of SYSTEM_FIELDS) {
        expect(got[f], `field ${f} did not round-trip`).toBe(desired[f]);
      }
    } finally {
      // Restore so the dev server isn't left reconfigured.
      await request.put('/api/v1/settings/system', { ...auth(token), data: orig });
    }
  });

  test('rejects unauthenticated access', async ({ request }) => {
    const r = await request.get('/api/v1/settings/system');
    expect(r.status(), 'no token → 401').toBe(401);
    const w = await request.put('/api/v1/settings/system', { data: { server_name: 'nope' } });
    expect([401, 403]).toContain(w.status());
  });
});

test.describe('Settings ▸ System — UI', () => {
  test.skip(!CAN_UI, 'set E2E_PASSWORD to run System settings UI specs');

  test('page loads with the scanner + discovery sections, no console errors', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
    page.on('pageerror', (e) => errors.push(e.message));

    await loginUI(page);

    await page.goto('/settings/system');
    // Restart-required notice + the new sections render. (HTTPS/TLS moved to
    // Settings ▸ Security; covered separately by the TLS spec.)
    await expect(page.getByText(/restart the server to apply/i)).toBeVisible();
    await expect(page.getByRole('heading', { name: /Scanner/i })).toBeVisible();
    await expect(page.getByRole('heading', { name: /LAN discovery/i })).toBeVisible();

    // Drive the Security page in the same login session and assert TLS lives there now.
    await page.goto('/settings/security');
    await expect(page.getByRole('heading', { name: /HTTPS \/ TLS/i })).toBeVisible();

    // Filter known harmless noise:
    // - cloudflareinsights — the analytics beacon, blocked by some dev setups
    // - notifications/stream — Firefox surfaces transient SSE-disconnect mid-
    //   navigation as a JS console error ("Firefox can't establish a connection
    //   to the server at …/notifications/stream"); Chromium and WebKit swallow
    //   the same reconnect silently. The SSE client retries automatically, so
    //   this is a Firefox-browser quirk, not a server problem.
    const real = errors.filter(
      (e) => !/cloudflareinsights/i.test(e) && !/notifications\/stream/i.test(e),
    );
    expect(real, `console errors:\n${real.join('\n')}`).toEqual([]);
  });
});
