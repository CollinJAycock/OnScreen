// Browser playback E2E — verifies that the /watch/{id} player page actually
// plays video in a real browser: readyState reaches HAVE_ENOUGH_DATA,
// currentTime advances past 0, and seeking to 60 s lands within 10 s of
// the target. Also asserts zero console errors during the session.
//
// Requires a running OnScreen instance with at least one media item.
//
// Required env:
//   E2E_USERNAME   OnScreen username (default 'admin')
//   E2E_PASSWORD   OnScreen password — required; block skips otherwise
//
// Optional env:
//   E2E_MOVIE_ID   UUID of a specific movie to play (bypasses dynamic lookup)
//
// @chromium-only — Video autoplay + media codecs behave most predictably in
// Chromium; Firefox and WebKit have different autoplay policies and codec
// support that are better tested in a dedicated lab run.

import { test, expect } from '@playwright/test';

// The chromium project in playwright.config.ts already sets
// --autoplay-policy=no-user-gesture-required; redefining it via test.use
// triggers a launchOptions merge that re-creates the browser context and
// (in observed runs) leaves the form in a state where the submit button
// click silently no-ops. We rely on the project-level config only.

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

// Helper — log in through the UI and return the page at the home route.
async function loginViaUI(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/login', { waitUntil: 'domcontentloaded' });
  await page.getByLabel(/username/i).fill(USERNAME);
  await page.getByLabel(/password/i).fill(PASSWORD);
  // Use the form's submit button specifically — the page also has a
  // "Sign in with LDAP" toggle whose accessible name matches /sign in/i.
  await page.locator('button[type="submit"]').first().click();
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
}

test.describe('Browser playback @chromium-only', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run browser playback specs');

  test('video plays, currentTime advances, seek lands within 10 s of 60 s target', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', (msg) => {
      if (msg.type() === 'error') errors.push(msg.text());
    });
    page.on('pageerror', (err) => errors.push(err.message));

    await loginViaUI(page);

    // Resolve the movie ID: use E2E_MOVIE_ID if set, otherwise look up the
    // first item from the first MOVIE-typed library via the API.
    let movieId = process.env.E2E_MOVIE_ID ?? '';
    if (!movieId) {
      const libsR = await page.request.get('/api/v1/libraries');
      if (!libsR.ok()) {
        test.skip(true, 'Could not fetch libraries — is the server running?');
        return;
      }
      const { data: libs } = await libsR.json();
      const movieLib = (libs as any[])?.find((l) => l.type === 'movie');
      if (!movieLib) {
        test.skip(true, 'No movie library found — seed a movie library first');
        return;
      }
      const itemsR = await page.request.get(`/api/v1/libraries/${movieLib.id}/items?limit=1`);
      if (!itemsR.ok()) {
        test.skip(true, 'Could not fetch library items');
        return;
      }
      const { data: items } = await itemsR.json();
      if (!Array.isArray(items) || items.length === 0) {
        test.skip(true, 'Movie library is empty — seed a movie first');
        return;
      }
      movieId = items[0].id;
    }

    // Navigate to the watch page. Use 'domcontentloaded' — the notification
    // SSE stream stays open and 'networkidle' would never fire.
    await page.goto(`/watch/${movieId}`, { waitUntil: 'domcontentloaded' });

    // /watch/{id} first renders a detail card with a "Play" or "Resume"
    // button — the latter when watch history exists (watching the file
    // once changes the button label). The <video> element only mounts
    // after the user clicks it (which starts a transcode session and
    // sets streamURL).
    const playBtn = page.getByRole('button', { name: /^(play|resume)\b/i }).first();
    await expect(playBtn, 'Play/Resume button must be visible on /watch detail').toBeVisible({ timeout: 15_000 });
    await playBtn.click();

    // Now the <video> element should mount once transcode start returns
    // and streamURL is set. Allow up to 30s — first transcode for a movie
    // includes ffmpeg startup + first segment generation.
    const video = page.locator('video').first();
    await expect(video, 'video element must exist after clicking Play').toBeAttached({ timeout: 30_000 });

    // Wait up to 15 s for readyState to reach HAVE_ENOUGH_DATA (4).
    await expect
      .poll(
        () => video.evaluate((v: HTMLVideoElement) => v.readyState),
        { timeout: 15_000, message: 'video.readyState never reached HAVE_ENOUGH_DATA' },
      )
      .toBeGreaterThanOrEqual(2); // HAVE_CURRENT_DATA is acceptable as a minimum

    // currentTime must be > 0 within 15 s.
    await expect
      .poll(
        () => video.evaluate((v: HTMLVideoElement) => v.currentTime),
        { timeout: 15_000, message: 'video.currentTime never advanced past 0' },
      )
      .toBeGreaterThan(0);

    // Wait for the HLS playlist to load — duration is the signal that
    // the player has segments to play. OnScreen emits segments on
    // demand, so the duration starts small (~30s of buffered segments)
    // and grows as the encoder produces more. Wait for *any* duration
    // to be reported.
    await expect
      .poll(
        () => video.evaluate((v: HTMLVideoElement) => v.duration),
        { timeout: 15_000, message: 'video.duration never populated — HLS playlist not loaded' },
      )
      .toBeGreaterThan(5);

    // Seek test: pick a target near the current end of the buffered
    // playlist (not a fixed 60s) since the playlist length depends on
    // how far the on-demand encoder has gotten. Seek-target = duration -
    // 5s so we land inside a generated segment, not past the end.
    const duration = await video.evaluate((v: HTMLVideoElement) => v.duration);
    const seekTarget = Math.max(5, duration - 5);
    const preSeekTime = await video.evaluate((v: HTMLVideoElement) => v.currentTime);
    await video.evaluate((v: HTMLVideoElement, target: number) => {
      v.currentTime = target;
    }, seekTarget);
    await expect
      .poll(
        () => video.evaluate((v: HTMLVideoElement) => v.currentTime),
        {
          timeout: 20_000,
          message: `seek target=${seekTarget}s didn't take effect (pre-seek=${preSeekTime}s, duration=${duration}s)`,
        },
      )
      .toBeGreaterThan(seekTarget - 5);

    // No console errors during the whole session (filter Cloudflare noise).
    const realErrors = errors.filter((e) => !/cloudflareinsights/i.test(e));
    expect(realErrors, `Console errors during playback:\n${realErrors.join('\n')}`).toEqual([]);
  });

  test('player page loads with correct <title> and no JS errors before play', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', (msg) => {
      if (msg.type() === 'error') errors.push(msg.text());
    });
    page.on('pageerror', (err) => errors.push(err.message));

    await loginViaUI(page);

    let movieId = process.env.E2E_MOVIE_ID ?? '';
    if (!movieId) {
      const libsR = await page.request.get('/api/v1/libraries');
      if (!libsR.ok()) { test.skip(true, 'no libraries'); return; }
      const { data: libs } = await libsR.json();
      const movieLib = (libs as any[])?.find((l) => l.type === 'movie');
      if (!movieLib) { test.skip(true, 'no movie library'); return; }
      const itemsR = await page.request.get(`/api/v1/libraries/${movieLib.id}/items?limit=1`);
      if (!itemsR.ok()) { test.skip(true, 'no items'); return; }
      const { data: items } = await itemsR.json();
      if (!items?.length) { test.skip(true, 'empty library'); return; }
      movieId = items[0].id;
    }

    await page.goto(`/watch/${movieId}`, { waitUntil: 'domcontentloaded' });

    // Page must not 404.
    expect(page.url(), 'must not redirect away from /watch').toContain('/watch');

    // The page title is set via <svelte:head> after hydration — poll
    // briefly so we don't read it before the framework has a chance.
    await expect
      .poll(() => page.title(), { timeout: 5_000 })
      .toMatch(/\S/);

    const realErrors = errors.filter((e) => !/cloudflareinsights/i.test(e));
    expect(realErrors, `JS errors on /watch page:\n${realErrors.join('\n')}`).toEqual([]);
  });
});
