import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockSearch = vi.hoisted(() => vi.fn());
const mockDiscover = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  searchApi: { search: mockSearch },
  discoverApi: { search: mockDiscover },
  requestsApi: { create: mockCreate },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: vi.fn() },
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  vi.useFakeTimers();
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
