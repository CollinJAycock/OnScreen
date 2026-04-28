// Sideload the latest .wgt to a developer-mode Tizen TV.
//
// Requires `sdb` (Samsung's adb-equivalent) on PATH — bundled with
// Tizen Studio at ~/tizen-studio/tools/sdb. The Tizen Studio
// "Device Manager" lists connected TVs; this script does the same
// thing CLI-only.
//
// Setup once per machine:
//   1. On the TV: Settings → Apps → enable Developer Mode (with
//      the magic remote sequence — see README.md).
//   2. Note the TV's IP. Connect from this machine:
//        sdb connect <tv-ip>:26101
//      That registers the TV as a device.
//   3. Set TIZEN_DEVICE to whatever sdb's `devices` lists it as
//      (usually the IP), or leave unset to use the first device.

import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');
const dist = join(root, 'dist');

if (!existsSync(dist)) {
  console.error('dist/ not found — run `npm run package` first');
  process.exit(1);
}
const wgts = readdirSync(dist)
  .filter((f) => f.endsWith('.wgt'))
  .map((f) => ({ f, mtime: statSync(join(dist, f)).mtimeMs }))
  .sort((a, b) => b.mtime - a.mtime);
if (wgts.length === 0) {
  console.error('no .wgt in dist/ — run `npm run package` first');
  process.exit(1);
}
const wgt = join(dist, wgts[0].f);

const device = process.env.TIZEN_DEVICE;
const target = device ? ['-t', device] : [];

console.log(`installing ${wgts[0].f}` + (device ? ` to ${device}` : ' (default device)'));
const r = spawnSync('tizen', ['install', '-n', wgt, ...target], {
  stdio: 'inherit',
  shell: process.platform === 'win32'
});

if (r.status !== 0) {
  console.error(`\ntizen install exited with status ${r.status}`);
  console.error('Common fixes:');
  console.error('  - TV not connected? `sdb connect <tv-ip>:26101` first');
  console.error('  - Wrong cert profile? Re-package with TIZEN_CERT_PROFILE matching the TV partner cert');
  console.error('  - "Author signature is invalid"? The author cert in Tizen Studio doesn\'t match');
  console.error('    the distributor cert installed on the TV (each TV pinned to a partner profile).');
  process.exit(r.status ?? 1);
}
