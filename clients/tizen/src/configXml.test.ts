import { describe, expect, it } from 'vitest';
import pkg from '../package.json';

// config.xml, the widget manifest Tizen reads: what the app is allowed to
// call, and what makes the remote's keys reach it.

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  readFileSync(path: URL, encoding: 'utf8'): string;
};
const xml = readFileSync(new URL('../config.xml', import.meta.url), 'utf8').replace(/<!--[\s\S]*?-->/g, '');
const privileges: string[] = [];
const privilegeRe = /<tizen:privilege name="([^"]+)"/g;
for (let m = privilegeRe.exec(xml); m; m = privilegeRe.exec(xml)) privileges.push(m[1]);

describe('config.xml', () => {
  it('forwards the hardware keys (Return would otherwise close the app)', () => {
    expect(xml).toMatch(/<tizen:setting[^>]*hwkey-event="enable"/);
  });

  it('declares the privileges the code calls, and only those', () => {
    expect(privileges.sort()).toEqual(
      [
        'http://developer.samsung.com/privilege/productinfo',
        'http://tizen.org/privilege/avplay',
        'http://tizen.org/privilege/internet',
      ].sort(),
    );
  });

  it('supports Tizen 5.5 and later, the floor the CSS and polyfills are written for', () => {
    expect(xml).toMatch(/required_version="5\.5"/);
  });

  it('carries the version package.json does', () => {
    const v = /<widget[^>]*\sversion="([^"]+)"/.exec(xml)?.[1];
    expect(v).toBe(pkg.version);
  });
});
