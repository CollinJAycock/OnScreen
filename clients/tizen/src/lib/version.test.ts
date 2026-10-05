import { describe, expect, it } from 'vitest';
import pkg from '../../package.json';
import lock from '../../package-lock.json';
import { APP_VERSION } from './version';

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  readFileSync(path: URL, encoding: 'utf8'): string;
};

// The version lives in four places (see version.ts); the store reads
// config.xml, Settings → About shows APP_VERSION.
describe('APP_VERSION', () => {
  it('matches config.xml, package.json and package-lock.json', () => {
    const xml = readFileSync(new URL('../../config.xml', import.meta.url), 'utf8');
    expect(/<widget[^>]*\sversion="([^"]+)"/.exec(xml)?.[1]).toBe(APP_VERSION);
    expect(pkg.version).toBe(APP_VERSION);
    expect(lock.version).toBe(APP_VERSION);
    expect(lock.packages[''].version).toBe(APP_VERSION);
  });
});
