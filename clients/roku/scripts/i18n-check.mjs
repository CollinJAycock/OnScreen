// Checks the channel's translations (source/util/I18n.brs, docs/i18n.md):
//
//   - i18n/en.json and es.json: the same keys, the same {placeholders}
//     per key (lowercase names), no empty strings, both forms of a plural
//     (_one / _other), no two keys that differ only in case (BrightScript
//     AAs ignore case);
//   - every key the code names exists in en.json, and every key in en.json
//     is used; I18n_TP / I18n_TN calls fill exactly the key's placeholders;
//   - no user-visible English is left in the code: string literals in
//     components/ and source/ that read as words, and text attributes in
//     the components' XML, outside the contexts and the allowlist below.
//
// Run by `npm test` (and `npm run i18n`). Exits 1 with a list of problems.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const LANGS = ['en', 'es'];
const problems = [];
const fail = (msg) => problems.push(msg);

// Literals that read as words but are not interface text, each with why.
const ALLOWED_LITERALS = new Map([
  ['OnScreen', 'product name, never translated'],
  ['Roku', 'the client name sent to the server (DeepLink_ClientName)'],
  ['SSO', 'fallback name of an OIDC provider (a service name)'],
  ['SAML', 'protocol name'],
  ['HEVC', 'codec name'],
  ['H.264', 'codec name'],
  [' Mbps', 'unit'],
  [' kbps', 'unit'],
  ['main 10', 'HEVC profile for roDeviceInfo.CanDecodeVideo'],
  ['Bearer ', 'Authorization scheme'],
  ['Accept', 'HTTP header name'],
  ['Accept-Language', 'HTTP header name'],
  ['Content-Type', 'HTTP header name'],
  ['X-Client-Capabilities', 'HTTP header name'],
  ['Español', "the language's own name (I18n_LanguageName)"],
  ['English', "the language's own name (I18n_LanguageName)"],
  ['https://onscreen.wolverscreen.com/privacy', 'address'],
  ['github.com/CollinJAycock/OnScreen', 'address'],
  ['http://192.168.1.50:7070', 'example address'],
]);

// XML attribute values that may stay in the markup.
const ALLOWED_XML_TEXT = new Set(['OnScreen']);

// A literal is an argument of one of these calls (node, field, beacon and
// header names, menu ids, registry keys) rather than text on screen.
const NAME_CALL = /(?:createObject|CreateObject|createChild|callFunc|signalBeacon|findNode|observeField|observeFieldScoped|unobserveField|addField|hasField|getField|setField|AddHeader|SetRequest|SetMessagePort|Menu_Show|Menu_ShowMessage|Prefs_Get|Prefs_Set|Prefs_Delete|Read|Write|Exists|Delete|getRoSGNode|subtype)\s*\(\s*(?:[^()]*,\s*)?$/;

// Node fields whose values are firmware enums, not text.
const ENUM_FIELD = /\.(?:globalCaptionMode|streamFormat|control|horizAlign|vertAlign|font|keyboardDomain|loadDisplayMode)\s*=\s*$/;

function walk(dir, ext, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, ext, out);
    else if (p.endsWith(ext)) out.push(p);
  }
  return out;
}
const rel = (p) => relative(root, p).split('\\').join('/');

// ── Catalogs ──────────────────────────────────────────────────────────

const placeholders = (text) => [...text.matchAll(/\{([^{}]*)\}/g)].map((m) => m[1]).sort();
const catalogs = {};
for (const lang of LANGS) {
  const path = join(root, 'i18n', `${lang}.json`);
  const raw = readFileSync(path, 'utf8');
  if (raw.charCodeAt(0) === 0xfeff) fail(`i18n/${lang}.json: starts with a BOM`);
  let cat;
  try {
    cat = JSON.parse(raw);
  } catch (e) {
    fail(`i18n/${lang}.json: ${e.message}`);
    continue;
  }
  // Duplicates (JSON.parse keeps the last) and case-only twins.
  const seen = new Map();
  for (const m of raw.matchAll(/^\s*"((?:[^"\\]|\\.)*)"\s*:/gm)) {
    const k = m[1];
    const lower = k.toLowerCase();
    if (seen.has(lower)) fail(`i18n/${lang}.json: "${k}" repeats "${seen.get(lower)}" (keys are compared without case)`);
    seen.set(lower, k);
  }
  for (const [k, v] of Object.entries(cat)) {
    if (typeof v !== 'string' || v === '') fail(`i18n/${lang}.json: "${k}" is empty or not a string`);
    else for (const p of placeholders(v)) if (!/^[a-z0-9]+$/.test(p)) fail(`i18n/${lang}.json: "${k}" has placeholder {${p}}: use lowercase letters and digits`);
    const plural = /_(one|other)$/.exec(k);
    if (plural) {
      const twin = k.replace(/_(one|other)$/, plural[1] === 'one' ? '_other' : '_one');
      if (!(twin in cat)) fail(`i18n/${lang}.json: "${k}" has no "${twin}"`);
    }
  }
  catalogs[lang] = cat;
}

const en = catalogs.en ?? {};
for (const lang of LANGS.filter((l) => l !== 'en')) {
  const cat = catalogs[lang];
  if (!cat) continue;
  for (const k of Object.keys(en)) {
    if (!(k in cat)) fail(`i18n/${lang}.json: missing "${k}"`);
    else if (placeholders(en[k]).join() !== placeholders(cat[k]).join()) fail(`i18n/${lang}.json: "${k}" has {${placeholders(cat[k]).join('}, {')}}, English has {${placeholders(en[k]).join('}, {')}}`);
  }
  for (const k of Object.keys(cat)) if (!(k in en)) fail(`i18n/${lang}.json: "${k}" is not in en.json`);
}

// ── Code ──────────────────────────────────────────────────────────────

// A BrightScript line split into code (comments dropped) and its string
// literals with the code that precedes each.
function scanLine(line) {
  const lits = [];
  let code = '';
  let i = 0;
  while (i < line.length) {
    const c = line[i];
    if (c === "'") break;
    if (/^rem(\s|$)/i.test(line.slice(i)) && (i === 0 || /[\s:]/.test(line[i - 1]))) break;
    if (c === '"') {
      let j = i + 1;
      let s = '';
      while (j < line.length) {
        if (line[j] === '"' && line[j + 1] === '"') { s += '"'; j += 2; continue; }
        if (line[j] === '"') break;
        s += line[j];
        j++;
      }
      lits.push({ value: s, before: code });
      code += '""';
      i = j + 1;
      continue;
    }
    code += c;
    i++;
  }
  return { code, lits };
}

const KEY_SHAPE = /^[a-z]+\.[a-z][A-Za-z0-9]*$/;
// SceneGraph component names (navigateTo / createObject targets, routes).
const COMPONENTS = new Set(
  walk(join(root, 'components'), '.xml').flatMap((f) => [...readFileSync(f, 'utf8').matchAll(/<component\s+name="([^"]+)"/g)].map((m) => m[1])),
);
const used = new Set();
const looksLikeWords = (s) => /[A-Za-z]{2,}/.test(s) && (/\s/.test(s) || /^[A-Z][a-z]/.test(s));

const files = [...walk(join(root, 'components'), '.brs'), ...walk(join(root, 'source'), '.brs')];
for (const file of files) {
  const lines = readFileSync(file, 'utf8').split(/\r?\n/);
  lines.forEach((line, n) => {
    const where = `${rel(file)}:${n + 1}`;
    const { lits } = scanLine(line);
    for (const lit of lits) {
      const v = lit.value;
      // A key: named by an I18n call, or held in a table that feeds one.
      if (KEY_SHAPE.test(v)) {
        if (v in en) used.add(v);
        else if (`${v}_one` in en && `${v}_other` in en) { used.add(`${v}_one`); used.add(`${v}_other`); }
        else fail(`${where}: "${v}" is not a key in i18n/en.json`);
        continue;
      }
      if (/\bprint\b/i.test(lit.before)) continue; // the debug console (port 8085)
      if (COMPONENTS.has(v)) continue;
      if (!looksLikeWords(v)) continue;
      if (ALLOWED_LITERALS.has(v)) continue;
      if (NAME_CALL.test(lit.before)) continue;
      if (isComparison(lit.before)) continue; // compared with a server value or a type name
      if (ENUM_FIELD.test(lit.before)) continue;
      fail(`${where}: English literal "${v}" (use I18n_T and a key in i18n/en.json)`);
    }
    // Placeholders: I18n_T on a key that has some, and I18n_TP / I18n_TN
    // filling a different set.
    for (const call of i18nCalls(line)) {
      const key = /^"([^"]*)"$/.exec(call.args[0] ?? '')?.[1];
      if (key === undefined) continue; // a key from a table (checked above)
      const keys = call.fn === 'TN' ? [`${key}_one`, `${key}_other`] : [key];
      // The params: I18n_T has none; an AA literal is read; anything else
      // (a variable) is skipped.
      let given = null;
      if (call.fn === 'T') given = [];
      const paramsArg = call.fn === 'TP' ? call.args[1] : call.fn === 'TN' ? call.args[2] : undefined;
      if (call.fn === 'TN' && (paramsArg === undefined || paramsArg === 'invalid')) given = [];
      if (paramsArg !== undefined && paramsArg.startsWith('{')) given = aaKeys(paramsArg);
      for (const k of keys) {
        if (!(k in en)) { fail(`${where}: I18n_${call.fn} names "${k}", not in i18n/en.json`); continue; }
        if (given === null) continue;
        const want = new Set(placeholders(en[k]));
        if (call.fn === 'TN') want.delete('count');
        const g = new Set(given);
        const missing = [...want].filter((p) => !g.has(p));
        const extra = [...g].filter((p) => !want.has(p));
        if (missing.length) fail(`${where}: I18n_${call.fn}("${key}") doesn't fill {${missing.join('}, {')}}`);
        if (extra.length) fail(`${where}: I18n_${call.fn}("${key}") fills {${extra.join('}, {')}}, which "${k}" doesn't have`);
      }
    }
  });
}

// True when the literal is the right side of a comparison (`x = "movie"`
// in a condition, `type(v) <> "String"`), not of an assignment.
function isComparison(before) {
  if (/<>\s*$/.test(before)) return true;
  if (!/=\s*$/.test(before)) return false;
  const assign = /(?:^|\bthen|\belse|:)\s*[A-Za-z_][\w.]*(?:\[[^\]]*\])?\s*=\s*$/;
  return !assign.test(before);
}

// The I18n_T / I18n_TP / I18n_TN calls on a line, each with its arguments
// as source text (split at top-level commas, strings and brackets kept
// whole).
function i18nCalls(line) {
  const out = [];
  const re = /\bI18n_(TP|TN|T)\(/g;
  let m;
  while ((m = re.exec(line))) {
    const args = [];
    let depth = 0;
    let cur = '';
    let i = m.index + m[0].length;
    for (; i < line.length; i++) {
      const c = line[i];
      if (c === '"') {
        let j = i + 1;
        while (j < line.length && !(line[j] === '"' && line[j + 1] !== '"')) j += line[j] === '"' ? 2 : 1;
        cur += line.slice(i, j + 1);
        i = j;
        continue;
      }
      if (c === '(' || c === '{' || c === '[') depth++;
      if (c === ')' || c === '}' || c === ']') {
        if (depth === 0) break;
        depth--;
      }
      if (c === ',' && depth === 0) {
        args.push(cur.trim());
        cur = '';
        continue;
      }
      cur += c;
    }
    if (cur.trim() !== '') args.push(cur.trim());
    out.push({ fn: m[1], args });
  }
  return out;
}

// The top-level keys of an AA literal's source, lowercased.
function aaKeys(src) {
  const inner = src.trim().replace(/^\{/, '').replace(/\}$/, '');
  const keys = [];
  let depth = 0;
  let cur = '';
  for (let i = 0; i <= inner.length; i++) {
    const c = inner[i];
    if (c === '"') {
      let j = i + 1;
      while (j < inner.length && inner[j] !== '"') j++;
      cur += inner.slice(i, j + 1);
      i = j;
      continue;
    }
    if (c === '(' || c === '{' || c === '[') depth++;
    if (c === ')' || c === '}' || c === ']') depth--;
    if ((c === ',' && depth === 0) || c === undefined) {
      const k = /^\s*([A-Za-z_][A-Za-z0-9_]*)\s*:/.exec(cur);
      if (k) keys.push(k[1].toLowerCase());
      cur = '';
      continue;
    }
    cur += c;
  }
  return keys;
}

for (const k of Object.keys(en)) if (!used.has(k)) fail(`i18n/en.json: "${k}" is never used`);

// ── Markup ────────────────────────────────────────────────────────────

const TEXT_ATTR = /\b(text|title|hintText|description|message|buttons)\s*=\s*"([^"]*)"/g;
for (const file of walk(join(root, 'components'), '.xml')) {
  const raw = readFileSync(file, 'utf8').replace(/<!--[\s\S]*?-->/g, (c) => c.replace(/[^\n]/g, ' '));
  for (const m of raw.matchAll(TEXT_ATTR)) {
    const v = m[2];
    if (!/[A-Za-z]{2,}/.test(v) || ALLOWED_XML_TEXT.has(v)) continue;
    const line = raw.slice(0, m.index).split('\n').length;
    fail(`${rel(file)}:${line}: ${m[1]}="${v}" in the markup (set it from init() with I18n_T)`);
  }
}

// ── Result ────────────────────────────────────────────────────────────

const keys = Object.keys(en).length;
if (problems.length) {
  console.error(`i18n-check: ${problems.length} problem(s)`);
  for (const p of problems) console.error(`  ${p}`);
  process.exit(1);
}
console.log(`i18n-check: ${keys} keys in ${LANGS.join(' + ')}, all used, placeholders match, no English left in the code`);
