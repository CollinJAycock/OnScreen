import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockListLibraries = vi.hoisted(() => vi.fn());
const mockHubGet = vi.hoisted(() => vi.fn());
const mockScan = vi.hoisted(() => vi.fn());
const mockDel = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  libraryApi: {
    list: mockListLibraries,
    scan: mockScan,
    del: mockDel,
  },
  hubApi: { get: mockHubGet },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

describe('Home page', () => {
  describe('auth guard', () => {
    it('redirects to /login when not authenticated', () => {
      render(Page);
      expect(mockGoto).toHaveBeenCalledWith('/login');
    });
  });

  describe('authenticated', () => {
    beforeEach(() => {
      localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'admin' }));
    });

    it('shows libraries after loading', async () => {
      mockListLibraries.mockResolvedValue([
        { id: 'lib-1', name: 'Movies', type: 'movie', scan_paths: ['/media/movies'] },
      ]);
      mockHubGet.mockResolvedValue({ continue_watching: [], recently_added: [] });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText('/media/movies')).toBeTruthy();
      });
    });

    it('shows empty state when no libraries', async () => {
      mockListLibraries.mockResolvedValue([]);
      mockHubGet.mockResolvedValue({ continue_watching: [], recently_added: [] });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText('No libraries')).toBeTruthy();
      });
    });

    it('shows continue watching hub', async () => {
      mockListLibraries.mockResolvedValue([]);
      mockHubGet.mockResolvedValue({
        continue_watching: [
          { id: 'item-1', title: 'The Matrix', year: 1999, view_offset_ms: 3600000, duration_ms: 7200000, updated_at: '2026-01-01' },
        ],
        recently_added: [],
      });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText('Continue Watching')).toBeTruthy();
        expect(screen.getByText('The Matrix')).toBeTruthy();
      });
    });

    it('shows recently added hub', async () => {
      mockListLibraries.mockResolvedValue([]);
      mockHubGet.mockResolvedValue({
        continue_watching: [],
        recently_added: [
          { id: 'item-2', title: 'Inception', year: 2010, updated_at: '2026-01-01' },
        ],
      });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText('Recently Added')).toBeTruthy();
        expect(screen.getByText('Inception')).toBeTruthy();
      });
    });

    it('shows error banner on load failure', async () => {
      mockListLibraries.mockRejectedValue(new Error('Network error'));
      mockHubGet.mockResolvedValue({ continue_watching: [], recently_added: [] });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText('Network error')).toBeTruthy();
      });
    });

    it('has a link to create new library', async () => {
      mockListLibraries.mockResolvedValue([]);
      mockHubGet.mockResolvedValue({ continue_watching: [], recently_added: [] });

      render(Page);
      await waitFor(() => {
        const links = screen.getAllByText('New Library');
        expect(links.length).toBeGreaterThan(0);
      });
    });
  });
});
