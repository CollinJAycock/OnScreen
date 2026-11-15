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

    it('links each entry to the page for its type, and badges a track as music', async () => {
      mockHistoryList.mockResolvedValue({
        items: [
          { id: 'h1', media_id: 'm-1', title: 'The Matrix', type: 'movie', occurred_at: '2026-03-30T12:00:00Z' },
          { id: 'h2', media_id: 'b-1', title: 'Dune', type: 'audiobook', occurred_at: '2026-03-30T12:00:00Z' },
          { id: 'h3', media_id: 't-1', title: 'Teardrop', type: 'track', occurred_at: '2026-03-30T12:00:00Z' },
        ],
      });

      render(Page);
      await waitFor(() => expect(screen.getByText('Dune')).toBeTruthy());
      const href = (title: string) => screen.getByText(title).closest('a')?.getAttribute('href');
      expect(href('The Matrix')).toBe('/watch/m-1');
      expect(href('Dune')).toBe('/audiobooks/b-1');
      expect(href('Teardrop')).toBe('/watch/t-1');
      expect(screen.getByText('Music')).toBeTruthy();
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
