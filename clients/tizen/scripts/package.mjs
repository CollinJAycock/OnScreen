// Run `tizen package` against build/ to produce a sideloadable
// .wgt. Requires the Tizen Studio CLI (`tizen`) on PATH — see
// README.md for install steps.
//
// The package command needs an author certificate; we point at
// ~/.tizen-studio-data/keystore/author/<author>.p12 by default
// (the path Tizen Studio's "Certificate Manager" creates on first
// use). Override via TIZEN_CERT_PROFILE if you maintain multiple
// profiles (e.g., one for sideload + one for store submission).

import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');
const build = join(root, 'build');
const dist = join(root, 'dist');

if (!existsSync(build)) {
  console.error('build/ not found — run `npm run build` first');
  process.exit(1);
}
if (!existsSync(join(build, 'config.xml'))) {
  console.error('build/config.xml missing — assemble-package.mjs should have copied it');
  process.exit(1);
}

const profile = process.env.TIZEN_CERT_PROFILE || 'OnScreenDev';

const args = ['package', '-t', 'wgt', '-s', profile, '-o', dist, '--', build];
console.log('tizen', args.join(' '));
const r = spawnSync('tizen', args, { stdio: 'inherit', shell: process.platform === 'win32' });

if (r.status !== 0) {
  console.error(`\ntizen package exited with status ${r.status}`);
  console.error('Common fixes:');
  console.error('  - Tizen Studio CLI not on PATH? Add ~/tizen-studio/tools/ide/bin');
  console.error('  - No author certificate? Open Tizen Studio → Certificate Manager → New');
  console.error(`  - Wrong profile name? Set TIZEN_CERT_PROFILE (currently "${profile}")`);
  process.exit(r.status ?? 1);
}

const wgts = readdirSync(dist).filter((f) => f.endsWith('.wgt'));
console.log(`packaged: ${wgts.map((f) => join(dist, f)).join(', ')}`);
