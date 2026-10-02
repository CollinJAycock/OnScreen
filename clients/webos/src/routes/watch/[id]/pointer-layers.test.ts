import { describe, expect, it } from 'vitest';

// The player page's CSS as the Magic Remote's pointer meets it. The page
// needs a video element and hls.js to run, so these read its styles.

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  readFileSync(path: URL, encoding: 'utf8'): string;
};

const source = readFileSync(new URL('./+page.svelte', import.meta.url), 'utf8');
const style = source.slice(source.indexOf('<style>')).replace(/\/\*[\s\S]*?\*\//g, '');

/** The declarations of the rule whose selector list is exactly `selector`. */
function rule(selector: string): string {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = new RegExp(`(?:^|\\})\\s*${esc}\\s*\\{([^}]*)\\}`).exec(style);
  expect(m, `no "${selector}" rule`).not.toBeNull();
  return m![1];
}

const prop = (body: string, name: string) => new RegExp(`(?:^|;|\\s)${name}\\s*:\\s*([^;]+)`).exec(body)?.[1].trim();

describe('the player under the pointer', () => {
  // The action and transport rows run the width of the controls; with
  // pointer-events on the rows themselves they covered Skip Intro /
  // Skip Credits (bottom right), which then took no click.
  it("lets clicks through the rows' empty width, to Skip Intro under them", () => {
    expect(prop(rule('.actions'), 'pointer-events')).toBe('none');
    expect(prop(rule('.transport'), 'pointer-events')).toBe('none');
    expect(prop(rule('.action'), 'pointer-events')).toBe('auto');
    expect(prop(rule('.transport-button'), 'pointer-events')).toBe('auto');
  });

  it('draws Skip Intro / Credits over the controls, under a picker', () => {
    const skip = Number(prop(rule('.skip-marker'), 'z-index'));
    const picker = Number(prop(rule('.picker'), 'z-index'));
    expect(skip).toBeGreaterThan(0);
    expect(skip).toBeLessThan(picker);
  });
});
