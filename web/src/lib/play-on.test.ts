import { describe, it, expect } from 'vitest';
import { formatLastSeen, otherDevices, transferPositionMs } from './play-on';

describe('otherDevices', () => {
  const own = 'Web — Chrome on Windows';

  it('drops this browser by exact client name', () => {
    const out = otherDevices(
      [
        { client_name: own, last_seen: '2026-09-28T10:00:00Z' },
        { client_name: 'Living Room TV', last_seen: '2026-09-28T09:00:00Z' },
      ],
      own,
    );
    expect(out.map((d) => d.client_name)).toEqual(['Living Room TV']);
  });

  it('keeps other browsers of the same user', () => {
    const out = otherDevices([{ client_name: 'Web — Firefox on Linux', last_seen: '2026-09-28T09:00:00Z' }], own);
    expect(out).toHaveLength(1);
  });

  it('sorts most recently seen first and de-duplicates', () => {
    const out = otherDevices(
      [
        { client_name: 'Pixel 8', last_seen: '2026-09-20T09:00:00Z' },
        { client_name: 'Living Room TV', last_seen: '2026-09-28T09:00:00Z' },
        { client_name: 'Pixel 8', last_seen: '2026-09-27T09:00:00Z' },
      ],
      own,
    );
    expect(out.map((d) => d.client_name)).toEqual(['Living Room TV', 'Pixel 8']);
  });

  it('skips blank names and tolerates a null list', () => {
    expect(otherDevices([{ client_name: '  ', last_seen: '2026-09-28T09:00:00Z' }], own)).toEqual([]);
    expect(otherDevices(null, own)).toEqual([]);
    expect(otherDevices(undefined, own)).toEqual([]);
  });
});

describe('formatLastSeen', () => {
  const now = Date.parse('2026-09-28T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();

  it('buckets into human units', () => {
    expect(formatLastSeen(ago(10_000), now)).toBe('just now');
    expect(formatLastSeen(ago(5 * 60_000), now)).toBe('5 min ago');
    expect(formatLastSeen(ago(3 * 3_600_000), now)).toBe('3 hr ago');
    expect(formatLastSeen(ago(30 * 3_600_000), now)).toBe('yesterday');
    expect(formatLastSeen(ago(4 * 86_400_000), now)).toBe('4 days ago');
    expect(formatLastSeen(ago(8 * 86_400_000), now)).toBe('1 week ago');
    expect(formatLastSeen(ago(20 * 86_400_000), now)).toBe('2 weeks ago');
  });

  it('clamps clock skew to "just now" and blanks garbage', () => {
    expect(formatLastSeen(new Date(now + 60_000).toISOString(), now)).toBe('just now');
    expect(formatLastSeen('not a date', now)).toBe('');
  });
});

describe('transferPositionMs', () => {
  it('rounds to a non-negative integer', () => {
    expect(transferPositionMs(() => 1234.6)).toBe(1235);
    expect(transferPositionMs(() => -5)).toBe(0);
  });

  it('falls back to 0 for missing, NaN or throwing getters', () => {
    expect(transferPositionMs(undefined)).toBe(0);
    expect(transferPositionMs(() => Number.NaN)).toBe(0);
    expect(
      transferPositionMs(() => {
        throw new Error('no video');
      }),
    ).toBe(0);
  });
});
