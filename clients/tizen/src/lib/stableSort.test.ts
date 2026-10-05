import { describe, expect, it } from 'vitest';
import { stableSort } from './stableSort';

describe('stableSort', () => {
  it('keeps equal elements in their original order, however many (V8 on Chromium 69 did not past 10)', () => {
    const items = Array.from({ length: 40 }, (_, i) => ({ key: i % 3 === 0 ? 0 : 1, i }));
    const sorted = stableSort(items, (a, b) => a.key - b.key);
    const zeros = sorted.filter((x) => x.key === 0).map((x) => x.i);
    const ones = sorted.filter((x) => x.key === 1).map((x) => x.i);
    expect(zeros).toEqual([...zeros].sort((a, b) => a - b));
    expect(ones).toEqual([...ones].sort((a, b) => a - b));
    expect(sorted.slice(0, zeros.length).every((x) => x.key === 0)).toBe(true);
  });

  it('leaves the input alone', () => {
    const items = [3, 1, 2];
    expect(stableSort(items, (a, b) => a - b)).toEqual([1, 2, 3]);
    expect(items).toEqual([3, 1, 2]);
  });
});
