import { render, screen, waitFor } from '@testing-library/svelte';
import type { MediaRequest } from '$lib/api';
import Page from './+page.svelte';

// Season-level TV requests on the Requests page: the season progress line
// and the "Partially available" chip.

const mockGetUser = vi.hoisted(() => vi.fn());
const mockMine = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/api', () => ({
  api: { getUser: mockGetUser },
  upcomingApi: { get: vi.fn().mockResolvedValue({ from: '', to: '', items: [], services: [] }) },
  requestsApi: { list: mockMine, cancel: vi.fn() },
  requestsAdminApi: { list: vi.fn().mockResolvedValue({ items: [], total: 0 }) },
  arrServicesApi: { list: vi.fn().mockResolvedValue({ items: [], total: 0 }), probe: vi.fn() },
}));
vi.mock('$lib/stores/pendingRequests', () => ({ refreshPendingRequests: vi.fn() }));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
// Requests on, and the account may request.
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
    type: 'show',
    tmdb_id: 1,
    status: 'downloading',
    auto_approved: false,
    created_at: '2026-09-20T12:00:00Z',
    updated_at: '2026-09-20T12:00:00Z',
    ...over,
  };
}

const rowOf = (title: string) => screen.getByText(title).closest('.row') as HTMLElement;

beforeEach(() => {
  vi.clearAllMocks();
  mockGetUser.mockReturnValue({ user_id: 'u1', username: 'viewer', is_admin: false });
});

describe('Requests page — seasons', () => {
  it('shows season progress and a Partially available chip', async () => {
    mockMine.mockResolvedValue({
      items: [
        req({ id: 'a', title: 'Severance', seasons: [1, 2, 3], seasons_available: [1, 2] }),
        req({ id: 'b', title: 'Andor', seasons: [2], seasons_available: [] }),
        req({ id: 'c', title: 'Dune', type: 'movie' }),
      ],
      total: 3,
    });
    render(Page);
    await waitFor(() => expect(screen.getByText('Severance')).toBeTruthy());

    const sev = rowOf('Severance');
    expect(sev.textContent).toContain('Seasons 1–3 (2 of 3 available)');
    expect(sev.textContent).toContain('Partially available');
    // The status itself is unchanged.
    expect(sev.textContent).toContain('Downloading');

    const andor = rowOf('Andor');
    expect(andor.textContent).toContain('Season 2');
    expect(andor.textContent).not.toContain('Partially available');
    expect(rowOf('Dune').textContent).not.toContain('Partially available');
  });
});
