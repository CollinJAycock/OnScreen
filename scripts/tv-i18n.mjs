#!/usr/bin/env node
// The TV web apps' translations are the same in all three apps (LG webOS,
// Samsung Tizen, Xbox): src/lib/i18n is one folder kept identical in each,
// and its index.svelte.ts is the web app's module, copied as is
// (docs/i18n.md). Each app stays self-contained (CI builds it from its own
// folder, and the release embeds clients/xbox alone), so the copies are kept
// in step by this script instead of being shared.
//
//   node scripts/tv-i18n.mjs                check: exit 1 when a copy differs
//   node scripts/tv-i18n.mjs --sync         copy webOS's folder to the others,
//                                           and the web app's module to all three
//   node scripts/tv-i18n.mjs --sync --from tizen   use another app's folder
//
// Line endings are ignored (a Windows checkout may have CRLF). --sync never
// deletes: a file only a sibling has is reported, for you to remove.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const APPS = ['webos', 'tizen', 'xbox'];
const WEB_MODULE = 'web/src/lib/i18n/index.svelte.ts';
const folder = (app) => `clients/${app}/src/lib/i18n`;

const args = process.argv.slice(2);
const sync = args.includes('--sync');
const fromAt = args.indexOf('--from');
const from = fromAt >= 0 ? args[fromAt + 1] : 'webos';
if (!APPS.includes(from)) {
  console.error(`--from must be one of ${APPS.join(', ')}`);
  process.exit(2);
}

const abs = (p) => path.join(root, p);
const read = (p) => (fs.existsSync(abs(p)) ? fs.readFileSync(abs(p), 'utf8').replace(/\r\n/g, '\n') : null);

/** Every file under dir, relative to it, '/'-separated. */
function files(dir) {
  const out = [];
  const walk = (d) => {
    for (const e of fs.readdirSync(abs(d), { withFileTypes: true })) {
      const p = `${d}/${e.name}`;
      if (e.isDirectory()) walk(p);
      else out.push(p.slice(dir.length + 1));
    }
  };
  if (fs.existsSync(abs(dir))) walk(dir);
  return out.sort();
}

function write(p, text) {
  fs.mkdirSync(path.dirname(abs(p)), { recursive: true });
  fs.writeFileSync(abs(p), text);
}

const problems = [];
const webModule = read(WEB_MODULE);
if (webModule === null) problems.push(`missing ${WEB_MODULE}`);

if (sync) {
  const source = folder(from);
  // The module comes from the web app; everything else from the source app.
  if (webModule !== null) write(`${source}/index.svelte.ts`, webModule);
  for (const app of APPS) {
    if (app === from) continue;
    for (const f of files(source)) {
      const text = read(`${source}/${f}`);
      if (read(`${folder(app)}/${f}`) !== text) {
        write(`${folder(app)}/${f}`, text);
        console.log(`updated ${folder(app)}/${f}`);
      }
    }
    for (const f of files(folder(app))) {
      if (read(`${source}/${f}`) === null) problems.push(`${folder(app)}/${f} is not in ${source}: remove it, or add it there`);
    }
  }
} else {
  const all = new Set(APPS.flatMap((app) => files(folder(app))));
  for (const f of [...all].sort()) {
    const versions = APPS.map((app) => read(`${folder(app)}/${f}`));
    if (versions.some((v) => v === null)) {
      problems.push(`${f}: only in ${APPS.filter((_, i) => versions[i] !== null).join(', ')}`);
    } else if (new Set(versions).size > 1) {
      problems.push(`${f}: differs between ${APPS.join(', ')}`);
    }
  }
  for (const app of APPS) {
    const m = read(`${folder(app)}/index.svelte.ts`);
    if (webModule !== null && m !== webModule) problems.push(`${folder(app)}/index.svelte.ts differs from ${WEB_MODULE}`);
  }
}

if (problems.length) {
  for (const p of problems) console.error(p);
  if (!sync) console.error('\nRun `node scripts/tv-i18n.mjs --sync` after editing the webOS copy (docs/i18n.md).');
  process.exit(1);
}
console.log(sync ? 'TV translations in step.' : 'TV translations are the same in every app.');
