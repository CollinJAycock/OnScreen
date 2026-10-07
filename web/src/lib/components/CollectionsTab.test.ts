import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import CollectionsTab from './CollectionsTab.svelte';

const mockForLibrary = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  collectionApi: { forLibrary: mockForLibrary },
  assetUrl: (p: string) => `http://srv${p}`,
}));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('CollectionsTab', () => {
  it('renders one card per franchise with its cover and visible film count', async () => {
    mockForLibrary.mockResolvedValue([
      { id: 'c-1', name: 'Alien Collection', type: 'franchise', poster_url: 'https://image.tmdb.org/p.jpg', item_count: 3 },
      { id: 'c-2', name: 'Toy Story Collection', type: 'franchise', poster_path: 'movies/ts/poster.jpg', item_count: 1 },
    ]);
    render(CollectionsTab, { libraryId: 'lib-1' });

    const alien = await screen.findByRole('link', { name: /Alien Collection/ });
    expect(mockForLibrary).toHaveBeenCalledWith('lib-1');
    expect(alien.getAttribute('href')).toBe('/collections/c-1');
    expect(alien.textContent).toContain('3 films');
    expect(alien.querySelector('img')?.getAttribute('src')).toBe('https://image.tmdb.org/p.jpg');

    const ts = screen.getByRole('link', { name: /Toy Story Collection/ });
    expect(ts.textContent).toContain('1 film');
    // No TMDB poster: falls back to the member's artwork via the asset URL.
    expect(ts.querySelector('img')?.getAttribute('src')).toBe('http://srv/artwork/movies/ts/poster.jpg?w=300');
  });

  it('explains the empty state', async () => {
    mockForLibrary.mockResolvedValue([]);
    render(CollectionsTab, { libraryId: 'lib-1' });
    expect(await screen.findByText('No collections yet')).toBeTruthy();
  });

  it('shows the error and retries', async () => {
    mockForLibrary.mockRejectedValueOnce(new Error('HTTP 500'));
    render(CollectionsTab, { libraryId: 'lib-1' });
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('HTTP 500');

    mockForLibrary.mockResolvedValue([{ id: 'c-1', name: 'Alien Collection', type: 'franchise', item_count: 2 }]);
    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.getByRole('link', { name: /Alien Collection/ })).toBeTruthy());
    expect(mockForLibrary).toHaveBeenCalledTimes(2);
  });
});

describe('CollectionsTab — manual collections', () => {
  it('counts a manual collection in titles, with its member cover', async () => {
    mockForLibrary.mockResolvedValue([
      { id: 'm-1', name: 'Saturday cartoons', type: 'manual', poster_path: 'shows/bluey.jpg', item_count: 2 },
    ]);
    render(CollectionsTab, { libraryId: 'lib-2', libraryType: 'show' });
    const card = await screen.findByRole('link', { name: /Saturday cartoons/ });
    expect(card.getAttribute('href')).toBe('/collections/m-1');
    expect(card.textContent).toContain('2 titles');
    expect(card.querySelector('img')?.getAttribute('src')).toBe('http://srv/artwork/shows/bluey.jpg?w=300');
  });

  it('explains the empty state of a show library without film series', async () => {
    mockForLibrary.mockResolvedValue([]);
    render(CollectionsTab, { libraryId: 'lib-2', libraryType: 'show' });
    expect(await screen.findByText(/Collections an admin makes/)).toBeTruthy();
    expect(screen.queryByText(/Film series/)).toBeNull();
  });
});