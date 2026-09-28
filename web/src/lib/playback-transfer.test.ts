import { describe, it, expect } from 'vitest';
import { parseAtParam, transferAction, transferStartMs } from './playback-transfer';

const track = { id: 't1', type: 'track', title: 'Song', duration_ms: 240_000, poster_path: 'a/p.jpg', files: [{ id: 'f1' }] };

describe('transferAction', () => {
  it('plays audio at the sent position', () => {
    const a = transferAction(track, 93_500);
    expect(a).toEqual({
      kind: 'audio',
      track: {
        id: 't1', fileId: 'f1', title: 'Song', artist: undefined, album: undefined,
        durationMS: 240_000, posterPath: 'a/p.jpg',
      },
      startMS: 93_500,
    });
  });

  it.each(['audiobook', 'audiobook_chapter'])('treats %s as audio', (type) => {
    const a = transferAction({ ...track, type }, 1000);
    expect(a.kind).toBe('audio');
    if (a.kind === 'audio') expect(a.startMS).toBe(1000);
  });

  it('starts audio from 0 when the position is at or past the end', () => {
    const a = transferAction(track, 240_000);
    expect(a.kind === 'audio' && a.startMS).toBe(0);
  });

  it('tolerates an item without files / art', () => {
    const a = transferAction({ id: 't2', type: 'track', title: 'X' }, 0);
    expect(a).toMatchObject({ kind: 'audio', startMS: 0, track: { fileId: '', posterPath: undefined, durationMS: undefined } });
  });

  it('sends video to the watch page with ?at=', () => {
    expect(transferAction({ id: 'm1', type: 'movie', title: 'M' }, 61_234.4)).toEqual({ kind: 'watch', href: '/watch/m1?at=61234' });
    expect(transferAction({ id: 'e1', type: 'episode', title: 'E' }, 0)).toEqual({ kind: 'watch', href: '/watch/e1' });
  });
});

describe('transferStartMs', () => {
  it('normalises garbage to 0', () => {
    for (const v of [NaN, -5, Infinity, '100', null, undefined]) expect(transferStartMs(v)).toBe(0);
  });
  it('rounds to whole ms', () => {
    expect(transferStartMs(1500.6)).toBe(1501);
  });
});

describe('parseAtParam', () => {
  it('accepts a positive integer', () => {
    expect(parseAtParam('61234')).toBe(61234);
  });
  it.each([null, undefined, '', '0', '-5', '12.5', '1e3', 'abc', '99999999999999999999'])('rejects %s', (v) => {
    expect(parseAtParam(v)).toBeNull();
  });
});
