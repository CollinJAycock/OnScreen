import { defineConfig, devices } from '@playwright/test';

// Browser-driven E2E for the rows of docs/manual-test-plan.md a real user
// could run unattended. Anything requiring perceptual judgement (HDR
// tonemap, audiophile A/B), real GPU/tuner hardware, or real third-party
// IdPs stays in the manual plan.
//
// Configure the target with env vars instead of baking host/creds into specs:
//   BASE_URL          where OnScreen is reachable (default http://localhost:7070)
//   E2E_USERNAME      admin user for auth flows
//   E2E_PASSWORD      admin password
//   E2E_GAPLESS_ALBUM gapless album item id (gapless.spec.ts)
//
// Run:
//   cd web && npm run test:e2e:install   # one-time browser download (~600MB)
//   cd web && npm run test:e2e
//
// CI launches autoplay-permissive Chromium so the gapless analyser spec can
// actually hear audio. WebKit + Firefox are smoke-only; cross-browser audio
// pipeline differences belong in the manual sweep.
export default defineConfig({
  testDir: './tests/e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? [['html', { open: 'never' }], ['list']] : 'list',

  use: {
    baseURL: process.env.BASE_URL ?? 'http://localhost:7070',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    ignoreHTTPSErrors: true,
  },

  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: {
          args: [
            '--autoplay-policy=no-user-gesture-required',
            '--use-fake-ui-for-media-stream',
          ],
        },
      },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
      // Gapless analyser depends on Chromium's autoplay flags. Skip it here.
      grepInvert: /@chromium-only/,
    },
    {
      name: 'webkit',
      use: { ...devices['Desktop Safari'] },
      grepInvert: /@chromium-only/,
    },
  ],
});
