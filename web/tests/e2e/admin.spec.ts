// Admin endpoint smoke — covers two operator-facing surfaces that
// historically have no automated coverage and tend to silently break:
//
//   1. /api/v1/admin/backup — must either return a real ZIP or, when
//      pg_dump is unavailable on the host, a structured 503 with the
//      explicit PG_DUMP_UNAVAILABLE error code (NOT a blank 500).
//      Backups are the kind of thing nobody notices is broken until
//      they actually need them.
//
//   2. /api/v1/admin/tasks — POST {id}/run must advance the task's
//      last_run_at within a few seconds. Catches scheduler-handler
//      regressions where the run-now button silently no-ops.
//
// Required env:
//   E2E_USERNAME   OnScreen username (default 'admin')
//   E2E_PASSWORD   OnScreen password — required; block skips otherwise

import { test, expect } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'admin';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

async function login(request: import('@playwright/test').APIRequestContext): Promise<string> {
  const r = await request.post('/api/v1/auth/login', {
    data: { username: USERNAME, password: PASSWORD },
  });
  expect(r.status()).toBe(200);
  const { data } = await r.json();
  return data.access_token;
}

test.describe('Admin — backup endpoint', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run admin backup specs');

  test('GET /api/v1/admin/backup returns a ZIP, or a clean 503 when pg_dump is missing', async ({
    request,
  }) => {
    const token = await login(request);

    const r = await request.get('/api/v1/admin/backup', {
      headers: { Authorization: `Bearer ${token}` },
    });

    if (r.status() === 200) {
      // Happy path: actual ZIP bytes. Verify magic + non-trivial size.
      const body = await r.body();
      expect(body.length, 'backup must be non-empty').toBeGreaterThan(100);
      // ZIP local-file header magic: 50 4B 03 04 ("PK\x03\x04").
      const magic = body.subarray(0, 4).toString('hex');
      expect(magic, `expected ZIP magic 504b0304, got ${magic}`).toBe('504b0304');
      // Some indication of content-type matching the bytes (operators
      // expect to be able to save the response straight to .zip).
      const ct = r.headers()['content-type'] ?? '';
      expect.soft(ct, `unexpected content-type for backup: ${ct}`).toMatch(/zip|octet-stream/i);
    } else if (r.status() === 503) {
      // pg_dump missing path: the error must be SHAPED, not a blank
      // 500. This catches the regression where the unavailable case
      // silently returns "internal error" instead of telling the
      // operator how to fix it.
      const body = await r.json();
      expect(body, 'backup 503 must have an error envelope').toHaveProperty('error');
      expect(body.error, 'backup 503 must name the specific cause').toHaveProperty('code');
      expect(
        body.error.code,
        `backup 503 must use the PG_DUMP_UNAVAILABLE code, got: ${body.error.code}`,
      ).toBe('PG_DUMP_UNAVAILABLE');
      expect(body.error.message, 'backup 503 must include a remediation hint').toMatch(
        /pg_dump|postgresql-client/i,
      );
    } else {
      throw new Error(
        `backup endpoint must return 200 (ZIP) or 503 (PG_DUMP_UNAVAILABLE); got ${r.status()}: ${await r.text()}`,
      );
    }
  });
});

test.describe('Admin — tasks scheduler', () => {
  test.skip(!PASSWORD, 'set E2E_PASSWORD to run admin tasks specs');

  test('POST /admin/tasks/{id}/run advances last_run_at', async ({ request }) => {
    // Picks any existing task and forces a run-now. Asserts last_run_at
    // moves forward within 15 s. Catches regressions where the
    // scheduler dispatcher is broken (run-now silently no-ops).
    const token = await login(request);

    const listR = await request.get('/api/v1/admin/tasks', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(listR.status()).toBe(200);
    const { data: tasks } = await listR.json();
    if (!Array.isArray(tasks) || tasks.length === 0) {
      test.skip(true, 'No scheduled tasks present — nothing to run');
      return;
    }

    // Prefer a fast task (DVR retention / EPG refresh / DVR matcher
    // all complete in <1 s on an unconfigured dev box). Fall back to
    // any available task.
    const fast = tasks.find((t: any) =>
      ['dvr_retention', 'epg_refresh', 'dvr_match'].includes(t.task_type),
    );
    const target: any = fast ?? tasks[0];
    const beforeRun = target.last_run_at; // may be null on a never-run task

    const runR = await request.post(`/api/v1/admin/tasks/${target.id}/run`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    // The run-now handler bumps next_run_at to NOW so the next scheduler
    // tick (≤30 s) picks it up. Accepts 200, 202, or 204 — exact code
    // depends on implementation.
    expect(
      [200, 202, 204],
      `run-now POST must succeed, got ${runR.status()}: ${await runR.text()}`,
    ).toContain(runR.status());

    // Poll the task list for last_run_at to advance. The scheduler
    // ticks every ~30 s by default; tasks themselves complete in <1 s
    // for the safe set above. Poll for up to 45 s.
    await expect
      .poll(
        async () => {
          const r = await request.get('/api/v1/admin/tasks', {
            headers: { Authorization: `Bearer ${token}` },
          });
          if (!r.ok()) return null;
          const { data: latest } = await r.json();
          const updated = (latest as any[]).find((t) => t.id === target.id);
          return updated?.last_run_at ?? null;
        },
        {
          timeout: 45_000,
          intervals: [1_000, 2_000, 5_000],
          message: `task ${target.task_type} (${target.id}) last_run_at never advanced past ${beforeRun}`,
        },
      )
      .not.toBe(beforeRun);
  });
});
