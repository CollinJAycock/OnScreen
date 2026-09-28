import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import RequestMoreSeasonsButton from './RequestMoreSeasonsButton.svelte';

const mockSeasons = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockQuota = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastInfo = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  discoverApi: { seasons: mockSeasons },
  requestsApi: { create: mockCreate, quota: mockQuota },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, info: mockToastInfo, error: vi.fn() },
}));

const S = (n: number, over: Record<string, unknown> = {}) => ({
  season_number: n,
  name: `Season ${n}`,
  episode_count: 10,
  aired_episodes: 10,
  air_date: '2020-01-01',
  owned_episodes: 0,
  requested: null,
  ...over,
});

const canRequest = { can_request: true, window_days: 7, movies: { limit: 0, used: 0, remaining: null }, tv: { limit: 0, used: 0, remaining: null } };

beforeEach(() => {
  vi.clearAllMocks();
  mockQuota.mockResolvedValue(canRequest);
});

const trigger = () => screen.queryByRole('button', { name: /Request more seasons/ });

describe('RequestMoreSeasonsButton', () => {
  it('stays hidden when every aired season is in the library or requested', async () => {
    mockSeasons.mockResolvedValue([S(1, { owned_episodes: 10 }), S(2, { requested: 'pending' }), S(3, { aired_episodes: 0 })]);
    render(RequestMoreSeasonsButton, { tmdbId: 1399, title: 'Show' });
    await waitFor(() => expect(mockSeasons).toHaveBeenCalledWith(1399));
    await waitFor(() => expect(mockQuota).toHaveBeenCalled());
    expect(trigger()).toBeNull();
  });

  it('stays hidden, without asking for the seasons, when the user may not request', async () => {
    mockSeasons.mockResolvedValue([S(1, { owned_episodes: 10 }), S(2)]);
    mockQuota.mockResolvedValue({ ...canRequest, can_request: false });
    render(RequestMoreSeasonsButton, { tmdbId: 1399, title: 'Show' });
    await waitFor(() => expect(mockQuota).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(trigger()).toBeNull();
    expect(mockSeasons).not.toHaveBeenCalled();
  });

  it('stays hidden when the season list is unavailable', async () => {
    mockSeasons.mockRejectedValue(new Error('TMDB not configured'));
    render(RequestMoreSeasonsButton, { tmdbId: 1399, title: 'Show' });
    await waitFor(() => expect(mockSeasons).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(trigger()).toBeNull();
  });

  it('requests the missing seasons through the picker', async () => {
    mockSeasons.mockResolvedValue([S(1, { owned_episodes: 10 }), S(2, { owned_episodes: 4 }), S(3)]);
    mockCreate.mockResolvedValue({ id: 'r1', status: 'pending', auto_approved: false });
    render(RequestMoreSeasonsButton, { tmdbId: 1399, title: 'Show' });
    const btn = await screen.findByRole('button', { name: /Request more seasons/ });
    await fireEvent.click(btn);
    await screen.findByText('In library');
    await fireEvent.click(screen.getByRole('button', { name: 'Request seasons 2, 3' }));
    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith({ type: 'show', tmdb_id: 1399, seasons: [2, 3] }),
    );
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith('Requested: Show — awaiting admin approval'));
    // The season list is re-read so the requested seasons show as such.
    await waitFor(() => expect(mockSeasons).toHaveBeenCalledTimes(3));
  });

  it('says so when the request went over the quota', async () => {
    mockSeasons.mockResolvedValue([S(1, { owned_episodes: 10 }), S(2)]);
    mockCreate.mockResolvedValue({ id: 'r1', status: 'pending', over_quota: true });
    render(RequestMoreSeasonsButton, { tmdbId: 5, title: 'Show' });
    await fireEvent.click(await screen.findByRole('button', { name: /Request more seasons/ }));
    await screen.findByText('In library');
    await fireEvent.click(screen.getByRole('button', { name: 'Request season 2' }));
    await waitFor(() => expect(mockToastInfo).toHaveBeenCalledTimes(1));
    expect(mockToastInfo.mock.calls[0][0]).toContain("You've reached your limit");
  });
});
