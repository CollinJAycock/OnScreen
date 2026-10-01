import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockSearch = vi.hoisted(() => vi.fn());
const mockDiscover = vi.hoisted(() => vi.fn());
const mockSeasons = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockQuota = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastInfo = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  api: { getUser: () => JSON.parse(localStorage.getItem('onscreen_user') ?? 'null') },
  searchApi: { search: mockSearch },
  discoverApi: { search: mockDiscover, seasons: mockSeasons },
  requestsApi: { create: mockCreate, quota: mockQuota },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError, info: mockToastInfo },
}));
// What the server advertises; a test flips features.requests.
const capsStore = vi.hoisted(() => ({}) as { set: (v: unknown) => void });
vi.mock('$lib/stores/capabilities', async () => {
  const { writable } = await import('svelte/store');
  const capabilities = writable<unknown>(null);
  capsStore.set = capabilities.set;
  return { capabilities, ensureCapabilities: vi.fn().mockResolvedValue(undefined) };
});

const unlimited = {
  can_request: true,
  window_days: 7,
  movies: { limit: 0, used: 0, remaining: null },
  tv: { limit: 0, used: 0, remaining: null },
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  vi.useFakeTimers();
  mockQuota.mockResolvedValue(unlimited);
  capsStore.set({ features: { requests: true } });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('Search page', () => {
  it('redirects to /login when not authenticated', () => {
    render(Page);
    expect(mockGoto).toHaveBeenCalledWith('/login');
  });

  describe('authenticated', () => {
    beforeEach(() => {
      localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'admin' }));
    });

    it('renders search input', () => {
      render(Page);
      expect(screen.getByRole('textbox')).toBeTruthy();
    });

    it('searches library + discover after debounce', async () => {
      mockSearch.mockResolvedValue([
        { id: 'item-1', title: 'The Matrix', type: 'movie', year: 1999 },
      ]);
      mockDiscover.mockResolvedValue([]);

      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: 'matrix' } });

      await vi.advanceTimersByTimeAsync(350);

      await waitFor(() => {
        expect(mockSearch).toHaveBeenCalledWith('matrix');
        expect(mockDiscover).toHaveBeenCalledWith('matrix', 12);
      });
    });

    it('does not search for empty query', async () => {
      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: '   ' } });
      await vi.advanceTimersByTimeAsync(350);

      expect(mockSearch).not.toHaveBeenCalled();
      expect(mockDiscover).not.toHaveBeenCalled();
    });

    it('shows no results message when both library and discover are empty', async () => {
      mockSearch.mockResolvedValue([]);
      mockDiscover.mockResolvedValue([]);

      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: 'xyznonexistent' } });
      await vi.advanceTimersByTimeAsync(350);

      await waitFor(() => {
        expect(screen.getByText(/no results/i)).toBeTruthy();
      });
    });

    it('filters in-library items out of the discover section', async () => {
      mockSearch.mockResolvedValue([
        { id: 'lib-1', title: 'The Matrix', type: 'movie', year: 1999 },
      ]);
      mockDiscover.mockResolvedValue([
        // This one is already in the library — the search page should
        // drop it from the Request section so the same title doesn't
        // render twice.
        {
          type: 'movie', tmdb_id: 603, title: 'The Matrix', year: 1999,
          in_library: true, library_item_id: 'lib-1',
          has_active_request: false,
        },
        // This one is not in the library → should show up with a
        // Request button.
        {
          type: 'movie', tmdb_id: 624860, title: 'The Matrix Resurrections', year: 2021,
          in_library: false, has_active_request: false,
        },
      ]);

      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: 'matrix' } });
      await vi.advanceTimersByTimeAsync(350);

      await waitFor(() => {
        expect(screen.getByText('The Matrix Resurrections')).toBeTruthy();
      });
      // The in-library Matrix appears ONCE (in the library section),
      // not twice. A naive merge would duplicate it.
      const matrixCards = screen.getAllByText('The Matrix');
      expect(matrixCards).toHaveLength(1);
      // Request button present for the non-library result.
      expect(screen.getByRole('button', { name: /request/i })).toBeTruthy();
    });

    describe('requesting a title', () => {
      const dune = {
        type: 'movie', tmdb_id: 438631, title: 'Dune', year: 2021,
        in_library: false, has_active_request: false,
      };

      async function requestDune() {
        mockSearch.mockResolvedValue([]);
        mockDiscover.mockResolvedValue([dune]);
        render(Page);
        await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'dune' } });
        await vi.advanceTimersByTimeAsync(350);
        await waitFor(() => expect(screen.getByRole('button', { name: /request/i })).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: /request/i }));
        await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledTimes(1));
        return mockToastSuccess.mock.calls[0][0] as string;
      }

      it('says it is on its way when the server auto-approved it', async () => {
        mockCreate.mockResolvedValue({ id: 'r1', status: 'downloading', auto_approved: true });
        const msg = await requestDune();
        expect(mockCreate).toHaveBeenCalledWith({ type: 'movie', tmdb_id: 438631 });
        expect(msg).toContain("Approved automatically — it's on its way");
        expect(msg).toContain('Dune');
        // The card flips to the returned status, not to "Pending".
        await waitFor(() => expect(screen.getByText('Downloading')).toBeTruthy());
      });

      it('treats an already-approved status as auto-approved', async () => {
        mockCreate.mockResolvedValue({ id: 'r1', status: 'approved' });
        expect(await requestDune()).toContain('Approved automatically');
      });

      it('says it awaits approval when the request is pending', async () => {
        // Also what a failed hand-off to Radarr/Sonarr looks like.
        mockCreate.mockResolvedValue({ id: 'r1', status: 'pending', auto_approved: false });
        const msg = await requestDune();
        expect(msg).not.toContain('Approved automatically');
        expect(msg).toMatch(/Requested: Dune.*awaiting admin approval/);
        await waitFor(() => expect(screen.getByText('Pending')).toBeTruthy());
      });

      async function searchDune() {
        mockSearch.mockResolvedValue([]);
        mockDiscover.mockResolvedValue([dune]);
        render(Page);
        await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'dune' } });
        await vi.advanceTimersByTimeAsync(350);
        await waitFor(() => expect(screen.getByText('Dune')).toBeTruthy());
      }

      it('says the limit was reached when the request went over quota', async () => {
        mockCreate.mockResolvedValue({ id: 'r1', status: 'pending', auto_approved: false, over_quota: true });
        await searchDune();
        await fireEvent.click(screen.getByRole('button', { name: /request/i }));
        await waitFor(() => expect(mockToastInfo).toHaveBeenCalledTimes(1));
        expect(mockToastInfo.mock.calls[0][0]).toContain("You've reached your limit — this request needs admin approval");
        expect(mockToastSuccess).not.toHaveBeenCalled();
        await waitFor(() => expect(screen.getByText('Pending')).toBeTruthy());
        // The allowance is re-read after the request.
        await waitFor(() => expect(mockQuota).toHaveBeenCalledTimes(2));
      });

      it('shows how many requests are left in the window', async () => {
        mockQuota.mockResolvedValue({
          can_request: true,
          window_days: 7,
          movies: { limit: 3, used: 1, remaining: 2 },
          tv: { limit: 1, used: 1, remaining: 0 },
        });
        await searchDune();
        await waitFor(() => expect(screen.getByText('2 movie requests left this week')).toBeTruthy());
        expect(screen.getByText('No TV requests left this week — new ones need admin approval')).toBeTruthy();
        // Over the limit is still allowed (it just waits for an admin).
        expect((screen.getByRole('button', { name: /request/i }) as HTMLButtonElement).disabled).toBe(false);
      });

      it('shows nothing about limits when requesting is unlimited', async () => {
        await searchDune();
        expect(screen.queryByTestId('request-quota')).toBeNull();
      });

      // A library search only: no TMDB results, posters or "ask your admin"
      // copy for an account that may not request — and TMDB isn't asked.
      async function searchWithoutRequests() {
        mockSearch.mockResolvedValue([{ id: 'item-1', title: 'Sintel', type: 'movie', year: 2010 }]);
        mockDiscover.mockResolvedValue([dune]);
        render(Page);
        await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'sintel' } });
        await vi.advanceTimersByTimeAsync(350);
        await waitFor(() => expect(screen.getByText('Sintel')).toBeTruthy());
        await vi.advanceTimersByTimeAsync(0);
      }

      it('shows no request section when requesting is turned off for the account', async () => {
        mockQuota.mockResolvedValue({ ...unlimited, can_request: false });
        await searchWithoutRequests();
        expect(mockDiscover).not.toHaveBeenCalled();
        expect(screen.queryByTestId('discover-section')).toBeNull();
        expect(screen.queryByText('Ask your admin to add')).toBeNull();
        expect(screen.queryByText('Dune')).toBeNull();
        expect((screen.getByRole('textbox') as HTMLInputElement).placeholder).toBe('Search your library…');
      });

      it('shows no request section when the server has requests off (no TMDB key)', async () => {
        capsStore.set({ features: { requests: false } });
        await searchWithoutRequests();
        expect(mockDiscover).not.toHaveBeenCalled();
        expect(screen.queryByTestId('discover-section')).toBeNull();
      });

      it('drops the request section after a REQUESTS_DISABLED refusal', async () => {
        mockCreate.mockRejectedValue(Object.assign(new Error('requesting is turned off'), { code: 'REQUESTS_DISABLED', status: 403 }));
        mockQuota.mockResolvedValueOnce(unlimited).mockResolvedValue({ ...unlimited, can_request: false });
        await searchDune();
        await fireEvent.click(screen.getByRole('button', { name: /request/i }));
        await waitFor(() => expect(mockToastError).toHaveBeenCalledTimes(1));
        expect(mockToastError.mock.calls[0][0]).toMatch(/turned off for your account/);
        await waitFor(() => expect(screen.queryByTestId('discover-section')).toBeNull());
      });

      it('drops the request section when the server refuses the search itself', async () => {
        mockSearch.mockResolvedValue([]);
        mockDiscover.mockRejectedValue(Object.assign(new Error('requesting is turned off'), { code: 'REQUESTS_DISABLED', status: 403 }));
        render(Page);
        await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'dune' } });
        await vi.advanceTimersByTimeAsync(350);
        await waitFor(() => expect(mockDiscover).toHaveBeenCalled());
        await waitFor(() => expect(screen.queryByTestId('discover-section')).toBeNull());
        expect(screen.queryByText(/turned off/)).toBeNull();
      });

      it('keeps Request for an admin when the quota endpoint is unavailable', async () => {
        localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'admin', is_admin: true }));
        mockQuota.mockRejectedValue(new Error('404'));
        await searchDune();
        expect((screen.getByRole('button', { name: /request/i }) as HTMLButtonElement).disabled).toBe(false);
        expect(screen.queryByTestId('request-quota')).toBeNull();
      });

      it('fails closed for a non-admin whose allowance could not be read', async () => {
        mockQuota.mockRejectedValue(new Error('500'));
        await searchWithoutRequests();
        expect(mockDiscover).not.toHaveBeenCalled();
        expect(screen.queryByTestId('discover-section')).toBeNull();
      });
    });

    describe('requesting a show (season picker)', () => {
      const season = (n: number, over: Record<string, unknown> = {}) => ({
        season_number: n, name: n === 0 ? 'Specials' : `Season ${n}`, episode_count: 10,
        aired_episodes: 10, air_date: '2020-01-01', owned_episodes: 0, requested: null, ...over,
      });
      const severance = {
        type: 'show', tmdb_id: 95396, title: 'Severance', year: 2022,
        in_library: false, has_active_request: false,
      };

      async function searchFor(q: string, results: unknown[]) {
        mockSearch.mockResolvedValue([]);
        mockDiscover.mockResolvedValue(results);
        render(Page);
        await fireEvent.input(screen.getByRole('textbox'), { target: { value: q } });
        await vi.advanceTimersByTimeAsync(350);
      }

      it('opens the picker and sends every season for a fresh show', async () => {
        mockSeasons.mockResolvedValue([season(1), season(2), season(0, { episode_count: 3 })]);
        mockCreate.mockResolvedValue({ id: 'r1', status: 'pending', auto_approved: false });
        await searchFor('severance', [severance]);
        await waitFor(() => expect(screen.getByRole('button', { name: 'Request' })).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: 'Request' }));
        await waitFor(() => expect(mockSeasons).toHaveBeenCalledWith(95396));
        await waitFor(() => expect(screen.getByRole('button', { name: 'Request all seasons' })).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: 'Request all seasons' }));
        // No seasons key: "every season" (the server's meaning of none).
        await waitFor(() => expect(mockCreate).toHaveBeenCalledWith({ type: 'show', tmdb_id: 95396 }));
        await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledTimes(1));
        expect(mockToastSuccess.mock.calls[0][0]).toMatch(/Requested: Severance.*awaiting admin approval/);
        await waitFor(() => expect(screen.getByText('Pending')).toBeTruthy());
      });

      it('sends the picked seasons', async () => {
        mockSeasons.mockResolvedValue([season(1), season(2), season(3)]);
        mockCreate.mockResolvedValue({ id: 'r1', status: 'pending' });
        await searchFor('severance', [severance]);
        await waitFor(() => expect(screen.getByRole('button', { name: 'Request' })).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: 'Request' }));
        await waitFor(() => expect(screen.getByRole('checkbox', { name: /Season 1/ })).toBeTruthy());
        await fireEvent.click(screen.getByRole('checkbox', { name: /Season 1/ }));
        await fireEvent.click(screen.getByRole('button', { name: 'Request seasons 2, 3' }));
        await waitFor(() =>
          expect(mockCreate).toHaveBeenCalledWith({ type: 'show', tmdb_id: 95396, seasons: [2, 3] }),
        );
      });

      it('offers "Request more seasons" for a show partly in the library', async () => {
        mockSeasons.mockResolvedValue([season(1, { owned_episodes: 10 }), season(2)]);
        mockCreate.mockResolvedValue({ id: 'r2', status: 'downloading', auto_approved: true });
        await searchFor('severance', [
          { ...severance, in_library: true, library_item_id: 'lib-9', partially_available: true, missing_seasons: [2] },
          // Fully owned: stays out of the request section.
          { type: 'show', tmdb_id: 1, title: 'Complete Show', in_library: true, has_active_request: false,
            partially_available: false, missing_seasons: [] },
        ]);
        await waitFor(() => expect(screen.getByRole('button', { name: 'Request more seasons' })).toBeTruthy());
        expect(screen.getByText('In your library · 1 season missing')).toBeTruthy();
        expect(screen.queryByText('Complete Show')).toBeNull();

        await fireEvent.click(screen.getByRole('button', { name: 'Request more seasons' }));
        await waitFor(() => expect(screen.getByText('In library')).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: 'Request season 2' }));
        await waitFor(() =>
          expect(mockCreate).toHaveBeenCalledWith({ type: 'show', tmdb_id: 95396, seasons: [2] }),
        );
        await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledTimes(1));
        expect(mockToastSuccess.mock.calls[0][0]).toContain('Approved automatically');
      });

      it('shows a refusal inside the picker instead of a toast', async () => {
        mockSeasons.mockResolvedValue([season(1), season(2)]);
        mockCreate.mockRejectedValue(Object.assign(new Error('requesting is turned off'), { code: 'REQUESTS_DISABLED', status: 403 }));
        await searchFor('severance', [severance]);
        await waitFor(() => expect(screen.getByRole('button', { name: 'Request' })).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: 'Request' }));
        await waitFor(() => expect(screen.getByRole('button', { name: 'Request all seasons' })).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: 'Request all seasons' }));
        await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/turned off for your account/));
        expect(mockToastError).not.toHaveBeenCalled();
      });

      it('movies are still requested straight from the card', async () => {
        mockCreate.mockResolvedValue({ id: 'r1', status: 'pending' });
        await searchFor('heat', [{ type: 'movie', tmdb_id: 949, title: 'Heat', in_library: false, has_active_request: false }]);
        await waitFor(() => expect(screen.getByRole('button', { name: /request for admin/i })).toBeTruthy());
        await fireEvent.click(screen.getByRole('button', { name: /request for admin/i }));
        await waitFor(() => expect(mockCreate).toHaveBeenCalledWith({ type: 'movie', tmdb_id: 949 }));
        expect(mockSeasons).not.toHaveBeenCalled();
      });
    });

    it('hides discover errors when TMDB is not configured', async () => {
      mockSearch.mockResolvedValue([]);
      mockDiscover.mockRejectedValue(new Error('TMDB not configured'));

      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: 'anything' } });
      await vi.advanceTimersByTimeAsync(350);

      await waitFor(() => {
        // "not configured" class errors stay silent — admin setup issue,
        // not user-visible.
        expect(screen.queryByText(/TMDB not configured/)).toBeNull();
      });
    });
  });
});
