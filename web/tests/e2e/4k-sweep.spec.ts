// Overnight 4K playback sweep — plays the first minutes of every 4K movie on
// the target server and verifies BOTH video and audio actually start and
// sustain, in a real Chromium. Validates the transcode pipeline (HEVC→H.264 +
// HDR tonemap + lossless-audio→AAC) across the whole 4K library.
//
// Opt-in only (never runs in the normal suite): set RUN_4K_SWEEP=1.
//   RUN_4K_SWEEP=1 BASE_URL=https://onscreen.wolverscreen.com \
//   E2E_USERNAME=collinAycock E2E_PASSWORD=... \
//   npx playwright test 4k-sweep --project=chromium
//
// Verification (no Web Audio — avoids colliding with the player's audio graph):
//   video → webkitVideoDecodedByteCount grows AND currentTime advances
//   audio → webkitAudioDecodedByteCount grows (proves an audio track is decoding)
//
// Reliability: between movies we navigate home and SETTLE so the GPU/worker
// fully recovers from the prior transcode's teardown (back-to-back start→stop→
// start otherwise degrades transcode startup and produces false "didn't start"
// failures — observed: a movie that starts in ~5s solo timing out at 45s when
// cycled). A failed attempt is retried once after an extra settle, so a transient
// slow start isn't recorded as a real failure. Results stream to
// 4k-sweep-results.json incrementally so a crash/kill keeps everything gathered.

import { test, expect, type Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';

const specDir = path.dirname(fileURLToPath(import.meta.url));

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';
const PLAYBACK_SECONDS = Number(process.env.SWEEP_PLAYBACK_SECONDS ?? 90);
const STARTUP_TIMEOUT = Number(process.env.SWEEP_STARTUP_TIMEOUT ?? 75) * 1000;
const SETTLE_MS = Number(process.env.SWEEP_SETTLE_SECONDS ?? 12) * 1000;
const DEADLINE_MS = Number(process.env.SWEEP_DEADLINE_HOURS ?? 9) * 3600 * 1000;
const LIMIT = Number(process.env.SWEEP_LIMIT ?? 0); // 0 = all

const moviesPath = process.env.SWEEP_MOVIES
  ? path.resolve(process.env.SWEEP_MOVIES)
  : path.join(specDir, '4k-movies.json');
const resultsPath = path.join(specDir, '4k-sweep-results.json');
const movies: any[] = fs.existsSync(moviesPath)
  ? JSON.parse(fs.readFileSync(moviesPath, 'utf-8'))
  : [];

async function loginViaUI(page: Page) {
  await page.goto('/login', { waitUntil: 'domcontentloaded' });
  await page.getByLabel(/username/i).fill(USERNAME);
  await page.getByLabel(/password/i).fill(PASSWORD);
  await page.locator('button[type="submit"]').first().click();
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
}

async function sampleMedia(page: Page) {
  return page.locator('video').first().evaluate((v: any) => ({
    currentTime: v.currentTime,
    duration: v.duration,
    readyState: v.readyState,
    vBytes: typeof v.webkitVideoDecodedByteCount === 'number' ? v.webkitVideoDecodedByteCount : -1,
    aBytes: typeof v.webkitAudioDecodedByteCount === 'number' ? v.webkitAudioDecodedByteCount : -1,
    audioTracks: v.audioTracks ? v.audioTracks.length : -1,
    paused: v.paused,
    error: v.error ? v.error.code : 0,
  }));
}

test.describe('4K playback sweep @chromium-only', () => {
  test.skip(!process.env.RUN_4K_SWEEP, 'set RUN_4K_SWEEP=1 to run the overnight 4K sweep');
  test.skip(!PASSWORD, 'set E2E_PASSWORD');

  test('play first minutes of every 4K movie, verify audio+video', async ({ page }) => {
    test.setTimeout(10 * 3600 * 1000); // 10h hard cap
    const startWall = Date.now();
    await loginViaUI(page);

    let sid = '';
    const shadow: string[] = []; // [capability-shadow] mismatch/failure lines (server-vs-local decision)
    page.on('request', (req) => {
      const m = req.url().match(/transcode\/sessions\/([0-9a-f-]+)\//);
      if (m) sid = m[1];
    });

    // Optional: inject a max_audio_channels cap into the transcode-start request.
    // Verifies the 7.1→5.1 fix WITHOUT a redeploy — the server honors a client-
    // declared max, so SWEEP_FORCE_MAX_CHANNELS=6 reproduces what the
    // maxAACChannels=6 change does by default.
    if (process.env.SWEEP_FORCE_MAX_CHANNELS) {
      const mc = Number(process.env.SWEEP_FORCE_MAX_CHANNELS);
      await page.route('**/api/v1/items/*/transcode', async (route) => {
        if (route.request().method() !== 'POST') { await route.continue(); return; }
        let body: any = {};
        try { body = JSON.parse(route.request().postData() || '{}'); } catch { /* keep {} */ }
        body.max_audio_channels = mc;
        await route.continue({ postData: JSON.stringify(body) });
      });
    }

    // Navigate home + pause so the previous transcode's teardown completes and
    // the GPU/worker is idle before the next start.
    async function settle() {
      await page.goto('/', { waitUntil: 'domcontentloaded' }).catch(() => {});
      await page.waitForTimeout(SETTLE_MS);
    }

    async function attempt(mv: any) {
      const errors: string[] = [];
      const onErr = (msg: any) => {
        const txt = msg.text();
        // Filter third-party noise by the message's ORIGIN URL: a failed
        // subresource reports as a bare "Failed to load resource: net::ERR_…"
        // with no URL in the text, so filtering on text alone never matches
        // (the edge-injected Cloudflare beacon is the usual culprit).
        const url = msg.location?.()?.url ?? '';
        const noisy = /cloudflareinsights|cloudflare\.com|gstatic\.com/i.test(url + ' ' + txt);
        if (msg.type() === 'error' && !noisy) errors.push(url ? `${txt}  [${url}]` : txt);
        if (txt.includes('[capability-shadow]')) shadow.push(txt);
      };
      const onPageErr = (e: any) => errors.push(e.message);
      page.on('console', onErr);
      page.on('pageerror', onPageErr);
      sid = '';
      const res: any = {
        video_started: false, audio_started: false, sustained: false,
        t0: null, t1: null, session_id: '', errors: [] as string[], note: '',
      };
      try {
        await page.goto(`/watch/${mv.id}`, { waitUntil: 'domcontentloaded', timeout: 30_000 });
        const playBtn = page.getByRole('button', { name: /^(play|resume)\b/i }).first();
        await playBtn.waitFor({ state: 'visible', timeout: 15_000 });
        await playBtn.click();
        const video = page.locator('video').first();
        await video.waitFor({ state: 'attached', timeout: 30_000 });
        await expect
          .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: STARTUP_TIMEOUT })
          .toBeGreaterThan(0);
        const t0 = await sampleMedia(page);
        res.t0 = t0;
        const end = Date.now() + PLAYBACK_SECONDS * 1000;
        let last = t0;
        while (Date.now() < end) {
          await page.waitForTimeout(10_000);
          last = await sampleMedia(page);
          if (last.error) break;
        }
        res.t1 = last;
        res.video_started = last.vBytes > t0.vBytes && last.currentTime > t0.currentTime + 10;
        res.audio_started = last.aBytes > t0.aBytes && last.aBytes > 0;
        res.sustained = last.currentTime >= t0.currentTime + PLAYBACK_SECONDS * 0.6;
        res.session_id = sid;
      } catch (e: any) {
        res.note = `exception: ${String(e?.message ?? e).slice(0, 240)}`;
        res.session_id = sid;
      }
      res.errors = errors.slice(0, 6); // already noise-filtered by origin in onErr
      res.pass = res.video_started && res.audio_started;
      if (sid) { try { await page.request.delete(`/api/v1/transcode/sessions/${sid}`); } catch { /* idle-reaps */ } }
      page.off('console', onErr);
      page.off('pageerror', onPageErr);
      return res;
    }

    const list = LIMIT > 0 ? movies.slice(0, LIMIT) : movies;
    const results: any[] = [];

    for (let i = 0; i < list.length; i++) {
      const mv = list[i];
      if (Date.now() - startWall > DEADLINE_MS) {
        console.log(`[deadline] stopping at ${i}/${list.length}`);
        break;
      }
      await settle();
      let a = await attempt(mv);
      if (!a.pass) {
        await settle(); // extra recovery, then one retry — separates transient from real
        a = await attempt(mv);
        a.retried = true;
      }
      const r = {
        idx: i + 1, id: mv.id, title: mv.title, year: mv.year,
        video_codec: mv.video_codec, audio_codec: mv.audio_codec, bit_depth: mv.video_bit_depth,
        ...a,
      };
      results.push(r);
      fs.writeFileSync(resultsPath, JSON.stringify(results, null, 2));
      const mins = ((Date.now() - startWall) / 60000).toFixed(0);
      const aDelta = r.t1 && r.t0 ? r.t1.aBytes - r.t0.aBytes : 'n/a';
      console.log(`[${i + 1}/${list.length} ${mins}m] ${r.pass ? 'PASS' : 'FAIL'} "${mv.title}" (${mv.audio_codec}) v=${r.video_started} a=${r.audio_started} sust=${r.sustained}${r.retried ? ' (retried)' : ''} aΔ=${aDelta} ${r.note}`);
    }

    const fails = results.filter((r) => !r.pass);
    console.log(`\n=== SWEEP DONE: ${results.length} played, ${fails.length} failures ===`);
    for (const f of fails) {
      console.log(`  FAIL "${f.title}" (a=${f.audio_codec}/v=${f.video_codec}) video=${f.video_started} audio=${f.audio_started} sess=${f.session_id} ${f.note} | ${f.errors.join(' || ')}`);
    }

    // Capability-profile parity: server-vs-local decision mismatches observed in
    // shadow mode. Empty = the server decision agrees with the local one across
    // the swept library (safe to make the server authoritative). Each line tells
    // which file class diverged (codec/audio/container/bitdepth/hdr/faststart).
    const uniqShadow = [...new Set(shadow)];
    console.log(`\n=== capability-shadow: ${uniqShadow.length} decision divergence line(s) ===`);
    for (const s of uniqShadow) console.log('  ' + s);
    fs.writeFileSync(path.join(specDir, '4k-shadow-mismatches.json'), JSON.stringify(uniqShadow, null, 2));
  });
});
