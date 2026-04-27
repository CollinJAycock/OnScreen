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
  await page.getByRole('button', { name: /sign in|log in/i }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

async function startAlbumPlayback(page: Page, albumId: string) {
  await page.goto(`/albums/${albumId}`);
  await page.waitForLoadState('networkidle');
  // The album page has a "Play album" button (.btn-play) — primary action.
  await page.locator('button.btn-play').click();
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

  test('next track plays at full volume after auto-advance', async ({ page }) => {
    await loginViaUI(page);
    await startAlbumPlayback(page, ALBUM_ID);

    // Capture the first track's src so we can assert it changes.
    const firstSrc = await page.evaluate(() => {
      const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
      return els.find((e) => !e.paused)?.src ?? '';
    });
    expect(firstSrc).toContain('/media/stream/');

    await seekActiveNearEnd(page);

    // Wait for the rollover — the new active element has a different src
    // than firstSrc and is not paused.
    await expect
      .poll(
        async () =>
          page.evaluate((firstSrc) => {
            const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
            const playing = els.find((e) => e.src && e.src !== firstSrc && !e.paused);
            return playing ? playing.volume : -1;
          }, firstSrc),
        {
          timeout: 15_000,
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

    await seekActiveNearEnd(page);

    // Wait for the swap.
    const secondSrc = await expect
      .poll(
        async () =>
          page.evaluate((firstSrc) => {
            const els = Array.from(document.querySelectorAll('audio')) as HTMLAudioElement[];
            return els.find((e) => e.src && e.src !== firstSrc && !e.paused)?.src ?? '';
          }, firstSrc),
        { timeout: 15_000, message: 'no second track started playing' },
      )
      .not.toBe('');

    // Settle, then prove the analyser saw real audio on the new element.
    await page.waitForTimeout(500);
    const audible = await page.evaluate((srcMatch: string) => {
      // @ts-expect-error e2e harness only
      const samples = (window.__rms__.samples ?? []) as Array<{ rms: number; src: string }>;
      return samples.filter((s) => s.src && s.src !== srcMatch).some((s) => s.rms > 1);
    }, firstSrc);
    expect(audible, 'track 2 element produced no audible output after rollover').toBe(true);

    void secondSrc;
  });
});
