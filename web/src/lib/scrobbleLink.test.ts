import { pollLink, SLOW_DOWN_STEP_MS } from './scrobbleLink';
import type { ScrobbleLinkResult } from '$lib/api';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

function sequence(...results: (ScrobbleLinkResult | Error)[]) {
  const fn = vi.fn();
  for (const r of results) {
    if (r instanceof Error) fn.mockRejectedValueOnce(r);
    else fn.mockResolvedValueOnce(r);
  }
  return fn;
}

describe('pollLink', () => {
  it('waits an interval, polls while pending, and reports the link', async () => {
    const complete = sequence({ status: 'pending' }, { status: 'pending' }, { status: 'linked', username: 'rj' });
    const onDone = vi.fn();
    pollLink({ complete, intervalMs: 3000, timeoutMs: 60_000, onDone, onError: vi.fn() });

    expect(complete).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(3000);
    expect(complete).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(6000);
    expect(complete).toHaveBeenCalledTimes(3);
    expect(onDone).toHaveBeenCalledWith('linked', 'rj');

    await vi.advanceTimersByTimeAsync(30_000);
    expect(complete).toHaveBeenCalledTimes(3); // settled: no more calls
  });

  it('backs off on slow_down', async () => {
    const complete = sequence({ status: 'slow_down' }, { status: 'denied' });
    const onDone = vi.fn();
    pollLink({ complete, intervalMs: 5000, timeoutMs: 60_000, onDone, onError: vi.fn() });

    await vi.advanceTimersByTimeAsync(5000);
    await vi.advanceTimersByTimeAsync(5000);
    expect(complete).toHaveBeenCalledTimes(1); // the next poll is now 10s out
    await vi.advanceTimersByTimeAsync(SLOW_DOWN_STEP_MS);
    expect(complete).toHaveBeenCalledTimes(2);
    expect(onDone).toHaveBeenCalledWith('denied', undefined);
  });

  it('gives up as expired at the deadline', async () => {
    const complete = vi.fn().mockResolvedValue({ status: 'pending' });
    const onDone = vi.fn();
    pollLink({ complete, intervalMs: 1000, timeoutMs: 3500, onDone, onError: vi.fn() });

    await vi.advanceTimersByTimeAsync(5000);
    expect(onDone).toHaveBeenCalledWith('expired');
    expect(complete).toHaveBeenCalledTimes(3);
  });

  it('stops on an error', async () => {
    const complete = sequence({ status: 'pending' }, new Error('offline'));
    const onError = vi.fn();
    pollLink({ complete, intervalMs: 1000, timeoutMs: 60_000, onDone: vi.fn(), onError });

    await vi.advanceTimersByTimeAsync(5000);
    expect(onError).toHaveBeenCalledWith(new Error('offline'));
    expect(complete).toHaveBeenCalledTimes(2);
  });

  it('fires nothing after cancel', async () => {
    const complete = vi.fn().mockResolvedValue({ status: 'linked' });
    const onDone = vi.fn();
    const cancel = pollLink({ complete, intervalMs: 1000, timeoutMs: 60_000, onDone, onError: vi.fn() });

    cancel();
    await vi.advanceTimersByTimeAsync(5000);
    expect(complete).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
  });
});
