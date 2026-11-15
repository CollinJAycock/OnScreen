import { describe, expect, it } from 'vitest';
import { hashConfigCsp, hashMetaCsp, inlineScripts, scriptHash, withScriptHashes } from './csp-hashes.mjs';

// The CSP spec's own example: <script>alert('Hello, world.');</script>.
const HELLO = "'sha256-qznLcsROx4GACP2dm0UCKCzCG+HiZ1guq6ZZDob/Tng='";

const POLICY =
  "default-src 'self'; script-src 'self' 'unsafe-inline'; worker-src 'self' blob:; style-src 'self' 'unsafe-inline'; object-src 'none'";

describe('scriptHash', () => {
  it("hashes a script's text as the browser does", () => {
    expect(scriptHash("alert('Hello, world.');")).toBe(HELLO);
  });
});

describe('inlineScripts', () => {
  it('reads each inline script, with CRLF / CR as the parser leaves them (LF)', () => {
    const html = "<script>\r\nalert('Hello, world.');\r</script><script type=\"module\">b()</script>";
    expect(inlineScripts(html)).toEqual(["\nalert('Hello, world.');\n", 'b()']);
  });

  it('skips scripts with a src and data blocks', () => {
    const html =
      '<script src="x.js"></script><script type="application/json">{"a":1}</script>' +
      '<script data-src="y">run()</script>';
    expect(inlineScripts(html)).toEqual(['run()']);
  });

  it("ends a script at its first </script>, as the parser does (an escaped one inside doesn't)", () => {
    const html = `<script>document.write('<script src="a.js"><\\/script>');</script>`;
    expect(inlineScripts(html)).toEqual([`document.write('<script src="a.js"><\\/script>');`]);
  });
});

describe('withScriptHashes', () => {
  it("swaps 'unsafe-inline' in script-src for the hashes, and only there", () => {
    const out = withScriptHashes(POLICY, [HELLO, "'sha256-abc='"]);
    expect(out).toContain(`script-src 'self' ${HELLO} 'sha256-abc='`);
    expect(out).toContain("style-src 'self' 'unsafe-inline'");
    expect(out.match(/'unsafe-inline'/g)).toHaveLength(1);
    expect(out).toContain("default-src 'self'; ");
  });

  it('replaces the hashes of an earlier run (idempotent)', () => {
    const once = withScriptHashes(POLICY, ["'sha256-old='"]);
    expect(withScriptHashes(once, [HELLO])).toBe(withScriptHashes(POLICY, [HELLO]));
  });

  it('refuses a policy without script-src', () => {
    expect(() => withScriptHashes("default-src 'self'", [HELLO])).toThrow(/script-src/);
  });
});

describe('the built page', () => {
  const page = (csp) =>
    `<!DOCTYPE html><html><head><meta http-equiv="Content-Security-Policy" content="${csp}" />` +
    "<script>\r\nalert('Hello, world.');\r\n</script></head><body><script>\n\t\tboot()\n</script></body></html>";

  it("webOS: the <meta> policy lists every inline script's hash, and no 'unsafe-inline' for scripts", () => {
    const out = hashMetaCsp(page(POLICY));
    const csp = /content="([^"]*)"/.exec(out)[1];
    const scriptSrc = csp.split(';').find((d) => d.trim().startsWith('script-src'));
    expect(scriptSrc).not.toContain("'unsafe-inline'");
    for (const text of inlineScripts(out)) expect(scriptSrc).toContain(scriptHash(text));
    expect(scriptSrc).toContain(scriptHash("\nalert('Hello, world.');\n"));
  });

  it('Tizen: config.xml carries the hashes of index.html\'s scripts', () => {
    const config = `<widget><tizen:content-security-policy>${POLICY}</tizen:content-security-policy></widget>`;
    const html = page("default-src 'self'; script-src 'self'");
    const out = hashConfigCsp(config, html);
    const csp = /<tizen:content-security-policy>([^<]*)</.exec(out)[1];
    expect(csp).not.toMatch(/script-src[^;]*'unsafe-inline'/);
    for (const text of inlineScripts(html)) expect(csp).toContain(scriptHash(text));
  });

  it('fails the build when there is nothing to rewrite', () => {
    expect(() => hashMetaCsp('<script>a()</script>')).toThrow(/meta/);
    expect(() => hashConfigCsp('<widget/>', '<script>a()</script>')).toThrow(/config.xml/);
  });
});
