// Security re-probe — Tier 3 manual rows that are already unit-tested but
// worth a live HTTP-level smoke against the deployed shape. None of these
// exercise novel attacks; they re-verify that path-traversal, open-redirect,
// CSRF, bearer-leak, rate-limiting, XSS-reflection, and admin-authz guards
// are still wired through the real router and not only the unit-test fakes.
//
// The base probes don't need a real login — the endpoints should reject
// auth-less requests too. Rate-limiting and admin-authz blocks do require
// credentials; they skip when E2E_PASSWORD is not set.

import { test, expect, request as pwRequest } from '@playwright/test';
import {
  USERNAME, PASSWORD, adminToken, loginAs, registerUser, retryOn429, drainAuthRateLimit,
} from './_auth';


test.describe('Security re-probe', () => {
  test('path traversal on /artwork rejected', async ({ request }) => {
    for (const probe of [
      '/artwork/..%2F..%2Fetc%2Fpasswd',
      '/artwork/..%252F..%252Fetc%252Fpasswd',
      '/artwork/%2e%2e%2f%2e%2e%2fetc%2fpasswd',
    ]) {
      const r = await request.get(probe, { maxRedirects: 0 });
      // 401 acceptable: the artwork handler is auth-gated post-H1.
      expect.soft([400, 401, 403, 404], `${probe} status`).toContain(r.status());
      // Defensive: never 200, never include /etc/passwd contents.
      expect.soft(r.status(), probe).not.toBe(200);
    }
  });

  test('path traversal on /trickplay rejected', async ({ request }) => {
    for (const probe of [
      '/trickplay/..%2F..%2Fetc%2Fpasswd',
      '/trickplay/00000000-0000-0000-0000-000000000000/..%2Findex.vtt',
    ]) {
      const r = await request.get(probe, { maxRedirects: 0 });
      // 401 is acceptable here too — the trickplay handler is auth-gated post-H1.
      expect.soft([400, 401, 403, 404], `${probe} status`).toContain(r.status());
      expect.soft(r.status(), probe).not.toBe(200);
    }
  });

  test('path traversal on /media/stream rejected', async ({ request }) => {
    // Only encoded variants — a bare `/media/stream/..` is normalized
    // client-side (curl, fetch, Playwright's request fixture all collapse
    // `..`) so it never reaches the server in that form. Encoded slashes
    // survive normalization and are the meaningful traversal vector.
    for (const probe of [
      '/media/stream/..%2F..%2Fetc%2Fpasswd',
      '/media/stream/%2e%2e%2f%2e%2e%2fetc%2fpasswd',
    ]) {
      const r = await request.get(probe, { maxRedirects: 0 });
      expect.soft([400, 401, 403, 404], `${probe} status`).toContain(r.status());
      expect.soft(r.status(), probe).not.toBe(200);
    }
  });

  test('login redirect param does not honor cross-origin', async ({ request }) => {
    // Open-redirect probe — even if the param is accepted, a redirect to
    // evil.com would be the bug. We test the response Location header.
    const r = await request.get('/login?redirect=https://evil.example/', {
      maxRedirects: 0,
    });
    const location = r.headers()['location'] ?? '';
    expect.soft(location.startsWith('https://evil.example')).toBe(false);
  });

  test('forgot-password endpoint accepts a body without leaking enumeration', async ({ request }) => {
    // Two probes: known-shape and totally-fake email. Both should return
    // the same shape (200 with neutral body) — different responses leak
    // whether the email is registered.
    // Rate-limit-tolerant: forgot-password shares the auth limiter, and a
    // comparison where one probe is 429 and the other 200 tests nothing about
    // enumeration. (It also passed for the wrong reason whenever BOTH were
    // rate-limited.)
    const a = await retryOn429(() =>
      request.post('/api/v1/auth/forgot-password', {
        data: { email: 'definitely-not-a-real-account@example.invalid' },
      }),
    );
    const b = await retryOn429(() =>
      request.post('/api/v1/auth/forgot-password', { data: { email: 'admin@example.com' } }),
    );
    test.skip(a.status() === 429 || b.status() === 429, 'forgot-password rate-limited — enumeration check inconclusive');
    // Both 200 (or both 202) — same status either way.
    expect.soft(a.status(), 'fake-email status').toBe(b.status());
  });

  test('CORS preflight does not echo arbitrary origin with credentials', async ({ request }) => {
    const r = await request.fetch('/api/v1/libraries', {
      method: 'OPTIONS',
      headers: {
        Origin: 'https://attacker.example',
        'Access-Control-Request-Method': 'GET',
      },
    });
    const allowOrigin = r.headers()['access-control-allow-origin'] ?? '';
    const allowCreds = r.headers()['access-control-allow-credentials'] ?? '';
    if (allowCreds.toLowerCase() === 'true') {
      // If credentials are allowed, allow-origin MUST NOT be `*` and
      // MUST NOT echo an unconfigured origin.
      expect.soft(allowOrigin).not.toBe('*');
      expect.soft(allowOrigin).not.toBe('https://attacker.example');
    }
  });

  test('bearer token in URL query is not logged back to client', async ({ request }) => {
    // The "TestLogger_NeverLogsRawQueryString" unit test guards the logger.
    // This live probe verifies the request still completes (or rejects
    // gracefully) when a bearer is misplaced into ?token= — the server
    // shouldn't accept query-string auth anymore (M2/M3).
    const r = await request.get('/api/v1/libraries?token=bogus-bearer-here');
    // Without a real Authorization header, this must NOT succeed.
    expect.soft([401, 403]).toContain(r.status());
  });

  test('path traversal in transcode segment URL rejected', async ({ request }) => {
    // Transcode session IDs are UUIDs; a literal "abc" is structurally invalid
    // but the handler must not allow traversal out of the segment directory
    // regardless. These probes target the segment sub-path — not the session
    // itself — so 401 (auth gate) or 4xx are both fine; 200 is not.
    for (const probe of [
      '/api/v1/transcode/sessions/abc/seg/..%2Fpasswd',
      '/api/v1/transcode/sessions/abc/seg/..%2F..%2Fetc%2Fpasswd',
      '/api/v1/transcode/sessions/abc/seg/%2e%2e%2f%2e%2e%2fetc%2fpasswd',
    ]) {
      const r = await request.get(probe, { maxRedirects: 0 });
      expect.soft([400, 401, 403, 404], `${probe} → ${r.status()}`).toContain(r.status());
      expect.soft(r.status(), probe).not.toBe(200);
    }
  });
});

// ── CSP nonce + asset token ──────────────────────────────────────────────────
//
// Guards Track C item 2 (nonce-based script-src) and item 1 (asset token).
// All request-based — no browser render needed — but a full browser soak
// (load the SPA, assert zero CSP violations in the console) is still the
// final gate before shipping the nonce change.

test.describe('Security — CSP nonce', () => {
  test('script-src is nonce-based and the shell stamps the matching nonce', async ({ request }) => {
    const r = await request.get('/');
    expect(r.status()).toBe(200);
    const csp = r.headers()['content-security-policy'] ?? '';
    expect(csp, 'CSP header present').not.toBe('');

    // Pull the script-src directive out of the header.
    const scriptSrc = csp.split('; ').find((d) => d.startsWith('script-src ')) ?? '';
    expect(scriptSrc, 'script-src directive present').not.toBe('');
    expect(scriptSrc, "script-src must not carry 'unsafe-inline'").not.toContain("'unsafe-inline'");

    const m = scriptSrc.match(/'nonce-([^']+)'/);
    expect(m, 'script-src must carry a nonce').not.toBeNull();
    const nonce = m![1];

    // The served shell's inline <script> tags must carry the SAME nonce
    // as the header, or the browser blocks them and the SPA never boots.
    const html = await r.text();
    expect(html, 'shell must stamp the header nonce on its inline scripts').toContain(
      `<script nonce="${nonce}">`,
    );
    // No bare inline <script> should survive un-nonced.
    expect(html, 'no un-nonced inline <script> may remain').not.toContain('<script>');
  });

  test('CSP nonce is fresh per request (not cached into the shell)', async ({ request }) => {
    const nonceOf = async () => {
      const r = await request.get('/');
      const csp = r.headers()['content-security-policy'] ?? '';
      return csp.match(/'nonce-([^']+)'/)?.[1] ?? '';
    };
    const a = await nonceOf();
    const b = await nonceOf();
    expect(a, 'first nonce present').not.toBe('');
    expect(b, 'second nonce present').not.toBe('');
    expect(a, 'nonce must rotate per request').not.toBe(b);
  });

  test.skip(!PASSWORD, 'set E2E_PASSWORD to run the asset-token contract');

  test('login mints an asset token; general token is rejected in ?token=', async ({ request }) => {
    // Raw login on purpose: this test asserts on the login RESPONSE BODY
    // (asset_token), which the cached-token helper doesn't expose.
    const loginR = await retryOn429(() =>
      request.post('/api/v1/auth/login', { data: { username: USERNAME, password: PASSWORD } }),
    );
    expect(loginR.status()).toBe(200);
    const { data } = await loginR.json();
    // Asset token is present and is a PASETO v4.local — distinct from the
    // access token, scoped purpose=asset.
    expect(data.asset_token, 'login response must include an asset_token').toBeTruthy();
    expect(data.asset_token, 'asset token is PASETO v4.local').toMatch(/^v4\.local\./);
    expect(data.asset_token).not.toBe(data.access_token);

    // The general access token must NOT authenticate an asset route via
    // ?token= (that leak is what the asset token closes). Use a bogus
    // artwork path — auth runs before the file lookup, so a general token
    // yields 401, not 404.
    const anon = await pwRequest.newContext();
    try {
      const r = await anon.get(
        `/artwork/does-not-exist.jpg?token=${encodeURIComponent(data.access_token)}`,
        { maxRedirects: 0 },
      );
      expect(
        [401, 403],
        `general access token in ?token= on /artwork must be rejected (got ${r.status()})`,
      ).toContain(r.status());

      // Route-grouping fix: /items/{id}/image (photos) and the Live TV
      // stream playlist moved under RequiredAllowQueryToken, so the asset
      // token now authenticates them via ?token=. A bogus-but-valid UUID
      // exercises the auth gate without needing seeded content — anything
      // other than 401 proves the token was accepted and the handler ran.
      const bogus = '00000000-0000-0000-0000-000000000000';
      for (const path of [
        `/api/v1/items/${bogus}/image?token=${encodeURIComponent(data.asset_token)}`,
        `/api/v1/tv/channels/${bogus}/stream.m3u8?token=${encodeURIComponent(data.asset_token)}`,
      ]) {
        const ok = await anon.get(path, { maxRedirects: 0 });
        expect(
          ok.status(),
          `${path} must accept the asset token via ?token= (got 401 — route not under RequiredAllowQueryToken?)`,
        ).not.toBe(401);
      }
    } finally {
      await anon.dispose();
    }
  });
});

// ── Rate limiting ──────────────────────────────────────────────────────────
//
// Gated on E2E_RATE_LIMIT (NOT just E2E_PASSWORD) because tripping the
// brute-force limiter locks out the test runner's IP for whatever cooldown
// the server enforces — long enough to break every subsequent test in the
// suite that needs to log in. Run this in isolation:
//
//   $env:E2E_RATE_LIMIT="1"; npx playwright test security.spec.ts -g "rate limit"
//
// We also use a guaranteed-nonexistent username so we can't lock out the
// real admin account even on deployments that key the limiter per-username.

test.describe('Security — rate limiting', () => {
  test.skip(!process.env.E2E_RATE_LIMIT, 'set E2E_RATE_LIMIT=1 to run (will lock out the test IP for a cooldown)');

  test('25 bad login attempts trigger 429 on the login endpoint', async ({ request }) => {
    // Send 25 deliberately-wrong-password requests in quick succession against
    // a fake username. The server's brute-force middleware should return 429
    // before (or by) the last attempt. We collect all statuses and assert at
    // least one 429 appeared — the exact threshold may be tuned lower by
    // deployment.
    const fakeUser = `__e2e_ratelimit_probe_${Date.now()}`;
    const statuses: number[] = [];
    for (let i = 0; i < 25; i++) {
      const r = await request.post('/api/v1/auth/login', {
        data: { username: fakeUser, password: `WRONG-${i}` },
      });
      statuses.push(r.status());
      if (r.status() === 429) break;
    }
    expect(
      statuses,
      `Never received 429 after ${statuses.length} bad logins; got: ${[...new Set(statuses)].join(', ')}`,
    ).toContain(429);

    // Hand the shared per-IP window back before finishing. This test knowingly
    // exhausts a resource every later spec needs; without the drain the whole
    // remainder of the run inherits a locked-out IP and fails as an
    // unauthenticated cascade rather than on its own merits.
    const drained = await drainAuthRateLimit(request);
    expect(drained, 'auth rate-limit window did not drain within budget').toBe(true);
  });
});

// ── XSS reflection ─────────────────────────────────────────────────────────

test.describe('Security — XSS reflection', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run XSS specs');

  test('search query with <script> tag is not reflected unescaped', async ({ page }) => {
    // Log in first so the search UI is reachable.
    await page.goto('/login');
    await page.getByLabel(/username/i).fill(USERNAME);
    await page.getByLabel(/password/i).fill(PASSWORD);
    await page.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(page).not.toHaveURL(/\/login/);

    // Intercept any XSS execution — if the script tag is reflected and
    // executed, window.__xss_fired will be set to true.
    await page.addInitScript(() => {
      (window as any).__xss_fired = false;
      (window as any).xssProbe = () => {
        (window as any).__xss_fired = true;
      };
    });

    const payload = '<script>window.xssProbe()</script>';

    // Try the search API endpoint directly — check the JSON response body
    // does not echo the raw tag.
    const r = await page.request.get(`/api/v1/search?q=${encodeURIComponent(payload)}`, {
      headers: {
        Accept: 'application/json',
      },
    });
    // API returns JSON — any status is fine as long as the raw payload
    // isn't echoed verbatim in a way that could escape into HTML context.
    if (r.ok()) {
      const text = await r.text();
      expect.soft(text, 'API must not echo raw <script> tag').not.toContain('<script>window.xssProbe');
    }

    // Browser check: navigate to search with the payload as a query param
    // and assert the script was never executed. Use 'domcontentloaded'
    // not 'networkidle' — the notifications SSE stream stays open and
    // 'networkidle' would time out forever.
    await page.goto(`/search?q=${encodeURIComponent(payload)}`, { waitUntil: 'domcontentloaded' });
    // Brief settle: any reflected <script> would execute synchronously
    // during HTML parse, but allow a tick for any async hydration too.
    await page.waitForTimeout(500);
    const fired = await page.evaluate(() => (window as any).__xss_fired);
    expect(fired, 'Script tag must not execute — XSS reflected in search route').toBe(false);
  });
});

// ── Admin endpoint authorization ───────────────────────────────────────────

test.describe('Security — admin endpoint authorization', () => {
  test('unauthenticated request to admin endpoints returns 401', async ({ request }) => {
    // These endpoints require admin privileges. Without any auth header the
    // server must return 401 (or 403). A 200 with no auth is a critical bug.
    // Each path here exists in the router — adding a bogus path would just
    // return 404 and tell us nothing.
    const adminEndpoints = [
      { method: 'GET', path: '/api/v1/users' },
      { method: 'GET', path: '/api/v1/admin/logs' },
      { method: 'GET', path: '/api/v1/admin/tasks' },
      { method: 'POST', path: '/api/v1/libraries' },
    ];
    for (const { method, path } of adminEndpoints) {
      const r =
        method === 'POST'
          ? await request.post(path, { data: {} })
          : await request.get(path);
      expect.soft(
        [401, 403],
        `${method} ${path} must reject unauthenticated requests (got ${r.status()})`,
      ).toContain(r.status());
    }
  });

  test.skip(!PASSWORD, 'set E2E_PASSWORD to run non-admin authz specs');

  test('non-admin user cannot reach admin-only endpoints', async ({ request }) => {
    // Register a fresh non-admin account, then try admin endpoints.
    // Clean up the account regardless of test outcome.
    const ts = Date.now();
    const testUser = `e2e-nonadmin-${ts}`;
    const testPass = `E2ePass${ts}!`;

    const reg = await registerUser(request, {
      username: testUser, password: testPass, email: `${testUser}@example.invalid`,
    });
    // If registration is admin-only or disabled, skip gracefully.
    if (reg.status === 403 || reg.status === 404) {
      test.skip(true, 'self-registration is disabled on this instance');
      return;
    }
    expect(reg.status, `register status: ${reg.raw}`).toBe(200);

    const userToken = await loginAs(request, testUser, testPass);

    try {
      // Admin-only list-users must be 403 for a regular user.
      const usersR = await request.get('/api/v1/users', {
        headers: { Authorization: `Bearer ${userToken}` },
      });
      expect.soft([403], `non-admin /api/v1/users must be 403, got ${usersR.status()}`).toContain(usersR.status());
    } finally {
      // Clean up: log in as admin and delete the test account.
      const adminTok = await adminToken(request).catch(() => '');
      if (adminTok) {
        // Look up the user's ID from the admin users list.
        const list = await request.get('/api/v1/users', {
          headers: { Authorization: `Bearer ${adminTok}` },
        });
        if (list.ok()) {
          const { data: users } = await list.json();
          const found = (users as any[]).find((u: any) => u.username === testUser);
          if (found) {
            await request.delete(`/api/v1/users/${found.id}`, {
              headers: { Authorization: `Bearer ${adminTok}` },
            });
          }
        }
      }
    }
  });
});
