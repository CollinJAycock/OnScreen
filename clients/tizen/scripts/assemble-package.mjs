// Copy Tizen widget metadata (config.xml + icon) into the
// SvelteKit build output so the build/ tree is the exact root of
// the .wgt the Tizen CLI will package.
//
// The Tizen `tizen package` CLI takes a directory; whatever's at
// the root of that dir lands in the .wgt at the root of the
// widget filesystem. config.xml at the root + index.html at the
// root is what the runtime expects.

import { copyFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');
const build = join(root, 'build');

const required = ['config.xml'];
const optional = ['icon.png'];

for (const f of required) {
  copyFileSync(join(root, f), join(build, f));
  console.log(`copied ${f} → build/`);
}
for (const f of optional) {
  if (existsSync(join(root, f))) {
    copyFileSync(join(root, f), join(build, f));
    console.log(`copied ${f} → build/`);
  } else {
    console.log(`skipped ${f} (not found — see images/README.md before sideloading)`);
  }
}
