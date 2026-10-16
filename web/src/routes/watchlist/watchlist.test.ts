import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockList = vi.hoisted(() => vi.fn());
const mockCounts = vi.hoisted(() => vi.fn());
const mockSet = vi.hoisted(() => vi.fn());
const mockClear = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  watchlistApi: { list: mockList, counts: mockCounts },
  itemApi: { setWatchStatus: mockSet, clearWatchStatus: mockClear },
  assetUrl: (p: string) => p,
}));

function row(id: string, title: string, over: Record<string, unknown> = {}) {
  return {
    id, title, library_id: 'l1', type: 'movie', watch_state: 'unwatched', status: 'plan_to_watch',
    status_created_at: '2026-10-01T00:00:00Z', status_updated_at: '2026-10-01T00:00:00Z', ...over,
  };
}

const counts = {
  total: 3,
  statuses: {
    plan_to_watch: { total: 2, types: { movie: 1, episode: 1 } },
    watching: { total: 1, types: { show: 1 } },
    on_hold: { total: 0, types: {} },
    completed: { total: 0, types: {} },
    dropped: { total: 0, types: {} },
  },
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  window.history.replaceState(null, '', '/watchlist');
});

describe('Watchlist page', () => {
  it('redirects to /login when not authenticated', () => {
    render(Page);
    expect(mockGoto).toHaveBeenCalledWith('/login');
    expect(mockList).not.toHaveBeenCalled();
  });

  describe('authenticated', () => {
    beforeEach(() => {
      localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'me' }));
      mockCounts.mockResolvedValue(counts);
    });

    it('loads Plan to Watch first and renders cards with subtitles', async () => {
      mockList.mockResolvedValue({
        items: [
          row('a', 'Alien', { year: 1979 }),
          row('b', 'Pilot', { type: 'episode', grandparent_title: 'Severance', parent_index: 1, index: 1 }),
        ],
        total: 2,
      });
      render(Page);
      await waitFor(() => expect(screen.getByText('Alien')).toBeTruthy());
      expect(mockList).toHaveBeenCalledWith(expect.objectContaining({ status: 'plan_to_watch', offset: 0, sort: 'recent' }));
      expect(screen.getByText('Severance · S1 · E1')).toBeTruthy();
      expect(screen.getByRole('link', { name: /Alien/ }).getAttribute('href')).toBe('/watch/a');
      const tab = screen.getByRole('tab', { name: /Plan to Watch/ });
      expect(tab.getAttribute('aria-selected')).toBe('true');
      await waitFor(() => expect(tab.textContent).toContain('2'));
    });

    it('switches tabs and reloads with that status', async () => {
      mockList.mockResolvedValue({ items: [], total: 0 });
      render(Page);
      await waitFor(() => expect(mockList).toHaveBeenCalledTimes(1));
      await fireEvent.click(screen.getByRole('tab', { name: /Watching/ }));
      await waitFor(() => expect(mockList).toHaveBeenLastCalledWith(expect.objectContaining({ status: 'watching', offset: 0 })));
      expect(mockGoto).toHaveBeenLastCalledWith('/watchlist?status=watching', expect.objectContaining({ replaceState: true }));
    });

    it('starts on the tab and type named in the URL', async () => {
      window.history.replaceState(null, '', '/watchlist?status=on_hold&type=movie&sort=title');
      mockList.mockResolvedValue({ items: [], total: 0 });
      render(Page);
      await waitFor(() =>
        expect(mockList).toHaveBeenCalledWith(expect.objectContaining({ status: 'on_hold', type: 'movie', sort: 'title' })),
      );
    });

    it('filters by type from the counts', async () => {
      mockList.mockResolvedValue({ items: [row('a', 'Alien')], total: 1 });
      render(Page);
      const select = await screen.findByLabelText('Filter by type');
      expect(select.textContent).toContain('Episodes (1)');
      await fireEvent.change(select, { target: { value: 'episode' } });
      await waitFor(() => expect(mockList).toHaveBeenLastCalledWith(expect.objectContaining({ type: 'episode' })));
    });

    it('explains how to add things when the tab is empty', async () => {
      mockList.mockResolvedValue({ items: [], total: 0 });
      render(Page);
      await waitFor(() => expect(screen.getByText(/Nothing in Plan to Watch yet/)).toBeTruthy());
      expect(screen.getByText(/Status/, { selector: 'strong' })).toBeTruthy();
    });

    it('shows the error when loading fails', async () => {
      mockList.mockRejectedValue(new Error('Server error'));
      render(Page);
      await waitFor(() => expect(screen.getByText('Server error')).toBeTruthy());
    });

    it('pages with Load more from the current offset', async () => {
      mockList
        .mockResolvedValueOnce({ items: [row('a', 'Alien')], total: 2 })
        .mockResolvedValueOnce({ items: [row('b', 'Brazil')], total: 2 });
      render(Page);
      await fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
      await waitFor(() => expect(screen.getByText('Brazil')).toBeTruthy());
      expect(mockList).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 1 }));
      expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
    });

    it('moves a card to another status and drops it from this tab', async () => {
      mockList.mockResolvedValue({ items: [row('a', 'Alien'), row('b', 'Brazil')], total: 2 });
      mockSet.mockResolvedValue({ status: 'watching' });
      render(Page);
      await fireEvent.click(await screen.findByRole('button', { name: 'More actions for Alien' }));
      await fireEvent.click(screen.getByRole('menuitem', { name: 'Move to Watching' }));
      await waitFor(() => expect(screen.queryByText('Alien')).toBeNull());
      expect(mockSet).toHaveBeenCalledWith('a', 'watching');
      expect(screen.getByText('1 item')).toBeTruthy();
    });

    it('removes a card from the list', async () => {
      mockList.mockResolvedValue({ items: [row('a', 'Alien'), row('b', 'Brazil')], total: 2 });
      mockClear.mockResolvedValue(undefined);
      render(Page);
      await fireEvent.click(await screen.findByRole('button', { name: 'More actions for Brazil' }));
      await fireEvent.click(screen.getByRole('menuitem', { name: 'Remove from list' }));
      await waitFor(() => expect(screen.queryByText('Brazil')).toBeNull());
      expect(mockClear).toHaveBeenCalledWith('b');
    });

    it('keeps the card when the change fails', async () => {
      mockList.mockResolvedValue({ items: [row('a', 'Alien')], total: 1 });
      mockClear.mockRejectedValue(new Error('nope'));
      render(Page);
      await fireEvent.click(await screen.findByRole('button', { name: 'More actions for Alien' }));
      await fireEvent.click(screen.getByRole('menuitem', { name: 'Remove from list' }));
      await waitFor(() => expect(mockClear).toHaveBeenCalled());
      expect(screen.getByText('Alien')).toBeTruthy();
    });
  });
});
