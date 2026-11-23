// Package the current build/ a second time as a 1280x720 app, for LG Full HD
// models: dist-720/tv.onscreen.webos_<version>_all.ipk.
//
// LG's App Resolution spec: Full HD models give an app a 1280x720 graphics
// plane, UHD models 1920x1080, and LG recommends submitting two packages,
// one at each resolution. The web build is the same for both: the layout
// scales its 1920x1080 canvas to whatever plane it gets ($lib/canvasScale).
// Only appinfo.json's "resolution" differs, so this copies build/ to a
// staging folder, changes that one field and runs ares-package there.
//
//   npm run package        # build/ + tv.onscreen.webos_<ver>_all.ipk (1920x1080)
//   npm run package:720    # dist-720/tv.onscreen.webos_<ver>_all.ipk (1280x720)

import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');
const build = join(root, 'build');
const stage = join(root, 'build-720');
const out = join(root, 'dist-720');

if (!existsSync(join(build, 'appinfo.json'))) {
  console.error('build/appinfo.json not found: run `npm run build` first');
  process.exit(1);
}

rmSync(stage, { recursive: true, force: true });
cpSync(build, stage, { recursive: true });
const infoPath = join(stage, 'appinfo.json');
const info = JSON.parse(readFileSync(infoPath, 'utf8'));
info.resolution = '1280x720';
writeFileSync(infoPath, JSON.stringify(info, null, 2) + '\n');

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
const r = spawnSync('ares-package', [stage, '-o', out], {
  stdio: 'inherit',
  shell: process.platform === 'win32',
});
rmSync(stage, { recursive: true, force: true });
if (r.status !== 0) {
  console.error(`ares-package exited with status ${r.status}`);
  process.exit(r.status ?? 1);
}
const ipks = readdirSync(out).filter((f) => f.endsWith('.ipk'));
console.log(`packaged (1280x720): ${ipks.map((f) => join(out, f)).join(', ')}`);
