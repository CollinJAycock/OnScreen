// Post-build asset-path rewriter for file:// deployment.
//
// SvelteKit's `kit.paths.relative = true` makes the runtime `base`
// import return relative paths, but the *prerendered* HTML output
// still emits modulepreload + src + href links as absolute paths
// (e.g. `/_app/immutable/...`). Browsers resolve those against the
// document origin — fine over http://, but webOS / Tizen load the
// app from a file:// URL (`file://com.onscreen.tv-webos/index.html`)
// where the absolute `/` walks out of the app sandbox into the
// filesystem root, hits CORS, and fails every chunk load.
//
// Fix: walk the build directory and rewrite every `"/_app/...` and
// `'/_app/...` to its `./_app/...` relative equivalent inside the
// .html files. Idempotent — re-running on an already-fixed build
// is a no-op.
//
// Only touches .html files because that's where the absolute paths
// land; the actual JS chunks reference each other via relative
// module imports that already work.

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
  // Match `="/_app/`, `='/_app/`, `("/_app/`, `('/_app/`, `("` , etc.
  // The leading non-path char is captured so we don't accidentally
  // alter a path-as-text occurrence inside a JSON literal where the
  // surrounding quote matters.
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
