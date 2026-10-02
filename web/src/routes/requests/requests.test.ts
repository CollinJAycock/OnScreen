import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import type { ArrService, MediaRequest } from '$lib/api';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockGetUser = vi.hoisted(() => vi.fn());
const mockMine = vi.hoisted(() => vi.fn());
const mockQueue = vi.hoisted(() => vi.fn());
const mockApprove = vi.hoisted(() => vi.fn());
const mockDecline = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());
const mockRefreshPending = vi.hoisted(() => vi.fn());
const mockArrList = vi.hoisted(() => vi.fn());
const mockProbe = vi.hoisted(() => vi.fn());
const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  api: { getUser: mockGetUser },
  upcomingApi: { get: vi.fn().mockResolvedValue({ from: '', to: '', items: [], services: [] }) },
  requestsApi: { list: mockMine, cancel: vi.fn() },
  requestsAdminApi: { list: mockQueue, approve: mockApprove, decline: mockDecline, del: mockDelete },
  arrServicesApi: { list: mockArrList, probe: mockProbe },
}));
vi.mock('$lib/stores/pendingRequests', () => ({ refreshPendingRequests: mockRefreshPending }));
vi.mock('$lib/stores/toast', () => ({ toast: mockToast }));
const mockConfirm = vi.hoisted(() => vi.fn());
vi.mock('$lib/native', async (importOriginal) => ({
  ...(await importOriginal<typeof import('$lib/native')>()),
  confirmAction: mockConfirm,
}));
// Requests on, and the account may request (the gates themselves are
// covered in upcoming.test.ts).
vi.mock('$lib/stores/capabilities', async () => {
  const { writable } = await import('svelte/store');
  return {
    capabilities: writable({ features: { requests: true, upcoming: true } }),
    ensureCapabilities: vi.fn().mockResolvedValue(undefined),
  };
});
vi.mock('$lib/stores/requestAccess', async () => {
  const { writable } = await import('svelte/store');
  return {
    requestQuota: writable({ can_request: true, window_days: 7, movies: { limit: 0, used: 0, remaining: null }, tv: { limit: 0, used: 0, remaining: null } }),
    loadRequestQuota: vi.fn().mockResolvedValue(undefined),
  };
});

function req(over: Partial<MediaRequest> & Pick<MediaRequest, 'id' | 'title'>): MediaRequest {
  return {
    user_id: 'u1',
    type: 'movie',
    tmdb_id: 1,
    status: 'pending',
    auto_approved: false,
    created_at: '2026-09-20T12:00:00Z',
    updated_at: '2026-09-20T12:00:00Z',
    ...over,
  };
}

function svc(over: Partial<ArrService> & Pick<ArrService, 'id' | 'name'>): ArrService {
  return {
    kind: 'radarr', base_url: 'http://radarr:7878', api_key_set: true, default_tags: [],
    is_default: false, enabled: true, created_at: '', updated_at: '', ...over,
  };
}

const RADARR = svc({ id: 'r1', name: 'Radarr', is_default: true, default_quality_profile_id: 4, default_root_folder: '/movies' });
const RADARR_4K = svc({ id: 'r2', name: 'Radarr 4K', default_quality_profile_id: 7, default_root_folder: '/movies-4k' });
const SONARR = svc({ id: 's1', name: 'Sonarr', kind: 'sonarr', is_default: true, default_quality_profile_id: 2, default_root_folder: '/tv' });

const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const ahead = (ms: number) => new Date(Date.now() + ms).toISOString();
const MIN = 60_000;

const rowOf = (title: string) => screen.getByText(title).closest('.row') as HTMLElement;

beforeEach(() => {
  vi.clearAllMocks();
  mockMine.mockResolvedValue({ items: [], total: 0 });
  mockQueue.mockResolvedValue({ items: [], total: 0 });
  mockArrList.mockResolvedValue({ items: [RADARR, RADARR_4K, SONARR], total: 3 });
  mockProbe.mockImplementation(({ service_id }: { service_id: string }) =>
    Promise.resolve({
      status: 'ok',
      quality_profiles: service_id === 'r2' ? [{ id: 7, name: 'Ultra-HD' }] : [{ id: 4, name: 'HD-1080p' }, { id: 2, name: 'WEB' }],
      root_folders: service_id === 'r2' ? [{ id: 1, path: '/movies-4k' }] : [{ id: 1, path: '/movies' }, { id: 2, path: '/tv' }],
      tags: [],
      language_profiles: [],
    }),
  );
});

async function openQueue() {
  render(Page);
  await waitFor(() => expect(screen.getByRole('button', { name: 'Queue' })).toBeTruthy());
  await fireEvent.click(screen.getByRole('button', { name: 'Queue' }));
}

describe('Auto-approved badge', () => {
  it('marks auto-approved requests in My Requests, and only those', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', username: 'kid', is_admin: false });
    mockMine.mockResolvedValue({
      items: [
        req({ id: 'a', title: 'Dune', status: 'downloading', auto_approved: true }),
        req({ id: 'b', title: 'Arrival', status: 'approved', auto_approved: false }),
        req({ id: 'c', title: 'Heat', status: 'pending' }),
      ],
      total: 3,
    });
    render(Page);
    await waitFor(() => expect(screen.getByText('Dune')).toBeTruthy());

    expect(within(rowOf('Dune')).getByText('Auto-approved')).toBeTruthy();
    expect(within(rowOf('Dune')).getByText('Downloading')).toBeTruthy();
    // Approved by an admin from the queue: no badge.
    expect(within(rowOf('Arrival')).queryByText('Auto-approved')).toBeNull();
    expect(within(rowOf('Heat')).queryByText('Auto-approved')).toBeNull();
    expect(screen.getAllByText('Auto-approved')).toHaveLength(1);
  });

  it('marks auto-approved requests in the admin queue history', async () => {
    mockGetUser.mockReturnValue({ user_id: 'admin', username: 'admin', is_admin: true });
    mockQueue.mockResolvedValue({
      items: [
        req({ id: 'a', title: 'Dune', status: 'available', auto_approved: true }),
        req({ id: 'b', title: 'Heat', status: 'available' }),
      ],
      total: 2,
    });
    await openQueue();
    await waitFor(() => expect(screen.getByText('Dune')).toBeTruthy());

    expect(within(rowOf('Dune')).getByText('Auto-approved')).toBeTruthy();
    expect(within(rowOf('Heat')).queryByText('Auto-approved')).toBeNull();
  });
});

describe('Approve with options', () => {
  beforeEach(() => {
    mockGetUser.mockReturnValue({ user_id: 'admin', username: 'admin', is_admin: true });
    mockQueue.mockResolvedValue({
      items: [
        req({ id: 'p1', title: 'Heat', username: 'alice' }),
        req({ id: 'p2', title: 'Dune', username: 'bob', requested_service_id: 'r2' }),
      ],
      total: 2,
    });
    mockApprove.mockResolvedValue(req({ id: 'p1', title: 'Heat', status: 'downloading' }));
  });

  async function openDialog(title: string) {
    await openQueue();
    await waitFor(() => expect(screen.getByText(title)).toBeTruthy());
    await fireEvent.click(within(rowOf(title)).getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog', { name: /Approve/ });
    await waitFor(() => expect((within(dialog).getByRole('button', { name: 'Approve' }) as HTMLButtonElement).disabled).toBe(false));
    return dialog;
  }

  it('opens pre-filled and approves with the chosen instance, profile and folder', async () => {
    const dialog = await openDialog('Heat');
    expect(mockApprove).not.toHaveBeenCalled();
    expect((within(dialog).getByLabelText('Radarr instance') as HTMLSelectElement).value).toBe('r1');
    expect(mockProbe).toHaveBeenCalledWith({ service_id: 'r1' });

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));
    await waitFor(() =>
      expect(mockApprove).toHaveBeenCalledWith('p1', { service_id: 'r1', quality_profile_id: 4, root_folder: '/movies' }),
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(mockToast.success).toHaveBeenCalledWith('Approved: Heat');
  });

  it('pre-selects the instance the requester asked for', async () => {
    const dialog = await openDialog('Dune');
    expect((within(dialog).getByLabelText(/Radarr instance/) as HTMLSelectElement).value).toBe('r2');
    expect(within(dialog).getByText('Requested by bob', { selector: '.tag' })).toBeTruthy();
    await fireEvent.keyDown(dialog, { key: 'Enter' });
    await waitFor(() =>
      expect(mockApprove).toHaveBeenCalledWith('p2', { service_id: 'r2', quality_profile_id: 7, root_folder: '/movies-4k' }),
    );
  });

  it('Esc closes the dialog without approving', async () => {
    const dialog = await openDialog('Heat');
    await fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(mockApprove).not.toHaveBeenCalled();
  });

  it('“Approve with defaults” leaves every choice to the server', async () => {
    const dialog = await openDialog('Heat');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Approve with defaults' }));
    await waitFor(() => expect(mockApprove).toHaveBeenCalledWith('p1', {}));
  });

  it('keeps the dialog open when the approval fails', async () => {
    mockApprove.mockRejectedValue(new Error('arr add failed: missing root folder'));
    const dialog = await openDialog('Heat');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));
    await waitFor(() => expect(mockToast.error).toHaveBeenCalledWith('arr add failed: missing root folder'));
    expect(screen.getByRole('dialog', { name: /Approve/ })).toBeTruthy();
    expect(mockRefreshPending).not.toHaveBeenCalled();
  });

  it('loads the instance list once for the page', async () => {
    await openDialog('Heat');
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await fireEvent.click(within(rowOf('Heat')).getByRole('button', { name: 'Approve' }));
    await screen.findByRole('dialog', { name: /Approve/ });
    expect(mockArrList).toHaveBeenCalledTimes(1);
    expect(mockProbe).toHaveBeenCalledTimes(1);
  });
});

describe('Admin nav badge', () => {
  beforeEach(() => {
    mockGetUser.mockReturnValue({ user_id: 'admin', username: 'admin', is_admin: true });
    mockQueue.mockResolvedValue({ items: [req({ id: 'p1', title: 'Heat' }), req({ id: 'p2', title: 'Dune' })], total: 2 });
    mockApprove.mockResolvedValue(req({ id: 'p1', title: 'Heat', status: 'downloading' }));
    mockDecline.mockResolvedValue(req({ id: 'p2', title: 'Dune', status: 'declined' }));
    mockDelete.mockResolvedValue(undefined);
  });

  async function openQueueRows() {
    await openQueue();
    await waitFor(() => expect(screen.getByText('Heat')).toBeTruthy());
  }

  it('refreshes the pending count after an approval', async () => {
    await openQueueRows();
    await fireEvent.click(within(rowOf('Heat')).getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog', { name: /Approve/ });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Approve with defaults' }));
    await waitFor(() => expect(mockApprove).toHaveBeenCalledWith('p1', {}));
    await waitFor(() => expect(mockRefreshPending).toHaveBeenCalledWith(true));
  });

  it('refreshes the pending count after a decline', async () => {
    await openQueueRows();
    await fireEvent.click(within(rowOf('Dune')).getByRole('button', { name: 'Decline' }));
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Decline' }));
    await waitFor(() => expect(mockDecline).toHaveBeenCalledWith('p2', ''));
    await waitFor(() => expect(mockRefreshPending).toHaveBeenCalledWith(true));
  });

  it('refreshes the pending count after a delete', async () => {
    mockConfirm.mockResolvedValue(true);
    await openQueueRows();
    await fireEvent.click(within(rowOf('Dune')).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith('p2'));
    await waitFor(() => expect(mockRefreshPending).toHaveBeenCalledWith(true));
  });

  it('does not refresh when the approval fails', async () => {
    mockApprove.mockRejectedValue(new Error('arr down'));
    await openQueueRows();
    await fireEvent.click(within(rowOf('Heat')).getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog', { name: /Approve/ });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Approve with defaults' }));
    await waitFor(() => expect(mockApprove).toHaveBeenCalled());
    expect(mockRefreshPending).not.toHaveBeenCalled();
  });
});

describe('Requester-aware queue rows', () => {
  beforeEach(() => {
    mockGetUser.mockReturnValue({ user_id: 'admin', username: 'admin', is_admin: true });
  });

  it('shows who asked, when, which seasons and the preferred instance', async () => {
    mockQueue.mockResolvedValue({
      items: [
        req({ id: 'a', title: 'Severance', type: 'show', username: 'alice', seasons: [1, 2, 3, 5], created_at: ago(3 * 60 * MIN), requested_service_id: 's1' }),
        req({ id: 'b', title: 'Andor', type: 'show', username: 'bob', created_at: ago(5 * MIN) }),
        req({ id: 'c', title: 'Heat', username: 'carol', requested_service_id: 'r2', created_at: ago(30 * 1000) }),
        req({ id: 'd', title: 'Arrival', requested_service_id: 'gone' }), // older server: no username
      ],
      total: 4,
    });
    await openQueue();
    await waitFor(() => expect(screen.getByText('Severance')).toBeTruthy());

    const sev = rowOf('Severance');
    expect(sev.textContent).toContain('Requested by alice');
    expect(sev.textContent).toContain('3 hr ago');
    expect(sev.textContent).toContain('Seasons 1–3, 5');
    await waitFor(() => expect(within(sev).getByText('Prefers Sonarr')).toBeTruthy());

    expect(rowOf('Andor').textContent).toContain('Requested by bob');
    expect(rowOf('Andor').textContent).toContain('All seasons');
    expect(rowOf('Andor').textContent).toContain('5 min ago');

    const heat = rowOf('Heat');
    expect(heat.textContent).toContain('Requested by carol');
    expect(heat.textContent).toContain('just now');
    expect(heat.textContent).not.toMatch(/seasons?/i);
    expect(within(heat).getByText('Prefers Radarr 4K')).toBeTruthy();

    const arrival = rowOf('Arrival');
    expect(arrival.textContent).toMatch(/Requested \S/);
    expect(arrival.textContent).not.toContain('Requested by');
    expect(within(arrival).getByText('Prefers a specific instance')).toBeTruthy();
  });

  it('still lists requests when the arr instance list can’t be loaded', async () => {
    mockArrList.mockRejectedValue(new Error('forbidden'));
    mockQueue.mockResolvedValue({ items: [req({ id: 'c', title: 'Heat', username: 'carol', requested_service_id: 'r2' })], total: 1 });
    await openQueue();
    await waitFor(() => expect(screen.getByText('Heat')).toBeTruthy());
    expect(within(rowOf('Heat')).getByText('Prefers a specific instance')).toBeTruthy();
  });
});

describe('Live download state', () => {
  it('shows a chip with progress and ETA on the user’s own requests', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', username: 'kid', is_admin: false });
    mockMine.mockResolvedValue({
      items: [
        req({ id: 'a', title: 'Dune', status: 'downloading', download: { state: 'downloading', progress: 0.42, eta: ahead(25 * MIN + 10_000), updated_at: ago(MIN) } }),
        req({ id: 'b', title: 'Heat', status: 'downloading', download: { state: 'searching', updated_at: ago(MIN) } }),
        req({ id: 'c', title: 'Arrival', status: 'downloading', download: { state: 'stalled', progress: 0.1, message: 'No connections to seeders', updated_at: ago(MIN) } }),
        req({ id: 'd', title: 'Alien', status: 'downloading', download: { state: 'import_pending', updated_at: ago(MIN) } }),
        req({ id: 'e', title: 'Tenet', status: 'available', download: { state: 'downloading', progress: 0.9, updated_at: ago(MIN) } }),
      ],
      total: 5,
    });
    render(Page);
    await waitFor(() => expect(screen.getByText('Dune')).toBeTruthy());

    expect(within(rowOf('Dune')).getByText('Downloading 42% · ~25 min')).toBeTruthy();
    const bar = within(rowOf('Dune')).getByRole('progressbar');
    expect(bar.getAttribute('aria-valuenow')).toBe('42');
    expect(within(rowOf('Heat')).getByText('Searching')).toBeTruthy();
    expect(within(rowOf('Heat')).queryByRole('progressbar')).toBeNull();
    expect(within(rowOf('Arrival')).getByText('Stalled')).toBeTruthy();
    expect(within(rowOf('Arrival')).getByText('No connections to seeders')).toBeTruthy();
    expect(within(rowOf('Alien')).getByText('Importing')).toBeTruthy();
    // A stale download block on an available request is ignored.
    expect(within(rowOf('Tenet')).queryByRole('progressbar')).toBeNull();
    expect(within(rowOf('Tenet')).queryByText(/Downloading 90%/)).toBeNull();
  });

  it('the Failed filter shows why each request failed', async () => {
    mockGetUser.mockReturnValue({ user_id: 'admin', username: 'admin', is_admin: true });
    mockQueue.mockImplementation((params: { status?: string }) =>
      Promise.resolve(
        params.status === 'failed'
          ? {
              items: [
                req({ id: 'f1', title: 'Dune', status: 'failed', username: 'alice', download: { state: 'failed', message: 'Import failed: no video files', updated_at: ago(MIN) } }),
                req({ id: 'f2', title: 'Heat', status: 'failed', username: 'bob' }),
              ],
              total: 2,
            }
          : { items: [], total: 0 },
      ),
    );
    await openQueue();
    await waitFor(() => expect(screen.getByText('Nothing in the queue.')).toBeTruthy());

    const filter = screen.getByLabelText('Filter the queue by status') as HTMLSelectElement;
    await fireEvent.change(filter, { target: { value: 'failed' } });
    await waitFor(() => expect(mockQueue).toHaveBeenLastCalledWith({ status: 'failed' }));
    await waitFor(() => expect(screen.getByText('Dune')).toBeTruthy());

    const dune = rowOf('Dune');
    expect(within(dune).getAllByText('Failed')).toHaveLength(1); // the status pill; no duplicate chip
    expect(within(dune).getByText('Import failed: no video files')).toBeTruthy();
    expect(within(rowOf('Heat')).getByText(/No reason was reported/)).toBeTruthy();
  });
});

describe('Polling', () => {
  let visibility: DocumentVisibilityState = 'visible';

  beforeEach(() => {
    visibility = 'visible';
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    mockGetUser.mockReturnValue({ user_id: 'u1', username: 'kid', is_admin: false });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const active = () => ({
    items: [req({ id: 'a', title: 'Dune', status: 'downloading', download: { state: 'downloading', progress: 0.2, updated_at: ago(MIN) } })],
    total: 1,
  });
  const done = () => ({ items: [req({ id: 'a', title: 'Dune', status: 'available', fulfilled_item_id: 'i1' })], total: 1 });

  it('re-reads the list every 30 s while something is downloading, then stops', async () => {
    mockMine.mockResolvedValue(active());
    render(Page);
    await vi.advanceTimersByTimeAsync(0);
    expect(mockMine).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(29_000);
    expect(mockMine).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(mockMine).toHaveBeenCalledTimes(2);

    // The download finished: that poll's result disarms polling.
    mockMine.mockResolvedValue(done());
    await vi.advanceTimersByTimeAsync(30_000);
    expect(mockMine).toHaveBeenCalledTimes(3);
    expect(screen.getByRole('link', { name: 'Watch' })).toBeTruthy();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(mockMine).toHaveBeenCalledTimes(3);
  });

  it('does not poll when nothing is in flight', async () => {
    mockMine.mockResolvedValue(done());
    render(Page);
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(mockMine).toHaveBeenCalledTimes(1);
  });

  it('pauses while the tab is hidden and refreshes on return', async () => {
    mockMine.mockResolvedValue(active());
    render(Page);
    await vi.advanceTimersByTimeAsync(0);
    expect(mockMine).toHaveBeenCalledTimes(1);

    visibility = 'hidden';
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(120_000);
    expect(mockMine).toHaveBeenCalledTimes(1);

    visibility = 'visible';
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(0);
    expect(mockMine).toHaveBeenCalledTimes(2);
  });

  it('a failed poll keeps the list on screen', async () => {
    mockMine.mockResolvedValueOnce(active()).mockRejectedValueOnce(new Error('offline')).mockResolvedValue(active());
    render(Page);
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(mockMine).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Dune')).toBeTruthy();
    expect(screen.queryByText('offline')).toBeNull();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(mockMine).toHaveBeenCalledTimes(3);
  });
});
