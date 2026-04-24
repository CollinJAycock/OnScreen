import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockSearch = vi.hoisted(() => vi.fn());
const mockDiscover = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  searchApi: { search: mockSearch },
  discoverApi: { search: mockDiscover },
  requestsApi: { create: mockCreate },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
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
