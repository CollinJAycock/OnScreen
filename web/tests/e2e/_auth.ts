// Shared admin-auth helper for the settings/metrics E2E specs.
//
// /auth/login is rate-limited to 10 req/min per IP, and these specs make many
// admin API calls. Because the suite runs with workers:1, all spec files share
// one Node worker — so caching the token at module scope means the whole API
// suite logs in exactly ONCE, well under the limit. (UI specs still form-login
// since the access token lives in memory, not a readable store.)
//
// Not a *.spec.ts, so Playwright's testMatch ignores it.

import { expect, type APIRequestContext } from '@playwright/test';

export const USERNAME = process.env.E2E_USERNAME ?? 'admin';
export const PASSWORD = process.env.E2E_PASSWORD ?? '';
// E2E_TOKEN lets a pre-minted admin bearer token drive the API specs without
// the login form — handy in CI (no plaintext password) and to avoid the
// /auth/login rate limit. Mint one with: go run ./cmd/devtoken (USER_ID + SESSION_EPOCH).
const TOKEN = process.env.E2E_TOKEN ?? '';

// CAN_API: API specs can run with either a token or a password.
// CAN_UI: browser specs need the password (the access token lives in memory and
// can't be injected into the SPA without the login flow).
export const CAN_API = !!(TOKEN || PASSWORD);
export const CAN_UI = !!PASSWORD;

let cached: string | null = null;

/** adminToken returns a pre-minted E2E_TOKEN, else logs in once and reuses it. */
export async function adminToken(request: APIRequestContext): Promise<string> {
  if (TOKEN) return TOKEN;
  if (cached) return cached;
  const r = await request.post('/api/v1/auth/login', {
    data: { username: USERNAME, password: PASSWORD },
  });
  expect(r.status(), 'admin login should succeed (check E2E_USERNAME/E2E_PASSWORD)').toBe(200);
  cached = (await r.json()).data.access_token as string;
  return cached;
}

/** auth builds the Bearer header block for request options. */
export const auth = (token: string) => ({ headers: { Authorization: `Bearer ${token}` } });

/** loginUI drives the login form for browser (page) specs. */
export async function loginUI(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/login');
  await page.getByLabel(/username/i).fill(USERNAME);
  await page.getByLabel(/password/i).fill(PASSWORD);
  await page.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(page).not.toHaveURL(/\/login/);
}
