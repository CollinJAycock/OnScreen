import { albumTrackOrder, discGroups, discOf } from './albumDiscs';

const t = (id: string, index?: number, disc_number?: number) => ({ id, index, disc_number });

describe('albumTrackOrder', () => {
  it('sorts by disc, then track, with no disc tag counting as disc 1', () => {
    const order = albumTrackOrder([t('2-1', 1, 2), t('1-2', 2), t('1-1', 1, 1), t('2-2', 2, 2)]);
    expect(order.map((x) => x.id)).toEqual(['1-1', '1-2', '2-1', '2-2']);
  });

  it('puts unnumbered tracks last on their disc and leaves the input alone', () => {
    const input = [t('none', undefined, 1), t('2-1', 1, 2), t('1-1', 1, 1)];
    expect(albumTrackOrder(input).map((x) => x.id)).toEqual(['1-1', 'none', '2-1']);
    expect(input[0].id).toBe('none');
  });
});

describe('discGroups', () => {
  it('keeps a single-disc album as one group', () => {
    const groups = discGroups([t('a', 1), t('b', 2)]);
    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({ disc: 1, start: 0 });
  });

  it('starts a group at each disc, remembering where it starts in the album', () => {
    const groups = discGroups(albumTrackOrder([t('2-1', 1, 2), t('1-1', 1), t('1-2', 2), t('3-1', 1, 3)]));
    expect(groups.map((g) => [g.disc, g.start, g.tracks.map((x) => x.id)])).toEqual([
      [1, 0, ['1-1', '1-2']],
      [2, 2, ['2-1']],
      [3, 3, ['3-1']],
    ]);
  });

  it('is empty for no tracks', () => {
    expect(discGroups([])).toEqual([]);
  });
});

describe('discOf', () => {
  it('defaults to disc 1', () => {
    expect(discOf({})).toBe(1);
    expect(discOf({ disc_number: 4 })).toBe(4);
  });
});
