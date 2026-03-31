import { render, screen, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockHistoryList = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  historyApi: { list: mockHistoryList },
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

describe('History page', () => {
  it('redirects to /login when not authenticated', () => {
    render(Page);
    expect(mockGoto).toHaveBeenCalledWith('/login');
  });

  describe('authenticated', () => {
    beforeEach(() => {
      localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'admin' }));
    });

    it('shows watch history items', async () => {
      mockHistoryList.mockResolvedValue({
        items: [
          { id: '1', title: 'The Matrix', type: 'movie', watched_at: '2026-03-30T12:00:00Z', progress_pct: 85, poster_path: null },
        ],
      });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText('The Matrix')).toBeTruthy();
      });
    });

    it('shows empty state when no history', async () => {
      mockHistoryList.mockResolvedValue({ items: [] });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText(/no watch history/i)).toBeTruthy();
      });
    });

    it('shows error on load failure', async () => {
      mockHistoryList.mockRejectedValue(new Error('Server error'));

      render(Page);
      await waitFor(() => {
        expect(screen.getByText('Server error')).toBeTruthy();
      });
    });
  });
});
