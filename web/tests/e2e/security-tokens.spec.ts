// Token-scoping security regression — guards the per-file stream token
// (24h, file_id-bound, purpose-scoped) shipped with v2.1 Track E. Two
// boundaries are checked: (1) a token issued for file A must not be usable
// to fetch file B (file_id claim enforcement), and (2) a stream token must
// not satisfy the Bearer auth on general API endpoints (purpose claim
// enforcement). Without these the leaked-URL blast radius is unbounded.
//
// Required env:
//   E2E_USERNAME   OnScreen username (default 'admin')
//   E2E_PASSWORD   OnScreen password — required; block skips otherwise
//
// Doesn't require any specific media — picks the first two distinct files
// from the first two libraries that have any items. Skips cleanly if the
// dev server has fewer than two playable files across all libraries.

import { test, expect, request as pwRequest, type APIRequestContext } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

interface FileWithToken {
  itemId: string;
  fileId: string;
  streamToken: string;
}

// Find two distinct files (different ids) — they can be from the same or
// different libraries; what matters for the test is that they're separate
// `media_files` rows with separate stream_tokens.
async function findTwoFiles(
  request: APIRequestContext,
  token: string,
): Promise<{ a: FileWithToken; b: FileWithToken } | null> {
  const libsR = await request.get('/api/v1/libraries', {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!libsR.ok()) return null;
  const { data: libs } = await libsR.json();
  if (!Array.isArray(libs)) return null;

  const collected: FileWithToken[] = [];
  for (const lib of libs) {
    const itemsR = await request.get(`/api/v1/libraries/${lib.id}/items?limit=5`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!itemsR.ok()) continue;
    const { data: items } = await itemsR.json();
    if (!Array.isArray(items)) continue;

    for (const it of items) {
      const detailR = await request.get(`/api/v1/items/${it.id}`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!detailR.ok()) continue;
      const { data: detail } = await detailR.json();
      const f = (detail.files as any[])?.[0];
      if (!f?.id || !f?.stream_token) continue;
      collected.push({ itemId: detail.id, fileId: f.id, streamToken: f.stream_token });
      if (collected.length >= 2) break;
    }
    if (collected.length >= 2) break;
  }

  if (collected.length < 2) return null;
  return { a: collected[0], b: collected[1] };
}

test.describe('Token scoping — per-file stream token', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run token-scoping specs');

  test("file A's stream token cannot fetch file B (file_id claim enforced)", async ({ request, baseURL }) => {
    // Setup uses the authenticated `request` context (cookie + Bearer
    // auth carry across calls — fine for fetching item metadata).
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    const pair = await findTwoFiles(request, token);
    if (!pair) {
      test.skip(true, 'Could not find two distinct files with stream_token in any library');
      return;
    }
    const { a, b } = pair;

    // Probe with a FRESH anonymous request context — the default
    // `request` fixture persists session cookies from the login above,
    // and /media/stream/{id} accepts cookie auth too, which would mask
    // a broken stream-token check (the cookie would grant access
    // regardless of the wrong ?token=). The clean context isolates the
    // test to query-token auth only, which is what we're guarding.
    const anon = await pwRequest.newContext({ baseURL });
    try {
      // Sanity: token A on file A works (HTTP 200/206 for partial range).
      const okR = await anon.get(`/media/stream/${a.fileId}?token=${a.streamToken}`, {
        headers: { Range: 'bytes=0-99' },
      });
      expect(
        [200, 206],
        `file A's token on file A must succeed (got ${okR.status()})`,
      ).toContain(okR.status());

      // Cross-file misuse: token A on file B must reject. 401 is canonical
      // (token exists but its file_id claim doesn't match the URL param).
      const xR = await anon.get(`/media/stream/${b.fileId}?token=${a.streamToken}`, {
        headers: { Range: 'bytes=0-99' },
      });
      const diag = `fileA=${a.fileId} fileB=${b.fileId} got=${xR.status()}`;
      expect(
        [401, 403],
        `file A's token on file B must reject; cross-file token misuse is the boundary this test guards. ${diag}`,
      ).toContain(xR.status());
      expect(xR.status(), `cross-file misuse must NOT return content. ${diag}`).not.toBe(200);
      expect(xR.status(), `cross-file misuse must NOT return content. ${diag}`).not.toBe(206);
    } finally {
      await anon.dispose();
    }
  });

  test('stream token cannot satisfy Bearer auth on API endpoints (purpose claim enforced)', async ({
    request,
    baseURL,
  }) => {
    // The stream token is minted with `purpose="stream"` — the asset
    // middleware accepts it on `/media/stream/{id}` + the trickplay path,
    // but the general Bearer middleware on `/api/v1/*` must reject it.
    // Without this, a leaked stream URL would grant full API access for
    // 24 h instead of just file-scoped read.
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const accessToken: string = loginData.access_token;

    const pair = await findTwoFiles(request, accessToken);
    if (!pair) {
      test.skip(true, 'No file with a stream_token available');
      return;
    }
    const streamTok = pair.a.streamToken;

    // Use a fresh anonymous context — the `request` fixture's session
    // cookie from login above would otherwise satisfy auth on
    // /api/v1/* regardless of what's in the Authorization header,
    // masking a broken purpose-claim check.
    const anon = await pwRequest.newContext({ baseURL });
    try {
      for (const path of [
        '/api/v1/libraries',
        '/api/v1/users/me/preferences',
        '/api/v1/hub',
      ]) {
        const r = await anon.get(path, {
          headers: { Authorization: `Bearer ${streamTok}` },
        });
        expect(
          [401, 403],
          `${path} must reject a stream-purpose token (got ${r.status()})`,
        ).toContain(r.status());
      }
    } finally {
      await anon.dispose();
    }
  });

  test('item endpoint mints a fresh stream token per call (no caching)', async ({ request }) => {
    // The 24h expiry on the PASETO can't be checked client-side
    // (v4.local is encrypted, opaque without the server key). What we
    // CAN check as a contract is that each /items/{id} call returns a
    // freshly-minted token rather than echoing a cached one — that's
    // the gate that lets clients refresh on demand without a server
    // restart. If a cache silently shipped, every call would return
    // the same string and we'd never catch the staleness.
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    const pair = await findTwoFiles(request, token);
    if (!pair) {
      test.skip(true, 'No file with a stream_token available');
      return;
    }

    const seen = new Set<string>();
    for (let i = 0; i < 3; i++) {
      const r = await request.get(`/api/v1/items/${pair.a.itemId}`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      expect(r.status()).toBe(200);
      const { data: detail } = await r.json();
      const tok = (detail.files as any[])?.[0]?.stream_token;
      expect(tok, 'every file response must include a stream_token').toBeTruthy();
      expect(tok, 'tokens must be PASETO v4.local').toMatch(/^v4\.local\./);
      seen.add(tok);
    }
    expect(
      seen.size,
      `expected 3 distinct tokens across 3 calls; got ${seen.size} unique. Caching the stream token defeats the per-call mint that lets clients refresh without server restart.`,
    ).toBe(3);
  });
});
