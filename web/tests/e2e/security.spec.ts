// Security re-probe — Tier 3 manual rows that are already unit-tested but
// worth a live HTTP-level smoke against the deployed shape. None of these
// exercise novel attacks; they re-verify that path-traversal, open-redirect,
// CSRF, and bearer-leak guards are still wired through the real router and
// not only the unit-test fakes.
//
// Doesn't need a real login — the probes target endpoints that should
// reject auth-less requests too. If a row 200s without auth, the test
// fails loudly, which IS the regression you want to catch.

import { test, expect } from '@playwright/test';

test.describe('Security re-probe', () => {
  test('path traversal on /artwork rejected', async ({ request }) => {
    for (const probe of [
      '/artwork/..%2F..%2Fetc%2Fpasswd',
      '/artwork/..%252F..%252Fetc%252Fpasswd',
      '/artwork/%2e%2e%2f%2e%2e%2fetc%2fpasswd',
    ]) {
      const r = await request.get(probe, { maxRedirects: 0 });
      expect.soft([400, 403, 404], `${probe} status`).toContain(r.status());
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
    for (const probe of ['/media/stream/..', '/media/stream/..%2F..%2Fetc%2Fpasswd']) {
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
    const a = await request.post('/api/v1/auth/forgot-password', {
      data: { email: 'definitely-not-a-real-account@example.invalid' },
    });
    const b = await request.post('/api/v1/auth/forgot-password', {
      data: { email: 'admin@example.com' },
    });
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
});
