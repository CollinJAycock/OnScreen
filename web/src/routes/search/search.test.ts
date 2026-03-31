import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockSearch = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  searchApi: { search: mockSearch },
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

    it('searches after debounce', async () => {
      mockSearch.mockResolvedValue([
        { id: 'item-1', title: 'The Matrix', type: 'movie', year: 1999 },
      ]);

      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: 'matrix' } });

      // Advance past debounce timer.
      await vi.advanceTimersByTimeAsync(350);

      await waitFor(() => {
        expect(mockSearch).toHaveBeenCalledWith('matrix');
      });
    });

    it('does not search for empty query', async () => {
      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: '   ' } });
      await vi.advanceTimersByTimeAsync(350);

      expect(mockSearch).not.toHaveBeenCalled();
    });

    it('shows no results message', async () => {
      mockSearch.mockResolvedValue([]);

      render(Page);
      const input = screen.getByRole('textbox');
      await fireEvent.input(input, { target: { value: 'xyznonexistent' } });
      await vi.advanceTimersByTimeAsync(350);

      await waitFor(() => {
        expect(screen.getByText(/no results/i)).toBeTruthy();
      });
    });
  });
});
