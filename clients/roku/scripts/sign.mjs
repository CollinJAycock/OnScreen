// Sign a channel zip on a developer-mode Roku and download the store .pkg.
//
//   node scripts/sign.mjs <channel.zip> <out.pkg>
//
// Env (never stored anywhere):
//   ROKU_HOST           the Roku's IP
//   ROKU_DEV_PASSWORD   the developer web server password (user `rokudev`)
//   ROKU_SIGN_PASSWORD  the genkey signing password for the channel's Dev ID
//
// The Packager signs whatever dev channel is installed, so this sideloads the
// given zip first (Install, or "Identical to previous"), then posts
// plugin_package and downloads the .pkg it links to. The Roku must already
// hold the channel's signing key (genkey, once, ever); never run genkey again,
// since a new key can't update the published channel.
//
// Used by clients/build-packages.ps1 (target roku); see clients/packages/README.md.

import { readFileSync, writeFileSync } from 'node:fs';
import { basename } from 'node:path';
import { createHash, randomBytes } from 'node:crypto';

const [zipPath, outPath] = process.argv.slice(2);
const host = process.env.ROKU_HOST;
const devPassword = process.env.ROKU_DEV_PASSWORD;
const signPassword = process.env.ROKU_SIGN_PASSWORD;

if (!zipPath || !outPath) {
  console.error('usage: node scripts/sign.mjs <channel.zip> <out.pkg>');
  process.exit(2);
}
if (!host || !devPassword || !signPassword) {
  console.error('Set ROKU_HOST, ROKU_DEV_PASSWORD and ROKU_SIGN_PASSWORD.');
  process.exit(2);
}

const md5 = (s) => createHash('md5').update(s).digest('hex');

// Roku's dev web server uses HTTP digest auth (MD5, qop=auth): an unauthenticated
// request gets the 401 challenge, the second one answers it.
async function withDigest(url, method, body) {
  const probe = await fetch(url, { method });
  if (probe.status !== 401) {
    throw new Error(`expected a 401 digest challenge from ${url}, got ${probe.status}`);
  }
  const challenge = probe.headers.get('www-authenticate') || '';
  if (!challenge.startsWith('Digest ')) throw new Error(`unexpected challenge: ${challenge}`);
  const p = Object.fromEntries(
    [...challenge.slice(7).matchAll(/(\w+)=(?:"([^"]*)"|([^,]*))/g)].map((m) => [m[1], m[2] ?? m[3]]),
  );
  const uri = new URL(url).pathname;
  const nc = '00000001';
  const cnonce = randomBytes(8).toString('hex');
  const ha1 = md5(`rokudev:${p.realm}:${devPassword}`);
  const ha2 = md5(`${method}:${uri}`);
  const response = md5(`${ha1}:${p.nonce}:${nc}:${cnonce}:auth:${ha2}`);
  const auth =
    `Digest username="rokudev", realm="${p.realm}", nonce="${p.nonce}", uri="${uri}", ` +
    `qop=auth, nc=${nc}, cnonce="${cnonce}", response="${response}"` +
    (p.opaque ? `, opaque="${p.opaque}"` : '');
  const res = await fetch(url, { method, headers: { Authorization: auth }, body });
  if (res.status === 401) throw new Error(`${url}: the developer password was refused`);
  return res;
}

// Roku reports outcomes in the HTML body, not the status code.
function errorText(html) {
  const m = /<font color="red">([^<]+)<\/font>/.exec(html) || /"type":"error","text":"([^"]+)"/.exec(html);
  return m ? m[1] : html.replace(/\s+/g, ' ').slice(0, 400);
}

function manifestField(field) {
  // The Packager's app_name label; the zip is built from this same manifest.
  const text = readFileSync(new URL('../manifest', import.meta.url), 'utf8');
  return new RegExp(`^${field}=(.*)$`, 'm').exec(text)?.[1]?.trim();
}

async function main() {
  const base = `http://${host}`;

  // 1. Install exactly this zip.
  const install = new FormData();
  install.append('mysubmit', 'Install');
  install.append('archive', new Blob([readFileSync(zipPath)]), basename(zipPath));
  const installHtml = await (await withDigest(`${base}/plugin_install`, 'POST', install)).text();
  if (!/Install Success|Identical to previous/.test(installHtml)) {
    throw new Error(`sideload failed: ${errorText(installHtml)}`);
  }
  console.log(`sideloaded ${basename(zipPath)} to ${host}`);

  // 2. Package (sign) the installed dev channel.
  const title = manifestField('title') || 'OnScreen';
  const version = ['major_version', 'minor_version', 'build_version']
    .map((f) => manifestField(f) || '0')
    .join('.');
  const pkg = new FormData();
  pkg.append('mysubmit', 'Package');
  pkg.append('app_name', `${title}/${version}`);
  pkg.append('passwd', signPassword);
  pkg.append('pkg_time', String(Date.now()));
  const pkgHtml = await (await withDigest(`${base}/plugin_package`, 'POST', pkg)).text();
  // Older firmware links the file; Roku OS 10+ puts it in page JSON.
  const link =
    /<a href="(pkgs\/[^"]+\.pkg)">/.exec(pkgHtml)?.[1] || /"pkgPath"\s*:\s*"([^"]+\.pkg)"/.exec(pkgHtml)?.[1];
  if (!link) throw new Error(`packaging failed: ${errorText(pkgHtml)}`);

  // 3. Download it.
  const path = link.replace(/^\/+/, '').replace(/\/{2,}/g, '/');
  const res = await withDigest(`${base}/${path}`, 'GET');
  if (!res.ok) throw new Error(`download of ${path} failed: HTTP ${res.status}`);
  const bytes = Buffer.from(await res.arrayBuffer());
  if (bytes.length < 1024) throw new Error(`downloaded ${path} is only ${bytes.length} bytes`);
  writeFileSync(outPath, bytes);
  console.log(`signed package ${path} -> ${outPath} (${(bytes.length / 1024).toFixed(1)} KiB)`);
}

main().catch((err) => {
  console.error(err.message || err);
  process.exit(1);
});
