// Sideload the latest dist/ APK to a Fire TV via adb. Connects
// over the network — Fire TVs expose adb on port 5555 once
// developer mode + ADB Debugging are turned on (see README.md).
//
// Env:
//   FIRETV_HOST=<ip>     required — Fire TV's LAN IP
//   FIRETV_PORT=5555     optional, default 5555
//   ADB=<path-to-adb>    optional — explicit adb binary
//
// adb is resolved from ADB, then ANDROID_SDK_ROOT/ANDROID_HOME's
// platform-tools, then PATH — so a missing platform-tools install
// fails with a clear message instead of an opaque ENOENT.

import { existsSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const __dirname = dirname(fileURLToPath(import.meta.url));
const here = resolve(__dirname, '..');
const dist = join(here, 'dist');

function resolveAdb() {
  if (process.env.ADB) return process.env.ADB;
  const sdk = process.env.ANDROID_SDK_ROOT || process.env.ANDROID_HOME;
  const exe = process.platform === 'win32' ? 'adb.exe' : 'adb';
  if (sdk) {
    const p = join(sdk, 'platform-tools', exe);
    if (existsSync(p)) return p;
  }
  // Fall back to PATH. Use the platform-correct name so Windows
  // (which needs the .exe to resolve a bare name without a shell) works.
  return exe;
}

const adb = resolveAdb();

// Verify adb is actually runnable up front, so the failure is a clear
// "install platform-tools" message rather than an opaque ENOENT mid-install.
const probe = spawnSync(adb, ['version'], { stdio: 'ignore' });
if (probe.error || probe.status !== 0) {
  console.error(`adb not found or not runnable (tried "${adb}").`);
  console.error('Fix one of:');
  console.error('  - install Android platform-tools and add it to PATH, or');
  console.error('  - set ANDROID_SDK_ROOT / ANDROID_HOME (adb is under platform-tools/), or');
  console.error('  - set ADB=<full path to adb>');
  process.exit(1);
}

function runAdb(args) {
  const r = spawnSync(adb, args, { stdio: 'inherit' });
  if (r.error) {
    console.error(`failed to run adb: ${r.error.message}`);
    process.exit(1);
  }
  return r;
}

const apks = existsSync(dist)
  ? readdirSync(dist)
      .filter((f) => f.endsWith('.apk'))
      .map((f) => ({ f, mtime: statSync(join(dist, f)).mtimeMs }))
      .sort((a, b) => b.mtime - a.mtime)
  : [];

if (apks.length === 0) {
  console.error('no .apk in dist/ — run `npm run build` first');
  process.exit(1);
}
const apk = join(dist, apks[0].f);

const host = process.env.FIRETV_HOST;
const port = process.env.FIRETV_PORT || '5555';
const target = host ? `${host}:${port}` : null;

if (target) {
  const c = runAdb(['connect', target]);
  if (c.status !== 0) {
    console.error(`adb connect ${target} failed`);
    console.error('Common fixes:');
    console.error('  - Fire TV not on same LAN? Check Settings → My Fire TV → About → Network');
    console.error('  - ADB Debugging not enabled? Settings → My Fire TV → Developer Options');
    console.error('  - First connect requires accepting the on-screen prompt on the TV');
    process.exit(c.status ?? 1);
  }
}

const installArgs = target ? ['-s', target, 'install', '-r', apk] : ['install', '-r', apk];
console.log(`adb ${installArgs.join(' ')}`);
const i = runAdb(installArgs);

if (i.status !== 0) {
  console.error(`adb install exited with status ${i.status}`);
  process.exit(i.status ?? 1);
}

console.log('install: success — find OnScreen under "Your Apps & Channels" on the Fire TV launcher');
