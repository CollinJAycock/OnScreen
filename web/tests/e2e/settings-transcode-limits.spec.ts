// E2E for Settings ▸ Transcode ▸ Output Limits — the global transcode ceilings
// (max bitrate / width / height) that moved out of env vars this cycle, living
// alongside the existing NVENC tuning in TranscodeConfig.
//
// Verifies the GET fills unset caps with the effective running defaults (so the
// form is never blank), the caps + NVENC round-trip together, and the endpoint
// is enveloped. Restart-time enforcement of the caps is in the manual plan.
//
// Required env: E2E_PASSWORD (admin). Restores the original config afterwards.

import { test, expect } from '@playwright/test';
import { adminToken, auth, loginUI, CAN_API, CAN_UI } from './_auth';

test.describe('Settings ▸ Transcode output limits — API', () => {
  test.skip(!CAN_API, 'set E2E_PASSWORD to run transcode-limit specs');

  test('GET fills unset caps with effective (non-zero) defaults', async ({ request }) => {
    const token = await adminToken(request);
    const r = await request.get('/api/v1/settings/transcode-config', auth(token));
    expect(r.status()).toBe(200);
    const body = await r.json();
    expect(body, 'must be enveloped').toHaveProperty('data');
    const tc = body.data;

    // The caps endpoint backfills 0 with the running server ceilings so the page
    // shows live values, never blanks.
    expect(tc.max_bitrate_kbps, 'max_bitrate_kbps backfilled').toBeGreaterThan(0);
    expect(tc.max_width, 'max_width backfilled').toBeGreaterThan(0);
    expect(tc.max_height, 'max_height backfilled').toBeGreaterThan(0);
  });

  test('caps + NVENC tuning round-trip together', async ({ request }) => {
    const token = await adminToken(request);
    const orig = (await (await request.get('/api/v1/settings/transcode-config', auth(token))).json()).data;

    const desired = {
      ...orig,
      nvenc_preset: 'p6',
      nvenc_tune: 'hq',
      nvenc_rc: 'vbr',
      maxrate_ratio: 2.0,
      max_bitrate_kbps: 25000,
      max_width: 1920,
      max_height: 1080,
    };

    try {
      const put = await request.put('/api/v1/settings/transcode-config', { ...auth(token), data: desired });
      expect([200, 204]).toContain(put.status());

      const got = (await (await request.get('/api/v1/settings/transcode-config', auth(token))).json()).data;
      expect(got.max_bitrate_kbps).toBe(25000);
      expect(got.max_width).toBe(1920);
      expect(got.max_height).toBe(1080);
      // The NVENC tuning that was already in this struct must survive a caps save.
      expect(got.nvenc_preset).toBe('p6');
      expect(got.maxrate_ratio).toBe(2.0);
    } finally {
      await request.put('/api/v1/settings/transcode-config', { ...auth(token), data: orig });
    }
  });

  test('rejects unauthenticated access', async ({ request }) => {
    expect((await request.get('/api/v1/settings/transcode-config')).status()).toBe(401);
  });
});

test.describe('Settings ▸ Transcode output limits — UI', () => {
  test.skip(!CAN_UI, 'set E2E_PASSWORD to run transcode-limit UI specs');

  test('Output Limits section renders with the three cap inputs', async ({ page }) => {
    await loginUI(page);

    await page.goto('/settings/transcode');
    await expect(page.getByText(/Output Limits/i)).toBeVisible();
    await expect(page.getByLabel(/Max Bitrate/i)).toBeVisible();
    await expect(page.getByLabel(/Max Width/i)).toBeVisible();
    await expect(page.getByLabel(/Max Height/i)).toBeVisible();
  });
});
