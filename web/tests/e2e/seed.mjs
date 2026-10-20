// Seeds a FRESH OnScreen stack for the Playwright specs (the nightly runs
// them against the throwaway compose stack): registers the first user, who
// becomes the admin, from E2E_USERNAME / E2E_PASSWORD, and creates one empty
// movie library so the library smoke has something to list. Re-running it
// against a seeded stack is a no-op.
//
//   BASE_URL=http://localhost:7090 E2E_USERNAME=… E2E_PASSWORD=… node tests/e2e/seed.mjs
//
// Not a *.spec file, so Playwright's testMatch ignores it.

const base = (process.env.BASE_URL ?? 'http://localhost:7070').replace(/\/$/, '');
const username = process.env.E2E_USERNAME ?? 'admin';
const password = process.env.E2E_PASSWORD ?? '';
if (!password) {
  console.error('seed: set E2E_PASSWORD');
  process.exit(2);
}

async function call(method, path, body, token) {
  const headers = { 'Content-Type': 'application/json' };
  if (token) headers.Authorization = `Bearer ${token}`;
  const r = await fetch(base + path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const text = await r.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* non-JSON body */
  }
  return { status: r.status, data: json?.data ?? json, text };
}

function must(res, ok, what) {
  if (!ok.includes(res.status)) {
    console.error(`seed: ${what} failed: HTTP ${res.status} ${res.text.slice(0, 300)}`);
    process.exit(1);
  }
  return res;
}

const status = must(await call('GET', '/api/v1/setup/status'), [200], 'setup status');
if (status.data?.setup_required) {
  must(await call('POST', '/api/v1/auth/register', { username, password }), [200, 201], 'register admin');
  console.log(`seed: registered admin ${username}`);
}

const login = must(await call('POST', '/api/v1/auth/login', { username, password }), [200], 'login');
const token = login.data?.access_token;

const libs = must(await call('GET', '/api/v1/libraries', undefined, token), [200], 'list libraries');
if (!Array.isArray(libs.data) || libs.data.length === 0) {
  must(
    await call('POST', '/api/v1/libraries', { name: 'E2E Movies', type: 'movie', scan_paths: ['/media/e2e-movies'] }, token),
    [200, 201],
    'create library',
  );
  console.log('seed: created library E2E Movies');
}
console.log('seed: done');
