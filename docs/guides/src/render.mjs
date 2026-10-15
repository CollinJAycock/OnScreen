// Renders a guide's HTML to PDF with Chromium (Playwright from web/node_modules).
// Usage: node docs/guides/src/render.mjs <guide.html> <out.pdf> "<footer title>"
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import path from 'node:path';

const require = createRequire(path.resolve('web/package.json'));
const { chromium } = require('playwright');

const [, , htmlPath, outPath, title = 'OnScreen'] = process.argv;
if (!htmlPath || !outPath) {
  console.error('usage: node render.mjs <guide.html> <out.pdf> "<footer title>"');
  process.exit(2);
}

const browser = await chromium.launch();
const page = await browser.newPage();
await page.goto(pathToFileURL(path.resolve(htmlPath)).href, { waitUntil: 'networkidle' });
await page.emulateMedia({ media: 'print' });
await page.pdf({
  path: outPath,
  format: 'Letter',
  printBackground: true,
  displayHeaderFooter: true,
  margin: { top: '0.7in', bottom: '0.75in', left: '0.75in', right: '0.75in' },
  headerTemplate: '<div></div>',
  footerTemplate: `<div style="width:100%;font:9px system-ui,sans-serif;color:#666;padding:0 0.75in;display:flex;justify-content:space-between">
    <span>${title}</span><span>Page <span class="pageNumber"></span> of <span class="totalPages"></span></span></div>`,
});
await browser.close();
console.log('wrote', outPath);
