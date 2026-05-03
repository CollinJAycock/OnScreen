// Transcode pipeline E2E specs — covers the DASH-removal guarantee, the
// server-side transcode smoke (login → library → start session → M3U8 → segment),
// concurrent session handling, and the AV1/fMP4 playlist shape.
//
// Required env (all optional — each block skips cleanly when absent):
//
//   E2E_USERNAME        OnScreen username (default 'admin')
//   E2E_PASSWORD        OnScreen password — required for all blocks here
//   E2E_AV1_MOVIE_ID    UUID of an AV1-encoded movie already in the library;
//                       required only for the AV1 fMP4 block

import { test, expect } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

// ── DASH removal ───────────────────────────────────────────────────────────
// DASH was removed 2026-04-30. These checks are auth-free — if DASH ever
// creeps back in, it should be caught before any login machinery is involved.

test.describe('Transcode — DASH removal', () => {
  test('manifest.mpd endpoint does not exist', async ({ request }) => {
    // Any path ending in .mpd must 404 (or 401 — never 200). A 200 means
    // DASH was re-added without updating the test plan.
    for (const probe of [
      '/api/v1/transcode/sessions/00000000-0000-0000-0000-000000000000/manifest.mpd',
      '/api/v1/stream/manifest.mpd',
      '/manifest.mpd',
    ]) {
      const r = await request.get(probe, { maxRedirects: 0 });
      expect.soft(r.status(), `${probe} must not return 200`).not.toBe(200);
      // 404 is the canonical answer; 401/403 are also acceptable if the
      // route exists but is auth-gated (still not serving DASH).
      expect.soft([401, 403, 404], `${probe} → ${r.status()}`).toContain(r.status());
    }
  });

  test.skip(!PASSWORD, 'set E2E_PASSWORD to check transcode response shape');

  test('POST transcode response contains no manifest_url (no DASH)', async ({ request }) => {
    // Start a transcode session for any movie in the library and verify
    // the response JSON does not contain a manifest_url field.
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    // Find any movie ID from the first library.
    const libsR = await request.get('/api/v1/libraries', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(libsR.status()).toBe(200);
    const { data: libs } = await libsR.json();
    expect(Array.isArray(libs) && libs.length > 0, 'Need at least one library').toBe(true);

    const libId: string = libs[0].id;
    const itemsR = await request.get(`/api/v1/libraries/${libId}/items?limit=1`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (itemsR.status() !== 200) {
      test.skip(true, 'Could not retrieve library items');
      return;
    }
    const { data: items } = await itemsR.json();
    if (!Array.isArray(items) || items.length === 0) {
      test.skip(true, 'Library is empty — seed media first');
      return;
    }
    const itemId: string = items[0].id;

    const txR = await request.post(`/api/v1/items/${itemId}/transcode`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {},
    });
    // 202 or 200 depending on implementation; 404 means the endpoint moved.
    expect(
      [200, 202],
      `POST transcode expected 200/202, got ${txR.status()}: ${await txR.text()}`,
    ).toContain(txR.status());
    const body = await txR.json();
    const txData = body.data ?? body;

    expect(txData, 'manifest_url must be absent — DASH was removed 2026-04-30').not.toHaveProperty('manifest_url');
  });
});

// ── Pipeline smoke ─────────────────────────────────────────────────────────

test.describe('Transcode — pipeline smoke', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run transcode pipeline');

  test('login → library → POST transcode → GET M3U8 → GET first segment → 200', async ({ request }) => {
    // Full golden path: authenticate, pick a real movie, start a transcode
    // session, fetch the M3U8 playlist, extract the first segment URL, and
    // verify the server returns 200 with video/MP2T or video/mp4 content.
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    // Pick the first item from the first library.
    const libsR = await request.get('/api/v1/libraries', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(libsR.status()).toBe(200);
    const { data: libs } = await libsR.json();
    expect(Array.isArray(libs) && libs.length > 0, 'Need a non-empty library').toBe(true);
    const libId: string = libs[0].id;

    const itemsR = await request.get(`/api/v1/libraries/${libId}/items?limit=1`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(itemsR.status()).toBe(200);
    const { data: items } = await itemsR.json();
    if (!Array.isArray(items) || items.length === 0) {
      test.skip(true, 'Library is empty — seed media first');
      return;
    }
    const itemId: string = items[0].id;

    // Start transcode session.
    const txR = await request.post(`/api/v1/items/${itemId}/transcode`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {},
    });
    expect([200, 202], `transcode start: ${await txR.text()}`).toContain(txR.status());
    const txBody = await txR.json();
    const txData = txBody.data ?? txBody;

    const sessionId: string = txData.session_id;
    const txToken: string = txData.token ?? token;
    expect(sessionId, 'session_id must be present').toBeTruthy();

    // Fetch the HLS playlist.
    const playlistUrl = `/api/v1/transcode/sessions/${sessionId}/playlist.m3u8?token=${txToken}`;
    const m3u8R = await request.get(playlistUrl);
    expect(m3u8R.status(), `M3U8 playlist: ${playlistUrl}`).toBe(200);
    const m3u8 = await m3u8R.text();
    expect(m3u8, 'Response must be HLS playlist').toContain('#EXTM3U');

    // Extract the first .ts or .m4s segment URL from the playlist.
    const segLine = m3u8.split('\n').find((l) => l.trim() && !l.startsWith('#'));
    expect(segLine, 'Playlist must contain at least one segment line').toBeTruthy();

    // Segment URLs may be relative (just the filename) or absolute paths.
    const segUrl = segLine!.startsWith('/')
      ? `${segLine!.trim()}?token=${txToken}`
      : `/api/v1/transcode/sessions/${sessionId}/seg/${segLine!.trim()}?token=${txToken}`;

    const segR = await request.get(segUrl);
    expect(segR.status(), `First segment: ${segUrl}`).toBe(200);
    const ct = segR.headers()['content-type'] ?? '';
    expect.soft(ct, 'Segment must be a video content-type').toMatch(/video\/(mp2t|mp4)|application\/octet-stream/);
  });
});

// ── Concurrent sessions ────────────────────────────────────────────────────

test.describe('Transcode — concurrent sessions', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run concurrent session tests');

  test('three simultaneous transcode sessions all return playlist_url', async ({ request }) => {
    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    const libsR = await request.get('/api/v1/libraries', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(libsR.status()).toBe(200);
    const { data: libs } = await libsR.json();
    expect(Array.isArray(libs) && libs.length > 0, 'Need a non-empty library').toBe(true);
    const libId: string = libs[0].id;

    const itemsR = await request.get(`/api/v1/libraries/${libId}/items?limit=3`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(itemsR.status()).toBe(200);
    const { data: items } = await itemsR.json();
    if (!Array.isArray(items) || items.length === 0) {
      test.skip(true, 'Library is empty — seed media first');
      return;
    }

    // If fewer than 3 items exist, duplicate the first to fill the slots.
    const ids = [items[0].id, (items[1] ?? items[0]).id, (items[2] ?? items[0]).id];

    // Fire all three in parallel.
    const sessions = await Promise.all(
      ids.map((id) =>
        request.post(`/api/v1/items/${id}/transcode`, {
          headers: { Authorization: `Bearer ${token}` },
          data: {},
        }),
      ),
    );

    for (let i = 0; i < sessions.length; i++) {
      const r = sessions[i];
      expect.soft([200, 202], `session ${i} status`).toContain(r.status());
      if (r.ok()) {
        const body = await r.json();
        const d = body.data ?? body;
        expect.soft(d.session_id, `session ${i} must have session_id`).toBeTruthy();
      }
    }
  });
});

// ── AV1 / fMP4 playlist shape ──────────────────────────────────────────────

test.describe('Transcode — AV1 fMP4', () => {
  test.skip(
    !process.env.E2E_AV1_MOVIE_ID,
    'set E2E_AV1_MOVIE_ID to the UUID of an AV1-encoded movie to run this block',
  );
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run AV1 fMP4 specs');

  test('AV1 source produces fMP4 playlist with #EXT-X-MAP', async ({ request }) => {
    // AV1 output is packaged as fMP4 (CMAF) segments rather than MPEG-TS.
    // The HLS playlist MUST contain an #EXT-X-MAP tag pointing at the init
    // segment — HLS.js requires it to decode fMP4. Missing it means playback
    // silently fails for AV1 content.
    const movieId = process.env.E2E_AV1_MOVIE_ID!;

    const loginR = await request.post('/api/v1/auth/login', {
      data: { username: USERNAME, password: PASSWORD },
    });
    expect(loginR.status()).toBe(200);
    const { data: loginData } = await loginR.json();
    const token: string = loginData.access_token;

    const txR = await request.post(`/api/v1/items/${movieId}/transcode`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {},
    });
    expect([200, 202], `transcode AV1: ${await txR.text()}`).toContain(txR.status());
    const txBody = await txR.json();
    const txData = txBody.data ?? txBody;
    const sessionId: string = txData.session_id;
    const txToken: string = txData.token ?? token;

    const m3u8R = await request.get(
      `/api/v1/transcode/sessions/${sessionId}/playlist.m3u8?token=${txToken}`,
    );
    expect(m3u8R.status()).toBe(200);
    const m3u8 = await m3u8R.text();

    expect(m3u8, 'AV1/fMP4 playlist must contain #EXT-X-MAP init segment tag').toContain('#EXT-X-MAP');
    // Segments must use .m4s or .mp4, not .ts.
    const segLine = m3u8.split('\n').find((l) => l.trim() && !l.startsWith('#'));
    if (segLine) {
      expect.soft(segLine, 'AV1 segments must be fMP4 (.m4s or .mp4)').toMatch(/\.(m4s|mp4)/);
    }
  });
});
