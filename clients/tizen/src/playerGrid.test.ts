import { describe, expect, it } from 'vitest';

// The AVPlay anchor (twice the screen, so the firmware's dot at its centre
// is off screen: avplayMedia.ts) widens the viewport position:fixed is laid
// out in to 4080x2295 on the Q80B. A fixed box tied to the viewport's edges
// or centre went with it: the player's bar, scrub preview, pickers, Up Next,
// the rebuffer spinner and "Starting playback…" all drew off the 1920x1080
// screen, and ←/→ seemed to do nothing (2026-11-19). They sit on the grid.

const page = Object.values(
  import.meta.glob('./routes/watch/\\[id\\]/+page.svelte', { query: '?raw', import: 'default', eager: true }),
)[0] as string;
const loadingLayer = Object.values(
  import.meta.glob('./lib/player/loadingLayer.ts', { query: '?raw', import: 'default', eager: true }),
)[0] as string;

function rule(selector: string): string {
  const css = (/<style[^>]*>([\s\S]*?)<\/style>/.exec(page)?.[1] ?? '').replace(/\/\*[\s\S]*?\*\//g, '');
  const re = new RegExp(`(?:^|\\})\\s*\\${selector}\\s*\\{([^}]*)\\}`);
  return re.exec(css)?.[1] ?? '';
}

describe('the player overlays sit on the 1920x1080 grid', () => {
  it('finds the page and its rules', () => {
    expect(page).toBeTruthy();
    for (const s of ['.player', '.up-next', '.buffering']) expect(rule(s)).toContain('position: fixed');
  });

  it('sizes the player to the grid, not the viewport', () => {
    const body = rule('.player');
    expect(body).toMatch(/width:\s*1920px/);
    expect(body).toMatch(/height:\s*1080px/);
    expect(body).not.toMatch(/(right|bottom):\s*0/);
  });

  it("puts Up Next and the spinner by the grid's edge and centre", () => {
    expect(rule('.up-next')).not.toMatch(/right:/);
    expect(rule('.up-next')).toMatch(/left:\s*1860px/);
    expect(rule('.buffering')).not.toMatch(/50%;/);
    expect(rule('.buffering')).toMatch(/top:\s*540px/);
    expect(rule('.buffering')).toMatch(/left:\s*960px/);
  });

  it('sizes "Starting playback…" to the grid', () => {
    expect(loadingLayer).toContain("'width: 1920px'");
    expect(loadingLayer).toContain("'height: 1080px'");
    expect(loadingLayer).not.toMatch(/'(right|bottom): 0'/);
  });
});
