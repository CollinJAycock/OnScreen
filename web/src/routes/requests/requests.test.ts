import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import type { MediaRequest } from '$lib/api';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockGetUser = vi.hoisted(() => vi.fn());
const mockMine = vi.hoisted(() => vi.fn());
const mockQueue = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  api: { getUser: mockGetUser },
  upcomingApi: { get: vi.fn().mockResolvedValue({ from: '', to: '', items: [], services: [] }) },
  requestsApi: { list: mockMine, cancel: vi.fn() },
  requestsAdminApi: { list: mockQueue, approve: vi.fn(), decline: vi.fn(), del: vi.fn() },
}));

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

const rowOf = (title: string) => screen.getByText(title).closest('.row') as HTMLElement;

beforeEach(() => {
  vi.clearAllMocks();
  mockMine.mockResolvedValue({ items: [], total: 0 });
  mockQueue.mockResolvedValue({ items: [], total: 0 });
});

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
    render(Page);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Queue' })).toBeTruthy());
    await fireEvent.click(screen.getByRole('button', { name: 'Queue' }));
    await waitFor(() => expect(screen.getByText('Dune')).toBeTruthy());

    expect(within(rowOf('Dune')).getByText('Auto-approved')).toBeTruthy();
    expect(within(rowOf('Heat')).queryByText('Auto-approved')).toBeNull();
  });
});
