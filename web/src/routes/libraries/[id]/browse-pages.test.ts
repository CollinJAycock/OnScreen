import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockGenres = vi.hoisted(() => vi.fn());
const mockYears = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: readable({ url: new URL('http://localhost/libraries/lib-1/genres'), params: { id: 'lib-1' }, state: {} }),
}));
vi.mock('$lib/api', () => ({
  libraryApi: { get: mockLibraryGet },
  mediaApi: { genres: mockGenres, years: mockYears },
}));

import GenresPage from './genres/+page.svelte';
import YearsPage from './years/+page.svelte';

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockGenres.mockResolvedValue([{ name: 'Rock', count: 12 }]);
  mockYears.mockResolvedValue([{ year: 1969, count: 3 }]);
});

// A music library's genres and years are on its albums (the artists carry
// neither), so the browse pages count albums and open the albums view.
describe('Genre / year browse on a music library', () => {
  beforeEach(() => mockLibraryGet.mockResolvedValue({ id: 'lib-1', name: 'Music', type: 'music' }));

  it('lists album genres and opens that genre\'s albums', async () => {
    render(GenresPage);
    await fireEvent.click(await screen.findByRole('button', { name: /Rock/ }));
    expect(mockGenres).toHaveBeenCalledWith('lib-1', 'album');
    expect(mockGoto).toHaveBeenCalledWith('/libraries/lib-1?view=albums&genre=Rock');
    expect(screen.getByText(/counted in albums/)).toBeTruthy();
  });

  it('lists album years and opens that year\'s or decade\'s albums', async () => {
    render(YearsPage);
    await fireEvent.click(await screen.findByRole('button', { name: /^1969/ }));
    expect(mockYears).toHaveBeenCalledWith('lib-1', 'album');
    expect(mockGoto).toHaveBeenLastCalledWith('/libraries/lib-1?view=albums&year_min=1969&year_max=1969');
    await fireEvent.click(screen.getByRole('button', { name: '1960s' }));
    expect(mockGoto).toHaveBeenLastCalledWith('/libraries/lib-1?view=albums&year_min=1960&year_max=1969');
    expect(screen.getByText('3 albums')).toBeTruthy();
  });

  it('explains where album genres come from when there are none', async () => {
    mockGenres.mockResolvedValue([]);
    render(GenresPage);
    await waitFor(() => expect(screen.getByText(/genre tags in your music files/)).toBeTruthy());
  });
});

describe('Genre / year browse elsewhere', () => {
  beforeEach(() => mockLibraryGet.mockResolvedValue({ id: 'lib-1', name: 'Movies', type: 'movie' }));

  it('keeps the root-type facets and plain links', async () => {
    render(GenresPage);
    await fireEvent.click(await screen.findByRole('button', { name: /Rock/ }));
    expect(mockGenres).toHaveBeenCalledWith('lib-1', undefined);
    expect(mockGoto).toHaveBeenCalledWith('/libraries/lib-1?genre=Rock');
  });

  it('links years without a view', async () => {
    render(YearsPage);
    await fireEvent.click(await screen.findByRole('button', { name: /^1969/ }));
    expect(mockYears).toHaveBeenCalledWith('lib-1', undefined);
    expect(mockGoto).toHaveBeenCalledWith('/libraries/lib-1?year_min=1969&year_max=1969');
  });
});
