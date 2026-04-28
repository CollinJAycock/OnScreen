// Bundle the Roku channel into a sideloadable .zip.
//
// Roku's bundler expects exactly: manifest, source/, components/,
// images/ (plus optional locale/ + cert files). Anything else in
// the zip is ignored at install time but bloats the upload, so we
// curate the file list explicitly rather than zipping the whole
// directory.
//
// Output: dist/onscreen-roku-<version>.zip
//
// The version comes from manifest's major/minor/build_version
// triplet — kept in sync with package.json's version field by hand
// (release process: bump both, regenerate the zip).

import { createWriteStream, existsSync, mkdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import archiver from 'archiver';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = resolve(__dirname, '..');
const distDir = join(root, 'dist');

// Roku-mandated top-level entries. `images/` is included even if
// only the README placeholder is there — the manifest references
// images by path, but Roku only fails install if a referenced
// image is missing. Empty images/ is fine.
const PATHS = ['manifest', 'source', 'components', 'images'];

function readManifestVersion() {
  const text = readFileSync(join(root, 'manifest'), 'utf8');
  const major = /^major_version=(\d+)$/m.exec(text)?.[1] ?? '0';
  const minor = /^minor_version=(\d+)$/m.exec(text)?.[1] ?? '0';
  const build = /^build_version=(\d+)$/m.exec(text)?.[1] ?? '0';
  return `${major}.${minor}.${build}`;
}

async function main() {
  if (!existsSync(distDir)) mkdirSync(distDir, { recursive: true });
  const version = readManifestVersion();
  const outPath = join(distDir, `onscreen-roku-${version}.zip`);

  await new Promise((resolveDone, rejectDone) => {
    const out = createWriteStream(outPath);
    const archive = archiver('zip', { zlib: { level: 9 } });

    out.on('close', () => {
      const sizeKb = (archive.pointer() / 1024).toFixed(1);
      console.log(`packaged: ${outPath} (${sizeKb} KiB)`);
      resolveDone();
    });
    archive.on('error', rejectDone);
    archive.pipe(out);

    for (const entry of PATHS) {
      const fullPath = join(root, entry);
      if (!existsSync(fullPath)) continue;
      const s = statSync(fullPath);
      if (s.isDirectory()) {
        archive.directory(fullPath, entry);
      } else {
        archive.file(fullPath, { name: entry });
      }
    }
    archive.finalize();
  });
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
