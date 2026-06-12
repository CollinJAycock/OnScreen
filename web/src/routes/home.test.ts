import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockListLibraries = vi.hoisted(() => vi.fn());
const mockHubGet = vi.hoisted(() => vi.fn());
const mockScan = vi.hoisted(() => vi.fn());
const mockDel = vi.hoisted(() => vi.fn());
const mockGetPreferences = vi.hoisted(() => vi.fn());
const mockSetHubLayout = vi.hoisted(() => vi.fn());
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
  userApi: { getPreferences: mockGetPreferences, setHubLayout: mockSetHubLayout },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  // Default: user has never customized the hub layout.
  mockGetPreferences.mockResolvedValue({});
});

describe('Home page', () => {
  describe('auth guard', () => {
    it('redirects to /login when not authenticated', () => {
      render(Page);
      expect(mockGoto).toHaveBeenCalledWith('/login');
    });

    // Regression guard: every SSO/SAML/OIDC callback redirects to "/?{x}_auth=1"
    // and the layout's onMount races the page's auth gate to bootstrap
    // localStorage from /api/v1/auth/refresh. If the gate fires synchronously
    // before localStorage is populated, the user gets bounced to /login
    // even though they just successfully signed in upstream.
    it.each([['oidc_auth'], ['saml_auth'], ['google_auth']])(
      'waits for localStorage hydration when ?%s=1 marker is present',
      async (marker) => {
        const url = new URL(window.location.href);
        url.searchParams.set(marker, '1');
        window.history.replaceState({}, '', url.toString());
        try {
          mockListLibraries.mockResolvedValue([]);
          mockHubGet.mockResolvedValue({ continue_watching: [], recently_added: [] });

          render(Page);
          // Simulate the layout's bootstrap landing during the poll window.
          setTimeout(() => {
            localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'sso-user' }));
          }, 250);

          // Give the home-page poll loop time to detect the hydration.
          await new Promise((r) => setTimeout(r, 600));
          expect(mockGoto).not.toHaveBeenCalledWith('/login');
        } finally {
          url.searchParams.delete(marker);
          window.history.replaceState({}, '', url.pathname);
        }
      },
    );

    it('still redirects when marker present but layout never hydrates', async () => {
      const url = new URL(window.location.href);
      url.searchParams.set('oidc_auth', '1');
      window.history.replaceState({}, '', url.toString());
      try {
        render(Page);
        // Poll window is 30 × 100ms = 3s. Allow generous slack for the
        // happy-dom/vitest scheduler so the assertion isn't flaky.
        await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/login'), {
          timeout: 5000,
        });
      } finally {
        url.searchParams.delete('oidc_auth');
        window.history.replaceState({}, '', url.pathname);
      }
    }, 7000);
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

    it('shows per-library "Recently Added" row', async () => {
      mockListLibraries.mockResolvedValue([]);
      mockHubGet.mockResolvedValue({
        continue_watching: [],
        recently_added: [],
        recently_added_by_library: [
          {
            library_id: 'lib-movies',
            library_name: 'Movies',
            library_type: 'movie',
            items: [
              { id: 'item-2', title: 'Inception', year: 2010, updated_at: '2026-01-01' },
            ],
          },
        ],
      });

      render(Page);
      await waitFor(() => {
        expect(screen.getByText(/Recently Added to Movies/)).toBeTruthy();
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

describe('Hub layout customization', () => {
  beforeEach(() => {
    localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'admin' }));
    mockListLibraries.mockResolvedValue([]);
    mockHubGet.mockResolvedValue({
      continue_watching: [],
      recently_added: [],
      trending: [
        { id: 't1', title: 'Trending Thing', updated_at: '2026-01-01' },
      ],
      recently_added_by_library: [
        { library_id: 'lib-movies', library_name: 'Movies', library_type: 'movie',
          items: [{ id: 'm1', title: 'Inception', updated_at: '2026-01-01' }] },
      ],
    });
  });

  it('hides rows the saved layout disables', async () => {
    mockGetPreferences.mockResolvedValue({
      hub_layout: [{ key: 'trending', enabled: false }],
    });
    render(Page);
    await waitFor(() => expect(screen.getByText(/Recently Added to Movies/)).toBeTruthy());
    expect(screen.queryByText('Trending this week')).toBeNull();
  });

  it('orders rows per the saved layout', async () => {
    mockGetPreferences.mockResolvedValue({
      hub_layout: [
        { key: 'library:lib-movies', enabled: true },
        { key: 'trending', enabled: true },
      ],
    });
    render(Page);
    await waitFor(() => expect(screen.getByText('Trending this week')).toBeTruthy());
    const titles = Array.from(document.querySelectorAll('.hub-title')).map((el) => el.textContent?.trim());
    const libIdx = titles.findIndex((t) => t?.includes('Recently Added to Movies'));
    const trendIdx = titles.findIndex((t) => t?.includes('Trending this week'));
    expect(libIdx).toBeGreaterThanOrEqual(0);
    expect(trendIdx).toBeGreaterThan(libIdx);
  });

  it('saves the edited layout', async () => {
    mockGetPreferences.mockResolvedValue({});
    mockSetHubLayout.mockResolvedValue(undefined);
    render(Page);
    await waitFor(() => expect(screen.getByText('Customize rows')).toBeTruthy());
    await fireEvent.click(screen.getByText('Customize rows'));
    await waitFor(() => expect(screen.getByText('Hub rows')).toBeTruthy());
    await fireEvent.click(screen.getByText('Save'));
    await waitFor(() => expect(mockSetHubLayout).toHaveBeenCalledTimes(1));
    const rows = mockSetHubLayout.mock.calls[0][0];
    expect(rows.map((r: { key: string }) => r.key)).toContain('trending');
    expect(rows.map((r: { key: string }) => r.key)).toContain('library:lib-movies');
  });
});
