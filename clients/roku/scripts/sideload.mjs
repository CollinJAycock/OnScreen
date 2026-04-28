// Sideload the latest packaged .zip to a developer-mode Roku.
//
// Reads the device IP and webserver password from env:
//   ROKU_HOST=192.168.1.42
//   ROKU_DEV_PASSWORD=mypass
//
// Roku's sideload endpoint is a digest-auth-protected multipart
// upload at http://<host>/plugin_install with field name
// `archive`. Username is always `rokudev`. The response is HTML;
// we look for "Identical to previous" / "Install Success." in it
// to decide pass/fail (Roku doesn't use HTTP status codes for
// install outcome, of course).
//
// If no zip exists in dist/, runs the package script first.

import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = resolve(__dirname, '..');
const distDir = join(root, 'dist');

const host = process.env.ROKU_HOST;
const password = process.env.ROKU_DEV_PASSWORD;

if (!host || !password) {
  console.error('Set ROKU_HOST and ROKU_DEV_PASSWORD in your environment.');
  console.error('Example: ROKU_HOST=192.168.1.42 ROKU_DEV_PASSWORD=mypass npm run sideload');
  process.exit(1);
}

function ensurePackaged() {
  if (!existsSync(distDir) || readdirSync(distDir).filter(f => f.endsWith('.zip')).length === 0) {
    console.log('No package in dist/ — running package script first...');
    const r = spawnSync('node', [join(__dirname, 'package.mjs')], { stdio: 'inherit' });
    if (r.status !== 0) process.exit(r.status ?? 1);
  }
  const zips = readdirSync(distDir).filter(f => f.endsWith('.zip'));
  if (zips.length === 0) {
    console.error('Packaging produced no zip in dist/');
    process.exit(1);
  }
  // Most recent by mtime — accommodates multiple version-stamped
  // builds left behind during a release-bump session.
  return zips
    .map((f) => ({ f, mtime: statSync(join(distDir, f)).mtimeMs }))
    .sort((a, b) => b.mtime - a.mtime)[0].f;
}

// Roku uses HTTP digest auth (RFC 7616 MD5). Build the response.
function digestResponse({ realm, nonce, qop, uri, method = 'POST' }) {
  const ha1 = md5(`rokudev:${realm}:${password}`);
  const ha2 = md5(`${method}:${uri}`);
  const cnonce = md5(`${Date.now()}`);
  const nc = '00000001';
  const response = md5(`${ha1}:${nonce}:${nc}:${cnonce}:${qop}:${ha2}`);
  return { response, cnonce, nc };
}

const md5 = (s) => createHash('md5').update(s).digest('hex');

async function postWithDigest(url, body, contentType) {
  // First request: server replies 401 with a WWW-Authenticate
  // challenge. Second request: same body + computed digest header.
  const probe = await fetch(url, { method: 'POST' });
  if (probe.status !== 401) {
    throw new Error(`Expected 401 challenge from ${url}, got ${probe.status}`);
  }
  const challenge = probe.headers.get('www-authenticate');
  if (!challenge || !challenge.startsWith('Digest ')) {
    throw new Error(`Unexpected challenge: ${challenge}`);
  }
  const params = Object.fromEntries(
    [...challenge.slice(7).matchAll(/(\w+)=(?:"([^"]*)"|([^,]*))/g)].map(
      (m) => [m[1], m[2] ?? m[3]],
    ),
  );

  const path = new URL(url).pathname;
  const { response, cnonce, nc } = digestResponse({
    realm: params.realm,
    nonce: params.nonce,
    qop: 'auth',
    uri: path,
  });

  const auth =
    `Digest username="rokudev", realm="${params.realm}", nonce="${params.nonce}", ` +
    `uri="${path}", qop=auth, nc=${nc}, cnonce="${cnonce}", response="${response}"`;

  return fetch(url, {
    method: 'POST',
    headers: { Authorization: auth, 'Content-Type': contentType },
    body,
  });
}

async function main() {
  const zipName = ensurePackaged();
  const zipPath = join(distDir, zipName);
  console.log(`sideloading ${zipName} → ${host}`);

  // Build a multipart/form-data body. Native fetch + FormData
  // handles the boundary + headers in Node 24.
  const fd = new FormData();
  fd.append('mysubmit', 'Install');
  fd.append('archive', new Blob([readFileSync(zipPath)]), zipName);

  const url = `http://${host}/plugin_install`;
  const resp = await postWithDigest(url, fd, /* fetch sets multipart boundary */ undefined);
  const text = await resp.text();

  // Roku surfaces install outcome in the HTML response body,
  // typically inside a div with class="roku-color-{ok,error}".
  if (text.includes('Install Success') || text.includes('Identical to previous')) {
    console.log('install: success');
    return;
  }
  console.error('install: failed');
  // Trim noise — Roku's UI HTML is verbose. Look for the relevant
  // error message tag; fall back to dumping the body.
  const errMatch = /<font color="red">([^<]+)<\/font>/.exec(text);
  if (errMatch) console.error(errMatch[1]);
  else console.error(text.slice(0, 500));
  process.exit(1);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
