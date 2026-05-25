// E2E for Settings ▸ System ▸ HTTPS/TLS — admin-uploaded TLS certificate,
// stored encrypted and served in-memory (UI-managed HTTPS, no cert file on disk).
//
// Verifies the status view (never leaks the key), keypair validation, and the
// upload/clear round-trip. The actual HTTPS handshake is restart-required and
// lives in the manual plan.
//
// Safety: the upload/clear tests only run when no cert is currently configured,
// so they can't clobber a real uploaded cert (GET deliberately doesn't return
// the PEMs, so it can't be restored). They clear what they upload via afterAll.
//
// Required env: E2E_PASSWORD (admin).

import { test, expect, type APIRequestContext } from '@playwright/test';
import { adminToken, auth, CAN_API } from './_auth';

// Throwaway self-signed P-256 cert + matching key (CN=e2e.onscreen.test).
const CERT_PEM = `-----BEGIN CERTIFICATE-----
MIIBJDCBy6ADAgECAgEBMAoGCCqGSM49BAMCMBwxGjAYBgNVBAMTEWUyZS5vbnNj
cmVlbi50ZXN0MB4XDTI2MDUyNTE5NDUxOFoXDTM2MDUyMjIwNDUxOFowHDEaMBgG
A1UEAxMRZTJlLm9uc2NyZWVuLnRlc3QwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNC
AAS3GpIz8wfADSRuUxOasUEoF8BSOwomqiAP6RZAr5+ZBYUOyXcLolp6sSgu/TUV
KTqa8viHKQ3TyP9CFQSyapyZMAoGCCqGSM49BAMCA0gAMEUCIQCRER5O6lM0Ayy9
kEl1OSCfJbPjqVuuFGy2wWGPecsYqwIgXvrBErWdI6yqg9W0xNI9jFgW25m6eFuG
s4RPvaQsMkA=
-----END CERTIFICATE-----
`;
const KEY_PEM = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIFNTaE4o1nITCnK84nsHjAHyFlm0OGz2fap6wMUIDYppoAoGCCqGSM49
AwEHoUQDQgAEtxqSM/MHwA0kblMTmrFBKBfAUjsKJqogD+kWQK+fmQWFDsl3C6Ja
erEoLv01FSk6mvL4hykN08j/QhUEsmqcmQ==
-----END EC PRIVATE KEY-----
`;

async function tlsStatus(request: APIRequestContext, token: string) {
  const r = await request.get('/api/v1/settings/tls', auth(token));
  expect(r.status()).toBe(200);
  return (await r.json()).data as {
    configured: boolean;
    source: string;
    subject?: string;
    not_after?: string;
  };
}

test.describe('Settings ▸ TLS — API', () => {
  test.skip(!CAN_API, 'set E2E_PASSWORD to run TLS specs');

  test.afterAll(async ({ request }) => {
    if (!CAN_API) return;
    // Clear any cert this spec uploaded so the next restart doesn't try to serve
    // a throwaway test cert. No-op if nothing was uploaded.
    const token = await adminToken(request);
    const s = await tlsStatus(request, token);
    if (s.source === 'uploaded') {
      await request.put('/api/v1/settings/tls', { ...auth(token), data: { cert_pem: '', key_pem: '' } });
    }
  });

  test('status is enveloped, sane, and never leaks the key', async ({ request }) => {
    const token = await adminToken(request);
    const r = await request.get('/api/v1/settings/tls', auth(token));
    expect(r.status()).toBe(200);
    const body = await r.json();
    expect(body).toHaveProperty('data');
    expect(['none', 'uploaded', 'env-file']).toContain(body.data.source);
    expect(typeof body.data.configured).toBe('boolean');
    // The status view must NEVER echo the PEMs back.
    const raw = JSON.stringify(body);
    expect(raw).not.toContain('PRIVATE KEY');
    expect(raw).not.toContain('BEGIN CERTIFICATE');
  });

  test('rejects a garbage / mismatched keypair with 400', async ({ request }) => {
    const token = await adminToken(request);
    const s = await tlsStatus(request, token);
    test.skip(s.source === 'env-file', 'TLS pinned via env files — uploads blocked');

    // Both present but not a valid pair.
    const bad = await request.put('/api/v1/settings/tls', {
      ...auth(token),
      data: { cert_pem: '-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----', key_pem: 'garbage' },
    });
    expect(bad.status(), 'invalid pair must be 400').toBe(400);

    // Half a pair (cert without key) must also be rejected.
    const half = await request.put('/api/v1/settings/tls', {
      ...auth(token),
      data: { cert_pem: CERT_PEM, key_pem: '' },
    });
    expect(half.status(), 'half a pair must be 400').toBe(400);
  });

  test('uploads a valid cert, then clears it', async ({ request }) => {
    const token = await adminToken(request);
    const s = await tlsStatus(request, token);
    test.skip(s.source !== 'none', `won't clobber existing TLS (source=${s.source})`);

    // Upload → 204, status flips to uploaded with the parsed subject.
    const up = await request.put('/api/v1/settings/tls', {
      ...auth(token),
      data: { cert_pem: CERT_PEM, key_pem: KEY_PEM },
    });
    expect([200, 204]).toContain(up.status());

    const after = await tlsStatus(request, token);
    expect(after.configured).toBe(true);
    expect(after.source).toBe('uploaded');
    expect(after.subject).toContain('e2e.onscreen.test');

    // Clear → 204, status back to none.
    const clr = await request.put('/api/v1/settings/tls', {
      ...auth(token),
      data: { cert_pem: '', key_pem: '' },
    });
    expect([200, 204]).toContain(clr.status());
    const cleared = await tlsStatus(request, token);
    expect(cleared.configured).toBe(false);
    expect(cleared.source).toBe('none');
  });

  test('rejects unauthenticated access', async ({ request }) => {
    expect((await request.get('/api/v1/settings/tls')).status()).toBe(401);
  });
});
