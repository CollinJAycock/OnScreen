const get = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({ jobsApi: { get } }));

import { startJobsPolling, stopJobsPolling } from './jobs';

let visibility: DocumentVisibilityState = 'visible';

beforeEach(() => {
  vi.useFakeTimers();
  get.mockReset();
  get.mockResolvedValue({ scans: [] });
  visibility = 'visible';
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
});

afterEach(() => {
  stopJobsPolling();
  vi.useRealTimers();
});

function setVisibility(v: DocumentVisibilityState) {
  visibility = v;
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('jobs polling', () => {
  it('polls every 30 s while the tab is visible', async () => {
    startJobsPolling();
    await vi.advanceTimersByTimeAsync(0);
    expect(get).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(get).toHaveBeenCalledTimes(3);
  });

  it('skips its ticks while the tab is hidden and catches up when shown', async () => {
    startJobsPolling();
    await vi.advanceTimersByTimeAsync(0);
    setVisibility('hidden');
    await vi.advanceTimersByTimeAsync(5 * 60_000);
    expect(get).toHaveBeenCalledTimes(1);
    setVisibility('visible');
    await vi.advanceTimersByTimeAsync(0);
    expect(get).toHaveBeenCalledTimes(2);
  });

  it('does nothing on a visibility change once stopped', async () => {
    startJobsPolling();
    await vi.advanceTimersByTimeAsync(0);
    setVisibility('hidden');
    await vi.advanceTimersByTimeAsync(30_000);
    stopJobsPolling();
    setVisibility('visible');
    await vi.advanceTimersByTimeAsync(0);
    expect(get).toHaveBeenCalledTimes(1);
  });
});
