// E2E for Settings ▸ Nodes — the per-node config store added this cycle. Each
// node reads only its own row (keyed by NODE_ID, default hostname), so node/
// site-specific values (bind addresses, paths, SITE_ID, QSV, embedded-worker
// role) can be managed in the UI without leaking across a multi-site fleet.
//
// Also guards two regressions from this cycle:
//   - the node list must serialise as [] not null (a null .map crashed the page)
//   - media_path was removed; it must not reappear in the per-node DTO
//
// Required env: E2E_PASSWORD (admin). The round-trip test writes to a throwaway
// node id so it never alters this node's real config (there's no delete
// endpoint, so a stable id keeps re-runs from accumulating rows).

import { test, expect } from '@playwright/test';
import { adminToken, auth, loginUI, collectConsoleErrors, CAN_API, CAN_UI } from './_auth';

const THROWAWAY_NODE = 'e2e-playwright-node';

test.describe('Settings ▸ Nodes — API', () => {
  test.skip(!CAN_API, 'set E2E_PASSWORD to run Nodes specs');

  test('GET /settings/nodes lists the current node, never null', async ({ request }) => {
    const token = await adminToken(request);
    const r = await request.get('/api/v1/settings/nodes', auth(token));
    expect(r.status()).toBe(200);
    const { data } = await r.json();

    expect(data.current_node_id, 'a node must know its own id').toBeTruthy();
    // Regression: must be [] not null so the web client can .map() it.
    expect(Array.isArray(data.nodes), 'nodes must be an array, not null').toBe(true);
  });

  test('current node reports is_current + effective values, and has no media_path', async ({ request }) => {
    const token = await adminToken(request);
    const { data: list } = await (await request.get('/api/v1/settings/nodes', auth(token))).json();
    const nodeID = list.current_node_id;

    const r = await request.get(`/api/v1/settings/node/${encodeURIComponent(nodeID)}`, auth(token));
    expect(r.status()).toBe(200);
    const { data: node } = await r.json();

    expect(node.is_current).toBe(true);
    expect(node.node_id).toBe(nodeID);
    // Effective env values are surfaced for the current node (not blank).
    expect(node.listen_addr, 'current node should surface its bind addr').toBeTruthy();
    // media_path was removed this cycle — it must not be back.
    expect(node, 'media_path must be gone from the node DTO').not.toHaveProperty('media_path');
    // The fields that DID stay.
    for (const f of [
      'metrics_addr',
      'worker_health_addr',
      'cache_path',
      'static_abr_root',
      'site_id',
      'transcode_qsv_decode',
      'disable_embedded_worker',
    ]) {
      expect(node, `missing field ${f}`).toHaveProperty(f);
    }
  });

  test('a different node shows stored-only values and is_current=false', async ({ request }) => {
    const token = await adminToken(request);
    // An unconfigured arbitrary node: not current, blanks (we don't know its env).
    const r = await request.get('/api/v1/settings/node/some-other-node-xyz', auth(token));
    expect(r.status()).toBe(200);
    const { data: node } = await r.json();
    expect(node.is_current).toBe(false);
    expect(node.listen_addr, 'other node has no known default → blank').toBe('');
  });

  test('PUT round-trips a node config and surfaces it in the list', async ({ request }) => {
    const token = await adminToken(request);
    const desired = {
      node_id: THROWAWAY_NODE,
      is_current: false,
      listen_addr: ':9090',
      metrics_addr: ':9091',
      worker_health_addr: ':9094',
      cache_path: '/var/cache/onscreen',
      static_abr_root: '/srv/abr',
      site_id: 'e2e-site',
      transcode_qsv_decode: true,
      disable_embedded_worker: true,
    };

    const put = await request.put(`/api/v1/settings/node/${THROWAWAY_NODE}`, { ...auth(token), data: desired });
    expect([200, 204]).toContain(put.status());

    const { data: got } = await (
      await request.get(`/api/v1/settings/node/${THROWAWAY_NODE}`, auth(token))
    ).json();
    expect(got.listen_addr).toBe(':9090');
    expect(got.site_id).toBe('e2e-site');
    expect(got.transcode_qsv_decode).toBe(true);
    expect(got.disable_embedded_worker).toBe(true);

    const { data: list } = await (await request.get('/api/v1/settings/nodes', auth(token))).json();
    expect(
      list.nodes.map((n: { node_id: string }) => n.node_id),
      'configured node should appear in the picker',
    ).toContain(THROWAWAY_NODE);
  });

  test('rejects unauthenticated access', async ({ request }) => {
    expect((await request.get('/api/v1/settings/nodes')).status()).toBe(401);
  });
});

test.describe('Settings ▸ Nodes — UI', () => {
  test.skip(!CAN_UI, 'set E2E_PASSWORD to run Nodes UI specs');

  test('page loads with the node picker and no crash (null-map regression)', async ({ page }) => {
    const consoleErrors = collectConsoleErrors(page);

    await loginUI(page);

    await page.goto('/settings/nodes');
    await expect(page.getByText(/Per-node configuration/i)).toBeVisible();
    // The node picker rendered (its <select> is labelled "Configuring") and lists
    // the current node — proves the list loaded without the null-map crash.
    const picker = page.getByLabel(/Configuring/i);
    await expect(picker).toBeVisible();
    await expect(picker.locator('option', { hasText: /this node/i })).toHaveCount(1);

    const real = consoleErrors();
    expect(real, `console errors:\n${real.join('\n')}`).toEqual([]);
  });
});
