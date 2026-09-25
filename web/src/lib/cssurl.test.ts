import { describe, expect, it } from 'vitest';
import { cssUrl } from './cssurl';

describe('cssUrl', () => {
  it('passes an ordinary artwork URL through unchanged', () => {
    expect(cssUrl('/artwork/Movies/Film (2010)/fanart.jpg?v=1&w=640')).toBe(
      'url("/artwork/Movies/Film%20%282010%29/fanart.jpg?v=1&w=640")'
    );
  });

  it('leaves nothing that can close the url() or the declaration', () => {
    const hostile = `/artwork/x');background:red;x:url('"\\\n)`;
    const out = cssUrl(hostile);
    const inner = out.slice('url("'.length, -'")'.length);
    expect(inner).not.toMatch(/["'()\\\s]/);
    expect(out.startsWith('url("')).toBe(true);
    expect(out.endsWith('")')).toBe(true);
  });

  it('percent-encodes apostrophes the server decodes back to the same path', () => {
    expect(cssUrl("/artwork/Schindler's List/fanart.jpg")).toBe(
      'url("/artwork/Schindler%27s%20List/fanart.jpg")'
    );
  });
});
