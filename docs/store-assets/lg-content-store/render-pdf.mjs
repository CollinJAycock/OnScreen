// Renders ux-scenario.html to the PDF uploaded as the LG Seller Lounge
// "UX Scenario File" (Test Info). The test account's password is never
// committed: it comes from the REVIEWER_PASSWORD environment variable and is
// written only into the output PDF. Run from the repo root:
//
//   REVIEWER_PASSWORD='…' node docs/store-assets/lg-content-store/render-pdf.mjs out.pdf
//
// --preview renders without a password ("(entered at review)") to check the
// layout. Uses Playwright from web/node_modules.
import { createRequire } from 'node:module';
import { readFileSync, writeFileSync, rmSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const require = createRequire(path.resolve('web/package.json'));
const { chromium } = require('playwright');

const args = process.argv.slice(2);
const preview = args.includes('--preview');
const out = args.find((a) => !a.startsWith('--'));
if (!out) {
  console.error('usage: node render-pdf.mjs [--preview] <out.pdf>');
  process.exit(2);
}
const password = process.env.REVIEWER_PASSWORD;
if (!password && !preview) {
  console.error('REVIEWER_PASSWORD is not set (or pass --preview)');
  process.exit(2);
}

const dir = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'));
const html = readFileSync(path.join(dir, 'ux-scenario.html'), 'utf8')
  .replace('{{REVIEWER_PASSWORD}}', password || '(entered at review)');
// A sibling temp file, so the ux/ image paths resolve; removed afterwards.
const tmp = path.join(dir, '.ux-scenario.render.html');
writeFileSync(tmp, html);
try {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.goto(pathToFileURL(tmp).href, { waitUntil: 'networkidle' });
  await page.pdf({ path: out, format: 'A4', printBackground: true, preferCSSPageSize: true });
  await browser.close();
} finally {
  rmSync(tmp, { force: true });
}
console.log('wrote', out);
