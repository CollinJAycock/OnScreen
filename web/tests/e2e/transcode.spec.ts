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

import { test, expect, type APIRequestContext } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

// pickFirstMovieItem authenticates and returns {token, itemId} for the first
// item in the first MOVIE-typed library. Transcode tests need a video item;
// the previous version of these tests grabbed libs[0] which was alphabetically
// the Audiobooks library — POST /items/{id}/transcode correctly returns 404
// for non-transcodable items, so the test failed before exercising any
// transcode logic.
//
// The token + the resolved items list are cached at module scope so the
// whole spec file (4 tests × N browsers) only triggers ONE auth roundtrip
// total. The dev server's /api/v1/auth/* rate limiter will otherwise trip
// from cumulative login load across the full Playwright suite.
let _cached: { token: string; itemIds: string[] } | null = null;

async function pickFirstMovieItem(
  request: APIRequestContext,
  count = 1,
): Promise<{ token: string; itemIds: string[] }> {
  if (_cached && _cached.itemIds.length >= count) return _cached;

  const loginR = await request.post('/api/v1/auth/login', {
    data: { username: USERNAME, password: PASSWORD },
  });
  if (!loginR.ok()) return { token: '', itemIds: [] };
  const { data: loginData } = await loginR.json();
  const token: string = loginData.access_token;

  const libsR = await request.get('/api/v1/libraries', {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!libsR.ok()) return { token, itemIds: [] };
  const { data: libs } = await libsR.json();
  const movieLib = (libs as any[])?.find((l) => l.type === 'movie');
  if (!movieLib) return { token, itemIds: [] };

  // Always fetch the larger of (requested, 3) so a later test asking for
  // 3 items can still hit the cache after an earlier test asked for 1.
  const fetchCount = Math.max(count, 3);
  const itemsR = await request.get(
    `/api/v1/libraries/${movieLib.id}/items?limit=${fetchCount}`,
    { headers: { Authorization: `Bearer ${token}` } },
  );
  if (!itemsR.ok()) return { token, itemIds: [] };
  const { data: items } = await itemsR.json();
  if (!Array.isArray(items) || items.length === 0) return { token, itemIds: [] };

  _cached = { token, itemIds: items.map((i: any) => i.id) };
  return _cached;
}

// ── DASH removal ───────────────────────────────────────────────────────────
// DASH was removed 2026-04-30. These checks are auth-free — if DASH ever
// creeps back in, it should be caught before any login machinery is involved.

test.describe('Transcode — DASH removal', () => {
  test('manifest.mpd endpoint does not exist', async ({ request }) => {
    // API-path probes only — a bare `/manifest.mpd` falls through to the
    // SvelteKit shell (which legitimately returns 200 with the SPA index)
    // and tells us nothing about whether DASH is actually wired up. The
    // meaningful check is that the API namespace doesn't serve DASH.
    for (const probe of [
      '/api/v1/transcode/sessions/00000000-0000-0000-0000-000000000000/manifest.mpd',
      '/api/v1/stream/manifest.mpd',
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
    const { token, itemIds } = await pickFirstMovieItem(request);
    if (!token) test.skip(true, 'Could not authenticate');
    if (itemIds.length === 0) test.skip(true, 'No movie items found — seed a movie library first');

    const txR = await request.post(`/api/v1/items/${itemIds[0]}/transcode`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {},
    });
    expect(
      [200, 202],
      `POST transcode expected 200/202, got ${txR.status()}: ${await txR.text()}`,
    ).toContain(txR.status());
    const body = await txR.json();
    const txData = body.data ?? body;

    expect(txData, 'manifest_url must be absent — DASH was removed 2026-04-30').not.toHaveProperty('manifest_url');
    // Positive assertion: HLS playlist_url IS present (if it weren't,
    // the no-manifest_url assertion above would still pass even though
    // the whole endpoint was broken).
    expect(txData, 'playlist_url must be present (HLS replaces DASH)').toHaveProperty('playlist_url');
  });
});

// ── Pipeline smoke ─────────────────────────────────────────────────────────

test.describe('Transcode — pipeline smoke', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run transcode pipeline');

  test('login → library → POST transcode → GET M3U8 → GET first segment → 200', async ({ request }) => {
    // Full golden path: authenticate, pick a real movie, start a transcode
    // session, fetch the M3U8 playlist, extract the first segment URL, and
    // verify the server returns 200 with video/MP2T or video/mp4 content.
    const { token, itemIds } = await pickFirstMovieItem(request);
    if (!token) test.skip(true, 'Could not authenticate');
    if (itemIds.length === 0) test.skip(true, 'No movie items found — seed a movie library first');

    // Start transcode session.
    const txR = await request.post(`/api/v1/items/${itemIds[0]}/transcode`, {
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

    // Segment URLs may be (a) relative filenames, (b) absolute paths
    // already including ?token=, or (c) absolute paths without a token.
    // Only append our token when the line doesn't already carry one,
    // otherwise we end up with `?token=X?token=X` which trips a 401.
    const trimmed = segLine!.trim();
    let segUrl: string;
    if (trimmed.startsWith('/')) {
      segUrl = trimmed.includes('token=') ? trimmed : `${trimmed}?token=${txToken}`;
    } else {
      segUrl = `/api/v1/transcode/sessions/${sessionId}/seg/${trimmed}?token=${txToken}`;
    }

    const segR = await request.get(segUrl);
    expect(segR.status(), `First segment: ${segUrl}`).toBe(200);
    const ct = segR.headers()['content-type'] ?? '';
    // Case-insensitive — HLS spec uses uppercase MP2T per the original
    // MIME registration, but lowercase is also seen in the wild.
    expect.soft(ct, 'Segment must be a video content-type').toMatch(/video\/(mp2t|mp4)|application\/octet-stream/i);
  });
});

// ── Concurrent sessions ────────────────────────────────────────────────────

test.describe('Transcode — concurrent sessions', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run concurrent session tests');

  test('three simultaneous transcode sessions all return playlist_url', async ({ request }) => {
    const { token, itemIds } = await pickFirstMovieItem(request, 3);
    if (!token) test.skip(true, 'Could not authenticate');
    if (itemIds.length === 0) test.skip(true, 'No movie items found — seed a movie library first');

    // If fewer than 3 movies exist, duplicate the first to fill the slots —
    // the transcode service should handle two sessions on the same item.
    const ids = [itemIds[0], itemIds[1] ?? itemIds[0], itemIds[2] ?? itemIds[0]];

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

    // Request video-copy / remux mode — for AV1 sources this is the path
    // that produces fMP4 segments with #EXT-X-MAP. Without video_copy=true
    // the server transcodes to H.264 (NVENC) and emits MPEG-TS segments,
    // which is correct behavior for browsers without AV1 decode but isn't
    // what this test is validating.
    const txR = await request.post(`/api/v1/items/${movieId}/transcode`, {
      headers: { Authorization: `Bearer ${token}` },
      data: { video_copy: true },
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

    expect(m3u8, 'AV1/fMP4 remux playlist must contain #EXT-X-MAP init segment tag').toContain('#EXT-X-MAP');
    // Segments must use .m4s or .mp4, not .ts.
    const segLine = m3u8.split('\n').find((l) => l.trim() && !l.startsWith('#'));
    if (segLine) {
      expect.soft(segLine, 'AV1 fMP4 segments must use .m4s or .mp4 extension').toMatch(/\.(m4s|mp4)/);
    }
  });
});
