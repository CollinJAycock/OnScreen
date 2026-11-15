// Post-build: the content security policy names the app's inline scripts by
// their sha256 hashes instead of allowing every inline script.
//
// The build is one index.html whose scripts are all inline (SvelteKit's
// bundleStrategy 'inline', plus app.html's polyfills), so script-src needed
// 'unsafe-inline', and that let any future markup injection run its own
// inline script or handler and read the refresh token from localStorage
// (connect-src / img-src * to send it anywhere). With the hashes only these
// exact scripts run; Chromium has taken script hashes since long before the
// oldest engine the apps support (Chromium 68).
//
// The source app.html keeps 'unsafe-inline': `vite dev` serves its own
// inline scripts, whose hashes change on every edit. Run after every other
// step that rewrites index.html (rewrite-file-paths): any change to a script
// after this one breaks its hash, and the app stays on a black screen.
//
//   node scripts/csp-hashes.mjs index.html   the <meta> CSP in build/index.html (webOS)
//   node scripts/csp-hashes.mjs config.xml   <tizen:content-security-policy> in build/config.xml (Tizen)

import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const JS_TYPES = new Set(['', 'module', 'text/javascript', 'application/javascript']);

/** The text of each inline script in `html` the browser would run, as it
 *  hashes it: the HTML parser turns CRLF and CR into LF before anything
 *  reads the text (the checkout's app.html can be CRLF). Scripts with a src
 *  and data blocks (application/json) are skipped. */
export function inlineScripts(html) {
  const out = [];
  for (const m of html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script\s*>/gi)) {
    const attrs = m[1];
    if (/\ssrc\s*=/i.test(` ${attrs}`)) continue;
    const type = (/\btype\s*=\s*["']?([^"'\s>]*)/i.exec(attrs)?.[1] ?? '').toLowerCase();
    if (!JS_TYPES.has(type)) continue;
    out.push(m[2].replace(/\r\n?/g, '\n'));
  }
  return out;
}

/** The CSP source expression for a script's text. */
export function scriptHash(text) {
  return `'sha256-${createHash('sha256').update(text, 'utf8').digest('base64')}'`;
}

/** `csp` with its script-src allowing exactly `hashes` inline: 'unsafe-inline'
 *  and any earlier hashes out, these in, the other sources kept. Throws when
 *  the policy has no script-src (the step would otherwise do nothing). */
export function withScriptHashes(csp, hashes) {
  let found = false;
  const out = csp.split(';').map((directive) => {
    const tokens = directive.trim().split(/\s+/);
    if (tokens[0]?.toLowerCase() !== 'script-src') return directive;
    found = true;
    const kept = tokens.slice(1).filter((t) => t !== "'unsafe-inline'" && !/^'sha(256|384|512)-/.test(t));
    const lead = directive.match(/^\s*/)?.[0] ?? '';
    return `${lead}${['script-src', ...kept, ...hashes].join(' ')}`;
  });
  if (!found) throw new Error('csp-hashes: the policy has no script-src');
  return out.join(';');
}

/** build/index.html with its <meta http-equiv="Content-Security-Policy">
 *  rewritten for the page's own inline scripts. */
export function hashMetaCsp(html) {
  const hashes = inlineScripts(html).map(scriptHash);
  if (hashes.length === 0) throw new Error('csp-hashes: no inline script in index.html');
  let found = false;
  const out = html.replace(
    /(<meta\s+http-equiv="Content-Security-Policy"\s+content=")([^"]*)(")/i,
    (_, a, csp, b) => {
      found = true;
      return a + withScriptHashes(csp, hashes) + b;
    },
  );
  if (!found) throw new Error('csp-hashes: no Content-Security-Policy <meta> in index.html');
  return out;
}

/** config.xml with <tizen:content-security-policy> rewritten for index.html's
 *  inline scripts. */
export function hashConfigCsp(configXml, html) {
  const hashes = inlineScripts(html).map(scriptHash);
  if (hashes.length === 0) throw new Error('csp-hashes: no inline script in index.html');
  let found = false;
  const out = configXml.replace(
    /(<tizen:content-security-policy>)([\s\S]*?)(<\/tizen:content-security-policy>)/,
    (_, a, csp, b) => {
      found = true;
      return a + withScriptHashes(csp, hashes) + b;
    },
  );
  if (!found) throw new Error('csp-hashes: no <tizen:content-security-policy> in config.xml');
  return out;
}

function main(target) {
  const build = join(import.meta.dirname, '..', 'build');
  const indexPath = join(build, 'index.html');
  const html = readFileSync(indexPath, 'utf8');
  const n = inlineScripts(html).length;
  if (target === 'index.html') {
    writeFileSync(indexPath, hashMetaCsp(html));
  } else if (target === 'config.xml') {
    const configPath = join(build, 'config.xml');
    writeFileSync(configPath, hashConfigCsp(readFileSync(configPath, 'utf8'), html));
  } else {
    throw new Error(`csp-hashes: say which policy (index.html or config.xml), not ${target}`);
  }
  console.log(`csp-hashes: script-src lists the ${n} inline script(s) by hash (${target})`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv[2]);
}
