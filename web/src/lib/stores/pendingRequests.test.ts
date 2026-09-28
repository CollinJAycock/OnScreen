import { get } from 'svelte/store';

const mockPendingCount = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({
  requestsAdminApi: { pendingCount: mockPendingCount },
}));

import {
  pendingRequestCount,
  refreshPendingRequests,
  resetPendingRequests,
  changesPendingQueue,
  pendingBadgeText,
} from './pendingRequests';

beforeEach(() => {
  vi.clearAllMocks();
  resetPendingRequests();
});

describe('pendingRequests store', () => {
  it('loads the count from GET /requests/pending-count', async () => {
    mockPendingCount.mockResolvedValue({ count: 4 });
    await refreshPendingRequests();
    expect(get(pendingRequestCount)).toBe(4);
  });

  it('throttles navigation refreshes but not forced ones', async () => {
    mockPendingCount.mockResolvedValue({ count: 1 });
    await refreshPendingRequests();
    await refreshPendingRequests();
    expect(mockPendingCount).toHaveBeenCalledTimes(1);

    mockPendingCount.mockResolvedValue({ count: 2 });
    await refreshPendingRequests(true);
    expect(mockPendingCount).toHaveBeenCalledTimes(2);
    expect(get(pendingRequestCount)).toBe(2);
  });

  it('keeps the last value when the call fails', async () => {
    mockPendingCount.mockResolvedValue({ count: 3 });
    await refreshPendingRequests(true);
    mockPendingCount.mockRejectedValue(new Error('403'));
    await refreshPendingRequests(true);
    expect(get(pendingRequestCount)).toBe(3);
  });

  it('drops a response that lands after a reset (logout / user switch)', async () => {
    let resolve!: (v: { count: number }) => void;
    mockPendingCount.mockReturnValue(new Promise((r) => (resolve = r)));
    const p = refreshPendingRequests(true);
    resetPendingRequests();
    resolve({ count: 9 });
    await p;
    expect(get(pendingRequestCount)).toBe(0);
  });

  it('treats only request_pending as a queue change', () => {
    expect(changesPendingQueue('request_pending')).toBe(true);
    expect(changesPendingQueue('request_created')).toBe(false);
    expect(changesPendingQueue('scan_complete')).toBe(false);
    expect(changesPendingQueue(undefined)).toBe(false);
  });

  it('formats the badge', () => {
    expect(pendingBadgeText(0)).toBe('');
    expect(pendingBadgeText(7)).toBe('7');
    expect(pendingBadgeText(99)).toBe('99');
    expect(pendingBadgeText(120)).toBe('99+');
  });
});
