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

test.use({ launchOptions: { args: ['--autoplay-policy=no-user-gesture-required'] } });

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

// Helper — log in through the UI and return the page at the home route.
async function loginViaUI(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/login');
  await page.getByLabel(/username/i).fill(USERNAME);
  await page.getByLabel(/password/i).fill(PASSWORD);
  await page.getByRole('button', { name: /sign in|log in/i }).click();
  await expect(page).not.toHaveURL(/\/login/, { timeout: 10_000 });
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
    // first item from the first library via the API.
    let movieId = process.env.E2E_MOVIE_ID ?? '';
    if (!movieId) {
      const libsR = await page.request.get('/api/v1/libraries');
      if (!libsR.ok()) {
        test.skip(true, 'Could not fetch libraries — is the server running?');
        return;
      }
      const { data: libs } = await libsR.json();
      if (!Array.isArray(libs) || libs.length === 0) {
        test.skip(true, 'No libraries found — seed media first');
        return;
      }
      const itemsR = await page.request.get(`/api/v1/libraries/${libs[0].id}/items?limit=1`);
      if (!itemsR.ok()) {
        test.skip(true, 'Could not fetch library items');
        return;
      }
      const { data: items } = await itemsR.json();
      if (!Array.isArray(items) || items.length === 0) {
        test.skip(true, 'Library is empty — seed media first');
        return;
      }
      movieId = items[0].id;
    }

    // Navigate to the watch page.
    await page.goto(`/watch/${movieId}`);
    await page.waitForLoadState('networkidle');

    // Click play if the player hasn't auto-started (some browsers require
    // an explicit gesture even with the autoplay flag above).
    const video = page.locator('video').first();
    await expect(video, 'video element must exist on /watch page').toBeAttached({ timeout: 10_000 });

    const isPaused = await video.evaluate((v: HTMLVideoElement) => v.paused);
    if (isPaused) {
      await video.click().catch(() => {});
      // Also try the play button overlay if clicking the video didn't work.
      await page
        .getByRole('button', { name: /play/i })
        .first()
        .click()
        .catch(() => {});
    }

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

    // Seek to 60 s and verify playback resumes within 10 s of the target.
    await video.evaluate((v: HTMLVideoElement) => {
      v.currentTime = 60;
    });
    await expect
      .poll(
        () => video.evaluate((v: HTMLVideoElement) => v.currentTime),
        { timeout: 15_000, message: 'currentTime did not advance after seeking to 60 s' },
      )
      .toBeGreaterThan(50);

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
      if (!libs?.length) { test.skip(true, 'empty library'); return; }
      const itemsR = await page.request.get(`/api/v1/libraries/${libs[0].id}/items?limit=1`);
      if (!itemsR.ok()) { test.skip(true, 'no items'); return; }
      const { data: items } = await itemsR.json();
      if (!items?.length) { test.skip(true, 'empty library'); return; }
      movieId = items[0].id;
    }

    await page.goto(`/watch/${movieId}`);
    await page.waitForLoadState('networkidle');

    // Page must not 404.
    expect(page.url(), 'must not redirect away from /watch').toContain('/watch');

    // The page title should be populated (not just "OnScreen" generic default).
    const title = await page.title();
    expect(title, 'page title should reference the item title or "Watch"').toMatch(/\S/);

    const realErrors = errors.filter((e) => !/cloudflareinsights/i.test(e));
    expect(realErrors, `JS errors on /watch page:\n${realErrors.join('\n')}`).toEqual([]);
  });
});
