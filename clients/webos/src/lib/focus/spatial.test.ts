import { describe, expect, it } from 'vitest';
import {
  CANDIDATE_LIMIT,
  nearbyCandidates,
  pickNeighbor,
  pickNeighborNear,
  verticalExit,
} from './spatial';

// Stand-ins for focusable elements: only getBoundingClientRect is read, and
// each read is counted so the tests can see how many cards a press measured.
interface Box {
  name: string;
  getBoundingClientRect(): { top: number; left: number; right: number; bottom: number; width: number; height: number };
}
let reads = 0;
function box(name: string, left: number, top: number, width = 100, height = 150): Box {
  return {
    name,
    getBoundingClientRect() {
      reads++;
      return { top, left, right: left + width, bottom: top + height, width, height };
    },
  };
}
const asEls = (b: Box[]) => b as unknown as Element[];
const nameOf = (e: Element | null) => (e as unknown as Box | null)?.name ?? null;

/** A top bar of `chips` buttons, then a `columns`-wide card grid of `count`
 *  cards, in document order (the library page's shape). */
function libraryPage(count: number, columns = 6, chips = 3): Box[] {
  const out: Box[] = [];
  for (let c = 0; c < chips; c++) out.push(box(`chip${c}`, c * 200, 0, 180, 60));
  for (let i = 0; i < count; i++) {
    const row = Math.floor(i / columns);
    const col = i % columns;
    out.push(box(`card${i}`, col * 120, 100 + row * 200));
  }
  return out;
}

/** The hub as the TV measured it: a 240 × 464 card every 264 px from x 80,
 *  rows 630 px apart (the heading, the scroller's padding and the row gap
 *  leave 166 px between one row's cards and the next's). `rows` gives each
 *  row's name and card count, top to bottom. */
function hub(rows: [string, number][]): Box[] {
  const out: Box[] = [];
  rows.forEach(([name, count], r) => {
    for (let c = 0; c < count; c++) out.push(box(`${name}${c}`, 80 + c * 264, 200 + r * 630, 240, 464));
  });
  return out;
}

/** The Avatar show page from the TV test: the hero's buttons wrapping onto
 *  a second line, the season chips, then a row of episode cards at `epX`. */
function showPage(epX: number[]): Box[] {
  return [
    box('resume', 448, 137, 278, 76),
    box('markAll', 750, 137, 302, 76),
    box('unmarkAll', 1076, 137, 330, 76),
    box('favorite', 1431, 137, 231, 76),
    box('report', 448, 238, 298, 76),
    box('season1', 80, 433, 254, 60),
    box('season2', 350, 433, 250, 60),
    ...epX.map((x, i) => box(`ep${i + 1}`, x, 616, 240, 462)),
  ];
}

describe('pickNeighbor', () => {
  it("doesn't skip a short row on the left from the right of the row above or below", () => {
    const page = asEls(hub([['movies', 8], ['shows', 2], ['cartoons', 7]]));
    const at = (name: string) => page.find((e) => nameOf(e) === name)!;
    // Down from a right-hand card: the short row's nearer card, not the
    // card straight below it a whole row further on.
    expect(nameOf(pickNeighbor(at('movies6'), page, 'down'))).toBe('shows1');
    expect(nameOf(pickNeighbor(at('cartoons6'), page, 'up'))).toBe('shows1');
    // In line with it, as before.
    expect(nameOf(pickNeighbor(at('movies0'), page, 'down'))).toBe('shows0');
    expect(nameOf(pickNeighbor(at('shows1'), page, 'down'))).toBe('cartoons1');
    expect(nameOf(pickNeighbor(at('shows0'), page, 'up'))).toBe('movies0');
  });

  it('moves along a hub row and keeps the column between full rows', () => {
    const page = asEls(hub([['a', 8], ['b', 8]]));
    const at = (name: string) => page.find((e) => nameOf(e) === name)!;
    expect(nameOf(pickNeighbor(at('a3'), page, 'right'))).toBe('a4');
    expect(nameOf(pickNeighbor(at('a3'), page, 'left'))).toBe('a2');
    expect(nameOf(pickNeighbor(at('a3'), page, 'down'))).toBe('b3');
    expect(nameOf(pickNeighbor(at('b7'), page, 'up'))).toBe('a7');
    expect(pickNeighbor(at('a7'), page, 'right')).toBeNull();
  });

  it('goes up from an episode to the season chips, not over them to a hero button', () => {
    // The old four-column grid (cards at 80 / 526 / 972 / 1418) …
    const old = asEls(showPage([80, 526, 972, 1418]));
    expect(nameOf(pickNeighbor(old[7 + 2], old, 'up'))).toBe('season2');
    expect(nameOf(pickNeighbor(old[7 + 3], old, 'up'))).toBe('season2');
    // … and the packed one.
    const packed = asEls(showPage([80, 344, 608, 872]));
    expect(nameOf(pickNeighbor(packed[7 + 0], packed, 'up'))).toBe('season1');
    expect(nameOf(pickNeighbor(packed[7 + 2], packed, 'up'))).toBe('season2');
    expect(nameOf(pickNeighbor(packed[7 + 3], packed, 'up'))).toBe('season2');
  });

  it('walks the show page from the hero down to the episodes', () => {
    const page = asEls(showPage([80, 344, 608, 872]));
    const at = (name: string) => page.find((e) => nameOf(e) === name)!;
    expect(nameOf(pickNeighbor(at('resume'), page, 'down'))).toBe('report');
    expect(nameOf(pickNeighbor(at('report'), page, 'down'))).toBe('season2');
    expect(nameOf(pickNeighbor(at('season2'), page, 'up'))).toBe('report');
    expect(nameOf(pickNeighbor(at('season2'), page, 'down'))).toBe('ep2');
    expect(nameOf(pickNeighbor(at('season1'), page, 'right'))).toBe('season2');
    expect(nameOf(pickNeighbor(at('resume'), page, 'right'))).toBe('markAll');
  });

  it('keeps Left and Right in the row even when its next element is far off', () => {
    // A wide gap to the next button in the row, and a card just below and
    // a little to the right: the row's own button wins (in the beam).
    const els = asEls([box('src', 0, 0, 200, 80), box('far', 700, 0, 200, 80), box('below', 210, 100, 200, 300)]);
    expect(nameOf(pickNeighbor(els[0], els, 'right'))).toBe('far');
  });

  it('moves a grid as before: same column, then the end of a short last row', () => {
    const els = asEls(libraryPage(14, 6, 0)); // rows of 6, 6, then 2
    expect(nameOf(pickNeighbor(els[3], els, 'down'))).toBe('card9');
    expect(nameOf(pickNeighbor(els[9], els, 'up'))).toBe('card3');
    expect(nameOf(pickNeighbor(els[9], els, 'right'))).toBe('card10');
    // Down from the right of the second row: the last row's nearer card.
    expect(nameOf(pickNeighbor(els[11], els, 'down'))).toBe('card13');
    expect(nameOf(pickNeighbor(els[6], els, 'down'))).toBe('card12');
    expect(pickNeighbor(els[12], els, 'down')).toBeNull();
  });

  it('reaches the top nav only by Up, and its Left / Right only by its own row', () => {
    // The hub: the nav's pills sit right of centre, above the first row.
    // Left from "Home" finds the first card (its right edge is left of the
    // pill) unless the move stays in the nav, which the focus manager does
    // for a [data-focus-row].
    const nav = [box('home', 560, 32, 120, 60), box('libraries', 692, 32, 160, 60)];
    const cards = hub([['row', 6]]);
    const all = asEls([...nav, ...cards]);
    expect(nameOf(pickNeighbor(all[0], all, 'left'))).toBe('row0');
    expect(pickNeighbor(all[0], asEls(nav), 'left')).toBeNull();
    expect(nameOf(pickNeighbor(all[0], asEls(nav), 'right'))).toBe('libraries');
    expect(nameOf(pickNeighbor(all[2], all, 'up'))).toBe('home');
  });

  it("stops Right at a hub row's end and the last season chip only when the move stays in the row", () => {
    // The TV: Right on the last card of the hub's last row went to the card
    // past the end of a longer row two rows up, and Right from the last
    // season chip dropped into the episodes. HubRow's scroller and the
    // season chips are [data-focus-row]s, so the focus manager offers
    // Left / Right only the row's own elements.
    const page = asEls(hub([['movies', 8], ['shows', 2], ['cartoons', 7]]));
    const at = (name: string) => page.find((e) => nameOf(e) === name)!;
    const row = (name: string) => page.filter((e) => nameOf(e)!.startsWith(name));
    expect(nameOf(pickNeighbor(at('cartoons6'), page, 'right'))).toBe('movies7');
    expect(pickNeighbor(at('cartoons6'), row('cartoons'), 'right')).toBeNull();
    expect(nameOf(pickNeighbor(at('cartoons5'), row('cartoons'), 'right'))).toBe('cartoons6');
    expect(pickNeighbor(at('cartoons0'), row('cartoons'), 'left')).toBeNull();
    // Up / Down aren't confined: the short row is still reached from the
    // right-hand end of the rows around it.
    expect(nameOf(pickNeighbor(at('cartoons6'), page, 'up'))).toBe('shows1');
    expect(nameOf(pickNeighbor(at('movies6'), page, 'down'))).toBe('shows1');

    const show = asEls(showPage([80, 344, 608, 872]));
    const chips = show.filter((e) => nameOf(e)!.startsWith('season'));
    const season2 = chips[1];
    expect(nameOf(pickNeighbor(season2, show, 'right'))).toBe('ep3');
    expect(pickNeighbor(season2, chips, 'right')).toBeNull();
    expect(nameOf(pickNeighbor(season2, show, 'down'))).toBe('ep2');
  });
});

describe('nearbyCandidates', () => {
  const list = Array.from({ length: 1000 }, (_, i) => i);

  it('keeps every candidate up to the limit', () => {
    const small = list.slice(0, CANDIDATE_LIMIT);
    expect(nearbyCandidates(small, 10)).toBe(small);
  });

  it('windows a long list around the current element', () => {
    expect(nearbyCandidates(list, 500, 300, 150)).toEqual(list.slice(350, 651));
  });

  it('clamps the window at both ends', () => {
    expect(nearbyCandidates(list, 3, 300, 150)).toEqual(list.slice(0, 154));
    expect(nearbyCandidates(list, 998, 300, 150)).toEqual(list.slice(848));
  });

  it('falls back to everything when the current element is not in the list', () => {
    expect(nearbyCandidates(list, -1, 300, 150)).toBe(list);
  });
});

describe('pickNeighborNear', () => {
  it('matches the full search inside a long grid and measures only the window', () => {
    const page = asEls(libraryPage(3000));
    const from = page[3 + 1500]; // card1500: row 250, column 0
    for (const dir of ['up', 'down', 'right'] as const) {
      reads = 0;
      const near = pickNeighborNear(from, page, dir);
      const measured = reads;
      expect(nameOf(near)).toBe(nameOf(pickNeighbor(from, page, dir)));
      // The window (301) plus the source rect, not 3003 cards.
      expect(measured).toBeLessThanOrEqual(2 * 150 + 2);
    }
    expect(nameOf(pickNeighborNear(from, page, 'up'))).toBe('card1494');
    expect(nameOf(pickNeighborNear(from, page, 'down'))).toBe('card1506');
    expect(nameOf(pickNeighborNear(from, page, 'right'))).toBe('card1501');
  });

  it('reaches the filter chips from the first row of a long grid', () => {
    const page = asEls(libraryPage(3000));
    expect(nameOf(pickNeighborNear(page[3 + 1], page, 'up'))).toBe('chip0');
  });

  it('falls back to the full search when nothing that way is in the window', () => {
    // A control far away in document order but drawn right below the last
    // row (a bar declared at the top of the page and positioned at the
    // bottom, say).
    const page = libraryPage(1000, 6, 0);
    page.unshift(box('footer', 0, 100 + 167 * 200, 600, 80));
    const els = asEls(page);
    const last = els[1 + 996]; // card996: the last (partial) row, column 0
    // Nothing below it inside the window, so the full search answers.
    expect(nameOf(pickNeighborNear(last, els, 'down'))).toBe('footer');
    // Same answer the unwindowed search gives.
    expect(nameOf(pickNeighbor(last, els, 'down'))).toBe('footer');
  });

  it('returns null at an edge with nothing beyond it', () => {
    const els = asEls(libraryPage(1000, 6, 0));
    expect(pickNeighborNear(els[5], els, 'right')).toBeNull();
  });

  it('leaves small screens on the full search', () => {
    const els = asEls(libraryPage(60));
    reads = 0;
    pickNeighborNear(els[30], els, 'down');
    // The source once, then every other candidate.
    expect(reads).toBe(els.length);
  });
});

describe('verticalExit', () => {
  /** The search page's shape: filter chips across the top (and a top-nav
   *  link far right), the on-screen keyboard (four rows of ten keys and a
   *  row of four controls), then a six-wide row of results below it. */
  function searchPage(withResults = true) {
    const chips = [box('chip0', 0, 0, 180, 60), box('chip1', 200, 0, 180, 60), box('nav', 1500, 0, 180, 60)];
    const keys: Box[] = [];
    for (let r = 0; r < 4; r++) {
      for (let c = 0; c < 10; c++) keys.push(box(`key${r}.${c}`, c * 80, 100 + r * 80, 72, 72));
    }
    keys.push(
      box('shift', 0, 420, 110, 72),
      box('symbols', 118, 420, 110, 72),
      box('space', 236, 420, 240, 72),
      box('backspace', 484, 420, 110, 72),
    );
    const results: Box[] = [];
    if (withResults) for (let c = 0; c < 6; c++) results.push(box(`card${c}`, c * 300, 560, 280, 400));
    const key = (name: string) => asEls(keys.filter((k) => k.name === name))[0];
    return { inside: asEls(keys), outside: asEls([...chips, ...results]), key };
  }

  it('keeps Left and Right in the keyboard at the end of a row', () => {
    const { inside, outside, key } = searchPage();
    // Without the scope, Right from the top row's last key would leave it.
    expect(pickNeighbor(key('key0.9'), [...inside, ...outside], 'right')).not.toBeNull();
    expect(verticalExit(key('key0.9'), inside, outside, 'right')).toBeNull();
    expect(verticalExit(key('key3.9'), inside, outside, 'right')).toBeNull();
    expect(verticalExit(key('key1.0'), inside, outside, 'left')).toBeNull();
  });

  it('leaves Up and Down to the keyboard while it has a row that way', () => {
    const { inside, outside, key } = searchPage();
    expect(verticalExit(key('key1.4'), inside, outside, 'down')).toBeNull();
    expect(verticalExit(key('key1.4'), inside, outside, 'up')).toBeNull();
    // The last letter row's right end: the controls row is still below it.
    expect(verticalExit(key('key3.9'), inside, outside, 'down')).toBeNull();
  });

  it('goes down from the bottom row to the result below', () => {
    const { inside, outside, key } = searchPage();
    expect(nameOf(verticalExit(key('space'), inside, outside, 'down'))).toBe('card1');
    expect(nameOf(verticalExit(key('shift'), inside, outside, 'down'))).toBe('card0');
  });

  it('goes up from the top row to the chip above', () => {
    const { inside, outside, key } = searchPage();
    expect(nameOf(verticalExit(key('key0.1'), inside, outside, 'up'))).toBe('chip0');
    expect(nameOf(verticalExit(key('key0.3'), inside, outside, 'up'))).toBe('chip1');
  });

  it('stays put at the bottom with nothing below it', () => {
    const { inside, outside, key } = searchPage(false);
    expect(verticalExit(key('space'), inside, outside, 'down')).toBeNull();
  });
});
