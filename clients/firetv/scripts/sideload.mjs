// Sideload the latest dist/ APK to a Fire TV via adb. Connects
// over the network — Fire TVs expose adb on port 5555 once
// developer mode + ADB Debugging are turned on (see README.md).
//
// Env:
//   FIRETV_HOST=<ip>     required — Fire TV's LAN IP
//   FIRETV_PORT=5555     optional, default 5555
//
// We run `adb connect` first so multiple Fire TVs on the same
// LAN are addressable individually; then `adb -s <host:port>
// install -r` targets only this one. If only one device is
// connected, you can drop FIRETV_HOST and adb picks it.

import { existsSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const __dirname = dirname(fileURLToPath(import.meta.url));
const here = resolve(__dirname, '..');
const dist = join(here, 'dist');

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
  const c = spawnSync('adb', ['connect', target], { stdio: 'inherit', shell: process.platform === 'win32' });
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
const i = spawnSync('adb', installArgs, { stdio: 'inherit', shell: process.platform === 'win32' });

if (i.status !== 0) {
  console.error(`adb install exited with status ${i.status}`);
  process.exit(i.status ?? 1);
}

console.log('install: success — find OnScreen under "Your Apps & Channels" on the Fire TV launcher');
