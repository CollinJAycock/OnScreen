vi.mock('$lib/api', () => ({
  notificationApi: { list: vi.fn().mockResolvedValue([]) },
  assetUrl: (p: string) => p,
}));

class FakeEventSource {
  static all: FakeEventSource[] = [];
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  onopen: (() => void) | null = null;
  constructor(public url: string) { FakeEventSource.all.push(this); }
  close() {}
}

import { initNotifications, stopNotifications, SSE_RETRY_MAX_MS, SSE_RETRY_MIN_MS } from './notifications';

beforeEach(() => {
  vi.useFakeTimers();
  FakeEventSource.all = [];
  vi.stubGlobal('EventSource', FakeEventSource);
});

afterEach(() => {
  stopNotifications();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const last = () => FakeEventSource.all[FakeEventSource.all.length - 1];

// Fails the current stream and returns how long the reconnect waited.
async function failAndTimeReconnect(): Promise<number> {
  const before = FakeEventSource.all.length;
  last().onerror!();
  let waited = 0;
  while (FakeEventSource.all.length === before) {
    await vi.advanceTimersByTimeAsync(1000);
    waited += 1000;
    if (waited > 10 * SSE_RETRY_MAX_MS) throw new Error('never reconnected');
  }
  return waited;
}

describe('notifications stream reconnect', () => {
  it('backs off while the stream keeps failing, capped at a minute', async () => {
    initNotifications();
    const waits: number[] = [];
    for (let i = 0; i < 6; i++) waits.push(await failAndTimeReconnect());
    expect(waits).toEqual([5_000, 10_000, 20_000, 40_000, SSE_RETRY_MAX_MS, SSE_RETRY_MAX_MS]);
  });

  it('starts over at 5 s once a connection opens', async () => {
    initNotifications();
    await failAndTimeReconnect();
    await failAndTimeReconnect();
    last().onopen!();
    expect(await failAndTimeReconnect()).toBe(SSE_RETRY_MIN_MS);
  });

  it('a stop cancels the pending reconnect and resets the backoff', async () => {
    initNotifications();
    await failAndTimeReconnect();
    last().onerror!();
    stopNotifications();
    const count = FakeEventSource.all.length;
    await vi.advanceTimersByTimeAsync(SSE_RETRY_MAX_MS);
    expect(FakeEventSource.all.length).toBe(count);
    initNotifications();
    expect(await failAndTimeReconnect()).toBe(SSE_RETRY_MIN_MS);
  });
});
