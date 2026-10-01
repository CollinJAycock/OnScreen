import { get } from 'svelte/store';
import { loadRequestQuota, requestQuota, resetRequestQuota } from './requestAccess';

const mockQuota = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({ requestsApi: { quota: mockQuota } }));

const allowance = { can_request: false, window_days: 7, movies: { limit: 0, used: 0, remaining: null }, tv: { limit: 0, used: 0, remaining: null } };

beforeEach(() => {
  vi.clearAllMocks();
  resetRequestQuota();
});

describe('requestAccess', () => {
  it('loads once and shares concurrent calls', async () => {
    mockQuota.mockResolvedValue(allowance);
    expect(get(requestQuota)).toBeUndefined();
    await Promise.all([loadRequestQuota(), loadRequestQuota()]);
    expect(mockQuota).toHaveBeenCalledTimes(1);
    expect(get(requestQuota)).toEqual(allowance);
    await loadRequestQuota();
    expect(mockQuota).toHaveBeenCalledTimes(1);
  });

  it('marks a failed load null and retries on the next call', async () => {
    mockQuota.mockRejectedValueOnce(new Error('503')).mockResolvedValue(allowance);
    await loadRequestQuota();
    expect(get(requestQuota)).toBeNull();
    await loadRequestQuota();
    expect(get(requestQuota)).toEqual(allowance);
    expect(mockQuota).toHaveBeenCalledTimes(2);
  });

  it("drops a load that was still running at sign-out, so the next account doesn't inherit it", async () => {
    let resolve!: (v: unknown) => void;
    mockQuota.mockReturnValueOnce(new Promise((r) => { resolve = r; }));
    const pending = loadRequestQuota();
    resetRequestQuota();
    resolve({ ...allowance, can_request: true });
    await pending;
    expect(get(requestQuota)).toBeUndefined();
  });
});
