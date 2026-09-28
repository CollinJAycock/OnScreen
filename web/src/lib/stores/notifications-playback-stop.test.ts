import { get } from 'svelte/store';

vi.mock('$lib/api', () => ({
  notificationApi: { list: vi.fn().mockResolvedValue([]) },
  assetUrl: (p: string) => p,
}));

class FakeEventSource {
  static last: FakeEventSource | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) { FakeEventSource.last = this; }
  close() {}
}

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('notifications store — playback.stop', () => {
  it('routes the admin stop to playbackStops, never the bell list', async () => {
    const { initNotifications, stopNotifications, notifications, playbackStops } = await import('./notifications');
    initNotifications();
    const es = FakeEventSource.last!;
    es.onmessage!({ data: JSON.stringify({
      id: '', type: 'playback.stop', item_id: 'i1', read: false, created_at: 1,
      data: { item_id: 'i1', session_id: 's1', message: 'Maintenance' },
    }) });
    expect(get(playbackStops)).toEqual({
      itemId: 'i1', sessionId: 's1', clientName: undefined, decision: undefined, message: 'Maintenance',
    });
    // A malformed stop is still swallowed (not a notification).
    es.onmessage!({ data: JSON.stringify({ type: 'playback.stop', data: { nope: true } }) });
    expect(get(notifications)).toEqual([]);
    stopNotifications();
  });
});
