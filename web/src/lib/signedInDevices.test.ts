import { describe, expect, it } from 'vitest';
import { deviceName, relativeTime, sortSessions } from './signedInDevices';
import type { SignedInSession } from './api';

function s(over: Partial<SignedInSession>): SignedInSession {
  return {
    id: over.id ?? 'x',
    client_name: '',
    created_at: '2026-10-01T00:00:00Z',
    last_seen_at: '2026-10-01T00:00:00Z',
    current: false,
    ...over,
  };
}

describe('signed-in devices helpers', () => {
  it('names unlabelled sign-ins', () => {
    expect(deviceName(s({ client_name: 'Roku' }))).toBe('Roku');
    expect(deviceName(s({ client_name: '  ' }))).toBe('Unknown device');
  });

  it('renders relative times', () => {
    const now = Date.parse('2026-10-14T12:00:00Z');
    expect(relativeTime('2026-10-14T11:59:30Z', now)).toBe('just now');
    expect(relativeTime('2026-10-14T11:55:00Z', now)).toBe('5 minutes ago');
    expect(relativeTime('2026-10-14T11:00:00Z', now)).toBe('1 hour ago');
    expect(relativeTime('2026-10-12T12:00:00Z', now)).toBe('2 days ago');
    expect(relativeTime('not a date', now)).toBe('');
  });

  it('puts the current device first, then most recent', () => {
    const list = [
      s({ id: 'old', last_seen_at: '2026-10-01T00:00:00Z' }),
      s({ id: 'new', last_seen_at: '2026-10-10T00:00:00Z' }),
      s({ id: 'me', last_seen_at: '2026-09-01T00:00:00Z', current: true }),
    ];
    expect(sortSessions(list).map((x) => x.id)).toEqual(['me', 'new', 'old']);
    expect(list[0].id).toBe('old'); // input untouched
  });
});
