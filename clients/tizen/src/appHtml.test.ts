import { describe, expect, it } from 'vitest';

// The page shell's polyfills: what the bundle uses that Tizen 5.5's
// Chromium 69 lacks (the build only lowers syntax, not methods).

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  readFileSync(path: URL, encoding: 'utf8'): string;
};
const html = readFileSync(new URL('./app.html', import.meta.url), 'utf8');
const script = /<script>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? '';

describe('app.html polyfills for Chromium 69', () => {
  for (const [name, guard] of [
    ['globalThis (71)', "typeof globalThis === 'undefined'"],
    ['queueMicrotask (71)', "typeof window.queueMicrotask !== 'function'"],
    ['Object.fromEntries (73)', '!Object.fromEntries'],
    ['String.replaceAll (85)', '!String.prototype.replaceAll'],
    ['Array.at (92)', '!Array.prototype.at'],
    ['Object.hasOwn (93)', '!Object.hasOwn'],
  ] as const) {
    it(`polyfills ${name}`, () => {
      expect(script).toContain(guard);
    });
  }

  it('leaves the content security policy to config.xml', () => {
    expect(html).not.toContain('Content-Security-Policy');
  });

  // The polyfills run as plain script on a browser that predates them: no
  // syntax past what Chromium 69 parses.
  it('writes the polyfills in syntax Chromium 69 parses', () => {
    const code = script.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
    expect(code).not.toMatch(/\?\.|\?\?/);
  });
});
