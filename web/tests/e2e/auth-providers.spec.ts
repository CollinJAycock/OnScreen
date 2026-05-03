// Auth-provider regression spec — locks down the OIDC + SAML + LDAP
// integration paths so the four-layer SAML signing/tracker fix and the
// OIDC/LDAP JIT-provisioning paths can't silently break between
// releases. v2.1 Track D item 1.
//
// All blocks gate on env vars and skip cleanly when the IdP stack
// isn't provisioned — same pattern as smoke.spec.ts. CI sets the
// vars after starting `docker/docker-compose.idp.yml`; local devs
// run the same compose file then `npm run test:e2e -- auth-providers`.
//
// Required env (per provider, all optional):
//
//   E2E_OIDC_ENABLED       set to anything non-empty to run OIDC checks
//   E2E_OIDC_ISSUER_HOST   substring expected in the redirect Location
//                          header (e.g. "localhost:8080" for Keycloak)
//
//   E2E_SAML_ENABLED       set to anything non-empty to run SAML checks
//   E2E_SAML_IDP_HOST      substring expected in the SAML AuthnRequest
//                          redirect (e.g. "localhost:8080")
//
//   E2E_LDAP_ENABLED       set to anything non-empty to run LDAP checks
//   E2E_LDAP_USERNAME      a user that exists in the test directory
//                          (default 'ldapuser' per docs/auth-test-stack.md)
//   E2E_LDAP_PASSWORD      that user's password
//                          (default 'ldappass')
//
// See docs/auth-test-stack.md for spinning up Keycloak + lldap + Mailpit
// and wiring OnScreen to point at them.

import { test, expect } from '@playwright/test';

// ── OIDC (Keycloak) ────────────────────────────────────────────────────────

test.describe('Auth providers — OIDC', () => {
  test.skip(!process.env.E2E_OIDC_ENABLED, 'set E2E_OIDC_ENABLED to run OIDC specs');

  test('GET /api/v1/auth/oidc/enabled reports enabled', async ({ request }) => {
    const r = await request.get('/api/v1/auth/oidc/enabled');
    expect(r.status()).toBe(200);
    const { data } = await r.json();
    expect(data.enabled).toBe(true);
    expect(typeof data.display_name).toBe('string');
  });

  test('GET /api/v1/auth/oidc redirects to the configured issuer', async ({ request }) => {
    // Don't follow the redirect — we want to inspect the Location header
    // to verify it points at the IdP, not silently hand the browser off
    // to whatever comes back. Auto-redirect would mask a misconfiguration
    // (e.g. fall-through to a wrong issuer).
    const r = await request.get('/api/v1/auth/oidc', { maxRedirects: 0 });
    // 302 (Found) and 307 (Temporary Redirect) are both valid for the
    // OIDC authorization redirect — the OnScreen handler currently
    // emits 307 via http.StatusTemporaryRedirect; both keep the GET
    // method and are accepted by all browsers per RFC 7231.
    expect([302, 307]).toContain(r.status());
    const loc = r.headers()['location'];
    expect(loc, 'OIDC redirect must include a Location header').toBeTruthy();
    if (process.env.E2E_OIDC_ISSUER_HOST) {
      expect(loc).toContain(process.env.E2E_OIDC_ISSUER_HOST);
    }
    // The PKCE/state machinery must round-trip — assert the OIDC standard
    // params are present so a regression that drops them is caught here
    // rather than as a confusing IdP error mid-flow.
    const url = new URL(loc);
    expect(url.searchParams.get('response_type')).toBe('code');
    expect(url.searchParams.get('client_id')).toBeTruthy();
    expect(url.searchParams.get('state')).toBeTruthy();
    expect(url.searchParams.get('code_challenge')).toBeTruthy();
    expect(url.searchParams.get('code_challenge_method')).toBe('S256');
  });
});

// ── SAML (Keycloak) ────────────────────────────────────────────────────────

test.describe('Auth providers — SAML', () => {
  test.skip(!process.env.E2E_SAML_ENABLED, 'set E2E_SAML_ENABLED to run SAML specs');

  test('GET /api/v1/auth/saml/enabled reports enabled', async ({ request }) => {
    const r = await request.get('/api/v1/auth/saml/enabled');
    expect(r.status()).toBe(200);
    const { data } = await r.json();
    expect(data.enabled).toBe(true);
  });

  test('GET /api/v1/auth/saml/metadata returns SAML 2.0 SP metadata', async ({ request }) => {
    // The SP metadata document is what the admin uploads (or the IdP
    // fetches by URL) to register OnScreen as a relying party. A
    // regression here breaks every SAML-enabled install on upgrade.
    const r = await request.get('/api/v1/auth/saml/metadata');
    expect(r.status()).toBe(200);
    expect(r.headers()['content-type']).toMatch(/xml/);
    const body = await r.text();
    expect(body).toContain('EntityDescriptor');
    expect(body).toContain('SPSSODescriptor');
    expect(body).toContain('AssertionConsumerService');
  });

  test('GET /api/v1/auth/saml redirects to the IdP with a signed AuthnRequest', async ({ request }) => {
    const r = await request.get('/api/v1/auth/saml', { maxRedirects: 0 });
    expect(r.status()).toBe(302);
    const loc = r.headers()['location'];
    expect(loc).toBeTruthy();
    if (process.env.E2E_SAML_IDP_HOST) {
      expect(loc).toContain(process.env.E2E_SAML_IDP_HOST);
    }
    // The four-layer SAML signing fix — request must carry SAMLRequest +
    // SigAlg + Signature on redirect-binding, otherwise Keycloak rejects
    // it. This assertion exists specifically to guard that fix.
    const url = new URL(loc);
    expect(url.searchParams.get('SAMLRequest')).toBeTruthy();
    expect(url.searchParams.get('SigAlg')).toBeTruthy();
    expect(url.searchParams.get('Signature')).toBeTruthy();
  });
});

// ── LDAP (lldap) ───────────────────────────────────────────────────────────

test.describe('Auth providers — LDAP', () => {
  test.skip(!process.env.E2E_LDAP_ENABLED, 'set E2E_LDAP_ENABLED to run LDAP specs');

  const username = process.env.E2E_LDAP_USERNAME ?? 'ldapuser';
  const password = process.env.E2E_LDAP_PASSWORD ?? 'ldappass';

  test('GET /api/v1/auth/ldap/enabled reports enabled', async ({ request }) => {
    const r = await request.get('/api/v1/auth/ldap/enabled');
    expect(r.status()).toBe(200);
    const { data } = await r.json();
    expect(data.enabled).toBe(true);
  });

  test('POST /api/v1/auth/ldap/login with valid creds issues a session', async ({ request }) => {
    // End-to-end: directory bind happens server-side via the configured
    // LDAP host, JIT-provisioning runs on first success, the response
    // includes a usable access token. No browser SSO redirect — the
    // LDAP path is the one provider OnScreen handles fully server-side.
    const r = await request.post('/api/v1/auth/ldap/login', {
      data: { username, password },
    });
    expect(r.status(), `body=${await r.text()}`).toBe(200);
    const { data } = await r.json();
    expect(data.access_token, 'login must return an access token usable on the next request').toBeTruthy();
    expect(data.username).toBeTruthy();

    // Round-trip: hit a Required-gated endpoint with the freshly-issued
    // token to prove the token is actually valid (not just well-shaped).
    const me = await request.get('/api/v1/users/me/preferences', {
      headers: { Authorization: `Bearer ${data.access_token}` },
    });
    expect(me.status(), 'token issued by LDAP login must satisfy the auth middleware').toBe(200);
  });

  test('POST /api/v1/auth/ldap/login with wrong password is 401', async ({ request }) => {
    // Negative path: a directory bind failure must surface as 401, not
    // 200-with-an-empty-token or 500. This is the auth boundary that
    // separates "nothing to see here" from "something to investigate."
    const r = await request.post('/api/v1/auth/ldap/login', {
      data: { username, password: password + '-NOPE' },
    });
    expect(r.status()).toBe(401);
  });
});
