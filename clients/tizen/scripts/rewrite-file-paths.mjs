// Post-build asset-path rewriter for file:// deployment. See the
// webOS sibling script (clients/webos/scripts/rewrite-file-paths.mjs)
// for the full rationale — same SvelteKit-on-file-protocol gotcha.
//
// Short version: SvelteKit emits `<link href="/_app/..." rel=
// "modulepreload">` in the prerendered HTML even with
// kit.paths.relative=true. On file:// the leading `/` resolves to
// the filesystem root, blowing up every chunk load with CORS. We
// rewrite those to `./_app/...` post-build so the chunks load
// relative to the app sandbox.

import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';

const root = dirname(import.meta.dirname);
const buildDir = join(root, 'build');

let rewrittenFiles = 0;
let rewrittenOccurrences = 0;

function walk(dir) {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    const s = statSync(path);
    if (s.isDirectory()) {
      walk(path);
    } else if (entry.endsWith('.html')) {
      rewriteOne(path);
    }
  }
}

function rewriteOne(path) {
  const before = readFileSync(path, 'utf8');
  const re = /([="'(])\/_app\//g;
  const after = before.replace(re, (_, lead) => `${lead}./_app/`);
  if (after === before) return;

  const count = (before.match(re) ?? []).length;
  writeFileSync(path, after);
  rewrittenFiles++;
  rewrittenOccurrences += count;
  console.log(`rewrote ${count} path(s) in ${path.slice(root.length + 1)}`);
}

walk(buildDir);
console.log(`rewrote ${rewrittenOccurrences} occurrence(s) across ${rewrittenFiles} file(s)`);
