// Gapless rollover — the regression catcher.
//
// Two assertions stacked from cheap to expensive:
//   1. After auto-advance, the *active* <audio> element has volume > 0.
//      Catches the AudioPlayer Svelte dep-tracking bug exactly.
//   2. (Chromium only) An AudioContext analyser tap on each <audio>
//      element confirms the next track produces non-silent output
//      within 500ms of the transition. Catches future bugs where the
//      element has the right volume but is muted somewhere downstream.
//
// Drives through the real UI (login form + album page click) so the
// test validates the production-shaped artifact, not a dev-only hook.
//
// Requires a running OnScreen server with a music album of >= 2 tracks
// already present. Pass the album item id via E2E_GAPLESS_ALBUM.

import { test, expect, type Page } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';
const ALBUM_ID = process.env.E2E_GAPLESS_ALBUM ?? '';

async function loginViaUI(page: Page) {
  await page.goto('/login');
  await page.getByLabel(/username/i).fill(USERNAME);
  await page.getByLabel(/password/i).fill(PASSWORD);
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

async function startAlbumPlayback(page: Page, albumId: string) {
  // Use 'domcontentloaded' — the notifications SSE stream stays open and
  // 'networkidle' would never fire.
  await page.goto(`/albums/${albumId}`, { waitUntil: 'domcontentloaded' });
  // The album page has a "Play album" button (.btn-play) — primary action.
  // Wait until it's actually attached before clicking.
  await page.locator('button.btn-play').first().waitFor({ state: 'visible', timeout: 15_000 });
  await page.locator('button.btn-play').first().click();
  // Wait for the AudioPlayer chrome to mount (it only renders when a track
  // is loaded). Its <audio> elements are the ones we instrument.
  await expect.poll(
    async () => page.evaluate(() => document.querySelectorAll('audio').length),
    { timeout: 10_000 },
  ).toBeGreaterThanOrEqual(2);
}

// Force a rollover quickly: seek the active element close to the end so
// 'ended' fires within seconds instead of waiting for the real track length.
async function seekActiveNearEnd(page: Page) {
  await page.evaluate(() => {
    const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
    // The active element is the one currently playing (volume > 0 AND has src).
    // Fallback: not-paused.
    const active =
      els.find((e) => e.src && e.volume > 0 && !e.paused) ??
      els.find((e) => e.src && !e.paused);
    if (active && Number.isFinite(active.duration) && active.duration > 0) {
      active.currentTime = Math.max(0, active.duration - 0.4);
    }
  });
}

test.describe('Gapless rollover', () => {
  test.skip(!PASSWORD || !ALBUM_ID, 'set E2E_PASSWORD + E2E_GAPLESS_ALBUM to run');

  test('next track plays at full volume after auto-advance', async ({ page, browserName }) => {
    // WebKit/Safari has spotty support for high-resolution FLAC (24-bit/192kHz
    // is the case for Pink Floyd reissues used in our test data) — duration
    // metadata never populates and the test can't seek-near-end. Skip; the
    // gapless behavior is exercised by chromium + firefox.
    test.skip(browserName === 'webkit', 'WebKit cannot reliably play 24-bit/192kHz FLAC in <audio>');

    await loginViaUI(page);
    await startAlbumPlayback(page, ALBUM_ID);

    // Capture the first track's src so we can assert it changes.
    const firstSrc = await page.evaluate(() => {
      const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
      return els.find((e) => !e.paused)?.src ?? '';
    });
    expect(firstSrc).toContain('/media/stream/');

    // Wait for the active element to have valid duration so seek-near-end
    // actually lands inside the track. Without this guard, on chromium/webkit
    // the seek can fire before duration populates and the resulting
    // `Math.max(0, NaN - 0.4)` gives NaN — no 'ended' event ever fires and
    // the rollover never happens.
    await expect.poll(
      async () =>
        page.evaluate(() => {
          const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
          const a = els.find((e) => e.src && !e.paused);
          return a && Number.isFinite(a.duration) && a.duration > 0 ? a.duration : 0;
        }),
      { timeout: 15_000, message: 'active <audio>.duration never populated' },
    ).toBeGreaterThan(0);

    await seekActiveNearEnd(page);

    // Wait for the rollover — the new active element has a different src
    // than firstSrc and is not paused. Bumped to 30s to accommodate slower
    // browsers (firefox usually rolls over within 5s; chromium/webkit need
    // longer when the preload element is still buffering).
    await expect
      .poll(
        async () =>
          page.evaluate((firstSrc) => {
            const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
            const playing = els.find((e) => e.src && e.src !== firstSrc && !e.paused);
            return playing ? playing.volume : -1;
          }, firstSrc),
        {
          timeout: 30_000,
          message: 'rollover never landed on a non-zero-volume active element',
        },
      )
      .toBeGreaterThan(0);
  });

  test('@chromium-only — analyser tap proves track 2 is audibly non-silent', async ({
    page,
    browserName,
  }) => {
    test.skip(browserName !== 'chromium', 'analyser tap needs Chromium autoplay flags');
    // The OnScreen AudioPlayer wires its own AudioContext + source nodes
    // onto the <audio> elements (for ReplayGain). A second
    // createMediaElementSource() call on the same element throws
    // InvalidStateError, which our test harness swallows in its try/catch
    // — so the analyser never receives samples. Re-enable this test once
    // the AudioPlayer exposes its own AnalyserNode for taps, or once we
    // patch the player to share a single AudioContext that the harness
    // can `tap` against without re-wrapping the element.
    test.skip(true, 'analyser tap collides with AudioPlayer ReplayGain wiring — see TODO above');

    // Install the AudioContext tap before the app mounts so we catch the
    // first audio element creation.
    await page.addInitScript(() => {
      // @ts-expect-error e2e harness only
      window.__rms__ = { samples: [] as Array<{ t: number; rms: number; src: string }> };
      const Ctx = window.AudioContext || (window as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
      if (!Ctx) return;
      const ctx = new Ctx();
      const taps = new WeakMap<HTMLAudioElement, AnalyserNode>();

      function attach(el: HTMLAudioElement) {
        if (taps.has(el)) return;
        try {
          const src = ctx.createMediaElementSource(el);
          const analyser = ctx.createAnalyser();
          analyser.fftSize = 512;
          src.connect(analyser);
          analyser.connect(ctx.destination);
          taps.set(el, analyser);
        } catch {
          // Element already wired or cross-origin block — skip.
        }
      }

      const obs = new MutationObserver(() => {
        document.querySelectorAll('audio').forEach((el) => attach(el as HTMLAudioElement));
      });
      obs.observe(document.documentElement, { childList: true, subtree: true });

      setInterval(() => {
        document.querySelectorAll('audio').forEach((el) => {
          const analyser = taps.get(el as HTMLAudioElement);
          if (!analyser) return;
          const buf = new Uint8Array(analyser.fftSize);
          analyser.getByteTimeDomainData(buf);
          let sum = 0;
          for (let i = 0; i < buf.length; i++) {
            const v = buf[i] - 128;
            sum += v * v;
          }
          const rms = Math.sqrt(sum / buf.length);
          // @ts-expect-error e2e harness only
          window.__rms__.samples.push({ t: performance.now(), rms, src: (el as HTMLAudioElement).src });
        });
      }, 16);
    });

    await loginViaUI(page);
    await startAlbumPlayback(page, ALBUM_ID);

    const firstSrc = await page.evaluate(() => {
      const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
      return els.find((e) => !e.paused)?.src ?? '';
    });

    // Wait for the active element's duration to populate before seeking,
    // otherwise the seek-near-end calculation lands on NaN.
    await expect.poll(
      async () =>
        page.evaluate(() => {
          const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
          const a = els.find((e) => e.src && !e.paused);
          return a && Number.isFinite(a.duration) && a.duration > 0 ? a.duration : 0;
        }),
      { timeout: 15_000, message: 'active <audio>.duration never populated' },
    ).toBeGreaterThan(0);

    await seekActiveNearEnd(page);

    // Wait for the swap (30s — same headroom as the volume test above).
    const secondSrc = await expect
      .poll(
        async () =>
          page.evaluate((firstSrc) => {
            const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
            return els.find((e) => e.src && e.src !== firstSrc && !e.paused)?.src ?? '';
          }, firstSrc),
        { timeout: 30_000, message: 'no second track started playing' },
      )
      .not.toBe('');

    // Settle 3s — analyser samples every 16ms, so we collect ~180 samples
    // post-rollover. Track 2 needs a moment to actually start producing
    // audio on the new element after it becomes active.
    await page.waitForTimeout(3000);
    const diag = await page.evaluate((srcMatch: string) => {
      // @ts-expect-error e2e harness only
      const samples = (window.__rms__.samples ?? []) as Array<{ rms: number; src: string }>;
      const otherSrc = samples.filter((s) => s.src && s.src !== srcMatch);
      const matchSrc = samples.filter((s) => s.src === srcMatch);
      const otherMaxRms = otherSrc.reduce((m, s) => Math.max(m, s.rms), 0);
      const matchMaxRms = matchSrc.reduce((m, s) => Math.max(m, s.rms), 0);
      return {
        totalSamples: samples.length,
        matchSrcCount: matchSrc.length,
        otherSrcCount: otherSrc.length,
        matchMaxRms,
        otherMaxRms,
      };
    }, firstSrc);

    expect(
      diag.otherMaxRms,
      `track 2 element produced no audible output after rollover (diag: ${JSON.stringify(diag)})`,
    ).toBeGreaterThan(1);

    void secondSrc;
  });
});
