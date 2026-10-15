import { commercialAt, commercialId, fileCommercials } from './commercials';
import type { CommercialMarker } from './api';

const breaks: CommercialMarker[] = [
  { start_ms: 600_500, end_ms: 690_000, source: 'heuristic' },
  { start_ms: 1_200_000, end_ms: 1_290_000, source: 'heuristic' },
];

describe('commercials', () => {
  it('finds the break the playhead is inside', () => {
    expect(commercialAt(breaks, 600_500)).toBe(breaks[0]);
    expect(commercialAt(breaks, 650_000)).toBe(breaks[0]);
    expect(commercialAt(breaks, 1_250_000)).toBe(breaks[1]);
  });

  it('is null outside every break, including at a break end', () => {
    expect(commercialAt(breaks, 0)).toBeNull();
    expect(commercialAt(breaks, 690_000)).toBeNull();
    expect(commercialAt([], 650_000)).toBeNull();
  });

  it("reads the playing file's breaks and tolerates their absence", () => {
    expect(fileCommercials([{ commercials: breaks }, { commercials: [] }])).toBe(breaks);
    expect(fileCommercials([{}])).toEqual([]);
    expect(fileCommercials(undefined)).toEqual([]);
  });

  it('ids a break by its range', () => {
    expect(commercialId(breaks[0])).toBe('600500-690000');
  });
});
