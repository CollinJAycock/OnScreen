import { describe, expect, it } from 'vitest';

// Tizen 5.5 (the oldest the app supports, Samsung's 2020 sets) runs
// Chromium 69; the 2022 Q80B's Tizen 6.5 runs 85. Their CSS falls short in
// ways that fail quietly: a rule whose selector list has one selector it
// doesn't know (:focus-visible is Chrome 86, :is / :where 88) is dropped
// whole, and flexbox `gap` (Chrome 84) is ignored, leaving items touching.
// Grid `gap` is fine (Chrome 66), as are margins. Every component's styles
// and the global sheet, as written.

const sheets = import.meta.glob(['./**/*.{svelte,css}'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/** Every match of a global regex (String.matchAll is ES2020, past the
 *  project's lib). */
function matches(text: string, re: RegExp): RegExpExecArray[] {
  const out: RegExpExecArray[] = [];
  for (let m = re.exec(text); m; m = re.exec(text)) out.push(m);
  return out;
}

/** A file's CSS: a component's <style> blocks, or a .css file whole, with
 *  comments out. */
function cssOf(path: string, text: string): string {
  const css = path.endsWith('.css') ? text : matches(text, /<style[^>]*>([\s\S]*?)<\/style>/g).map((m) => m[1]).join('\n');
  return css.replace(/\/\*[\s\S]*?\*\//g, '');
}

/** The innermost rules: selector and declarations. */
function rules(css: string): { selector: string; body: string }[] {
  return matches(css, /([^{}]*)\{([^{}]*)\}/g).map((m) => ({ selector: m[1].trim(), body: m[2] }));
}

const all = Object.entries(sheets).flatMap(([path, text]) =>
  rules(cssOf(path, text)).map((r) => ({ path, ...r })),
);

describe('CSS for Chromium 69 (Tizen 5.5)', () => {
  it('reads every component', () => {
    expect(Object.keys(sheets).length).toBeGreaterThan(30);
    expect(all.length).toBeGreaterThan(300);
  });

  it('uses no selector Chromium 69 drops a rule over', () => {
    const bad = all.filter((r) => /:focus-visible|:is\(|:where\(|:has\(/.test(r.selector));
    expect(bad.map((r) => `${r.path}: ${r.selector}`)).toEqual([]);
  });

  it('uses gap on grids only, never on flexbox', () => {
    const bad = all.filter(
      (r) => /(^|[;\s])(row-|column-)?gap\s*:/.test(r.body) && !/display\s*:\s*(inline-)?grid/.test(r.body),
    );
    expect(bad.map((r) => `${r.path}: ${r.selector}`)).toEqual([]);
  });

  // `inset` is fine as written: the build's cssTarget (chrome69) writes it
  // out as top / right / bottom / left. aspect-ratio (Chrome 88) has no
  // such fallback.
  it('uses no aspect-ratio', () => {
    const bad = all.filter((r) => /(^|[;\s])aspect-ratio\s*:/.test(r.body));
    expect(bad.map((r) => `${r.path}: ${r.selector}`)).toEqual([]);
  });
});
