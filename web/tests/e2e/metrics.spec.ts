// E2E for the Prometheus metrics port — this cycle wired up the onscreen_*
// application metrics that were previously registered but never recorded.
//
// Verifies the port serves valid Prometheus on its own mux (no app routes leak
// onto it), the runtime + custom families are present, HTTP/DB metrics actually
// record after traffic, and — critically — the HTTP path label uses the chi
// route TEMPLATE so per-ID URLs can't explode metric cardinality.
//
// The metrics port is separate from the API (default :7071 vs the API's :7070).
// Override with METRICS_URL if your deployment differs.

import { test, expect } from '@playwright/test';

const METRICS_URL = (process.env.METRICS_URL ?? 'http://localhost:7071').replace(/\/$/, '');

async function scrape(request: import('@playwright/test').APIRequestContext): Promise<string> {
  const r = await request.get(`${METRICS_URL}/metrics`);
  expect(r.status(), 'metrics endpoint should be 200').toBe(200);
  return r.text();
}

test.describe('Metrics port', () => {
  test('serves valid Prometheus exposition format', async ({ request }) => {
    const r = await request.get(`${METRICS_URL}/metrics`);
    expect(r.status()).toBe(200);
    expect(r.headers()['content-type'] ?? '').toMatch(/text\/plain.*version=0\.0\.4/);
    const body = await r.text();
    // HELP/TYPE preamble proves it's real exposition output, not an error page.
    expect(body).toMatch(/# HELP /);
    expect(body).toMatch(/# TYPE /);
  });

  test('exposes Go runtime, process, and onscreen_* families', async ({ request }) => {
    const body = await scrape(request);
    expect(body, 'go runtime collector').toMatch(/^go_goroutines /m);
    expect(body, 'process collector').toMatch(/^process_start_time_seconds /m);
    // ratelimit_failopen is a plain counter — registers at 0 even when idle, so
    // its presence proves the custom registry is wired to this endpoint.
    expect(body, 'custom registry').toMatch(/^onscreen_ratelimit_failopen_total /m);
  });

  test('records HTTP request metrics after traffic, with route-template labels', async ({ request }) => {
    // Generate a few requests on a REAL route. These 401 (no auth) but still get
    // routed, so the middleware records them under the route template.
    for (let i = 0; i < 3; i++) {
      await request.get('/api/v1/jobs');
    }
    await request.get('/health/live');

    const body = await scrape(request);
    const httpLines = body.split('\n').filter((l) => l.startsWith('onscreen_http_requests_total'));
    expect(httpLines.length, 'HTTP request metric should have series after traffic').toBeGreaterThan(0);

    // The /api/v1/jobs route must appear by its template path.
    expect(httpLines.some((l) => l.includes('path="/api/v1/jobs"'))).toBe(true);

    // Cardinality guard: NO path label may contain a raw UUID — that would mean
    // per-ID URLs are minting unbounded series instead of collapsing to the
    // chi route template (e.g. /items/{id}).
    const uuid = /path="[^"]*[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
    const offenders = httpLines.filter((l) => uuid.test(l));
    expect(offenders, `raw UUIDs leaked into path labels:\n${offenders.join('\n')}`).toEqual([]);
  });

  test('records DB query duration by SQL verb', async ({ request }) => {
    // /health/ready touches the DB, so the query tracer fires.
    await request.get('/health/ready');
    const body = await scrape(request);
    expect(body, 'DB query histogram').toMatch(/^onscreen_db_query_duration_seconds_count\{query="SELECT"\}/m);
  });

  test('is isolated — app routes do not serve on the metrics port', async ({ request }) => {
    // The metrics mux only serves /metrics; an API path must not return the app's
    // JSON (it should 404 / not-200 on this port).
    const r = await request.get(`${METRICS_URL}/api/v1/jobs`);
    expect(r.status(), 'API must not be reachable on the metrics port').not.toBe(200);
  });
});
