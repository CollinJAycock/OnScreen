import { copyFileSync } from 'node:fs';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');
const build = join(root, 'build');

for (const f of ['appinfo.json', 'icon.png', 'largeIcon.png']) {
  copyFileSync(join(root, f), join(build, f));
  console.log(`copied ${f} → build/`);
}
