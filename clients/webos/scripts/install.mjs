// Install the newest com.onscreen.tv_*.ipk to a registered device.
//
// We were originally just inlining the glob in the npm script
// (`ares-install ./com.onscreen.tv_*.ipk`), but Unix shells expand
// the * and Windows shells (PowerShell, cmd) hand the literal string
// through. ares-install then 404s with "specified path does not
// exist". Resolving via Node's readdirSync works the same way on
// every OS.
//
// Device selection:
//   --emu              → target the emulator (device name "emulator",
//                        the conventional ares-setup-device label)
//   --tv               → target the real TV (device name "tv")
//   --device=<name>    → arbitrary device label
//   default (none)     → "tv" — preserves the prior `install-tv` behaviour

import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');

if (!existsSync(root)) {
  console.error('webos client root missing — repo layout broken');
  process.exit(1);
}

// Find the newest IPK in the client root. ares-package writes it
// here by default (alongside package.json), so no recursion needed.
const ipks = readdirSync(root)
  .filter((f) => f.startsWith('com.onscreen.tv_') && f.endsWith('.ipk'))
  .map((f) => ({ f, mtime: statSync(join(root, f)).mtimeMs }))
  .sort((a, b) => b.mtime - a.mtime);

if (ipks.length === 0) {
  console.error('no com.onscreen.tv_*.ipk in', root);
  console.error('run `npm run package` first');
  process.exit(1);
}
const ipk = join(root, ipks[0].f);

let device = 'tv';
for (const arg of process.argv.slice(2)) {
  if (arg === '--emu') device = 'emulator';
  else if (arg === '--tv') device = 'tv';
  else if (arg.startsWith('--device=')) device = arg.slice('--device='.length);
}

console.log(`installing ${ipks[0].f} → device "${device}"`);
const r = spawnSync('ares-install', ['--device', device, ipk], {
  stdio: 'inherit',
  shell: process.platform === 'win32',
});

if (r.status !== 0) {
  console.error(`\nares-install exited with status ${r.status}`);
  console.error('Common fixes:');
  console.error('  - Emulator not running? Start it from the SDK Emulator Launcher.');
  console.error('  - Device not registered? `ares-setup-device` and add a device named', JSON.stringify(device));
  console.error('  - Different IPK name after a version bump? Re-run `npm run package` to refresh.');
  process.exit(r.status ?? 1);
}
