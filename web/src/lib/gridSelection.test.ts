import { describe, it, expect } from 'vitest';
import { selectRange, allSelected, toggleAll, runLimited } from './gridSelection';

const ids = ['a', 'b', 'c', 'd', 'e'];

describe('selectRange', () => {
  it('selects from the anchor through the target, either direction', () => {
    expect([...selectRange(new Set(['a']), ids, 'b', 'd')].sort()).toEqual(['a', 'b', 'c', 'd']);
    expect([...selectRange(new Set(), ids, 'e', 'c')].sort()).toEqual(['c', 'd', 'e']);
  });
  it('toggles the target alone without a usable anchor', () => {
    expect([...selectRange(new Set(), ids, null, 'c')]).toEqual(['c']);
    expect([...selectRange(new Set(['c']), ids, 'gone', 'c')]).toEqual([]);
  });
  it('does not mutate the input', () => {
    const sel = new Set(['a']);
    selectRange(sel, ids, 'a', 'c');
    expect([...sel]).toEqual(['a']);
  });
});

describe('toggleAll / allSelected', () => {
  it('selects the page, then clears it', () => {
    const other = new Set(['z']);
    const all = toggleAll(other, ids);
    expect(allSelected(all, ids)).toBe(true);
    expect([...toggleAll(all, ids)]).toEqual(['z']);
  });
  it('an empty page is never all selected', () => {
    expect(allSelected(new Set(), [])).toBe(false);
  });
});

describe('runLimited', () => {
  it('bounds concurrency and collects failures', async () => {
    let inFlight = 0;
    let peak = 0;
    const failed = await runLimited([1, 2, 3, 4, 5, 6], 2, async (n) => {
      inFlight++;
      peak = Math.max(peak, inFlight);
      await new Promise((r) => setTimeout(r, 1));
      inFlight--;
      if (n % 3 === 0) throw new Error('x');
    });
    expect(peak).toBe(2);
    expect(failed.sort()).toEqual([3, 6]);
  });
});
