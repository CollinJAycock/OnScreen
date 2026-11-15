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

  // The TV enforces config.xml's policy only in part (workers, plugin
  // data): with it alone an injected inline script, handler and remote
  // script all ran on the Q80B. The page's own <meta> policy stops them.
  const meta = /<meta\s+http-equiv="Content-Security-Policy"\s+content="([^"]*)"/.exec(html);
  const directives = (csp: string) =>
    new Map(
      csp
        .split(';')
        .map((d) => d.trim().split(/\s+/))
        .filter((t) => t[0])
        .map((t) => [t[0], t.slice(1).filter((s) => s !== 'ws:' && s !== 'wss:').join(' ')]),
    );

  it('carries a <meta> content security policy before the first script', () => {
    expect(meta).not.toBeNull();
    expect(html.indexOf('Content-Security-Policy')).toBeLessThan(html.indexOf('<script'));
  });

  it("refuses remote scripts and eval in the <meta> policy", () => {
    const scriptSrc = directives(meta?.[1] ?? '').get('script-src') ?? '';
    expect(scriptSrc).toBe("'self' 'unsafe-inline'");
  });

  it("keeps the <meta> policy and config.xml's in step (bar the dev server's ws:)", () => {
    const config = readFileSync(new URL('../config.xml', import.meta.url), 'utf8');
    const widget = /<tizen:content-security-policy>([^<]*)</.exec(config)?.[1] ?? '';
    expect(directives(meta?.[1] ?? '')).toEqual(directives(widget));
  });

  it('hashes the inline scripts into both policies at build time', () => {
    const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')) as {
      scripts: Record<string, string>;
    };
    const build = pkg.scripts.build;
    expect(build).toContain('csp-hashes.mjs index.html');
    expect(build).toContain('csp-hashes.mjs config.xml');
    // After the last step that rewrites index.html; a later rewrite breaks a hash.
    expect(build.indexOf('csp-hashes.mjs index.html')).toBeGreaterThan(build.indexOf('rewrite-file-paths'));
  });

  // The polyfills run as plain script on a browser that predates them: no
  // syntax past what Chromium 69 parses.
  it('writes the polyfills in syntax Chromium 69 parses', () => {
    const code = script.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
    expect(code).not.toMatch(/\?\.|\?\?/);
  });
});
