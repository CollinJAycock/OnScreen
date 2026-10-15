import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockStudioGet = vi.hoisted(() => vi.fn());
const mockStudioItems = vi.hoisted(() => vi.fn());
const mockStudioList = vi.hoisted(() => vi.fn());
const mockLibraryGet = vi.hoisted(() => vi.fn());
const pageState = vi.hoisted(() => ({ href: 'http://localhost/studios/s-1?library_id=lib-1', id: 's-1' }));

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: readable({
    get url() { return new URL(pageState.href); },
    get params() { return { id: pageState.id }; },
    state: {},
  }),
}));
vi.mock('$lib/api', () => ({
  studiosApi: { get: mockStudioGet, items: mockStudioItems, list: mockStudioList },
  libraryApi: { get: mockLibraryGet },
  assetUrl: (p: string) => p,
}));

import StudioPage from './+page.svelte';
import LibraryStudiosPage from '../../libraries/[id]/studios/+page.svelte';

const item = (n: number) => ({
  id: `m-${n}`, title: `Film ${n}`, type: 'movie', year: 1990 + n, created_at: '', updated_at: '',
});

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: 'u1' }));
  mockStudioGet.mockResolvedValue({ id: 's-1', name: 'Pixar', kind: 'studio', item_count: 3 });
});

describe('Studio page', () => {
  it('lists the studio\'s titles within the library it was opened from, and pages', async () => {
    mockStudioItems
      .mockResolvedValueOnce({ items: [item(1), item(2)], total: 3 })
      .mockResolvedValueOnce({ items: [item(3)], total: 3 });
    render(StudioPage);

    expect(await screen.findByRole('heading', { name: 'Pixar' })).toBeTruthy();
    expect(screen.getByText('Studio · 3 titles')).toBeTruthy();
    expect(mockStudioItems).toHaveBeenCalledWith('s-1', 60, 0, { sort: 'title', library_id: 'lib-1' });
    expect((screen.getByRole('link', { name: /Film 1/ }) as HTMLAnchorElement).getAttribute('href')).toBe('/watch/m-1');

    await fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(screen.getByRole('link', { name: /Film 3/ })).toBeTruthy());
    expect(mockStudioItems).toHaveBeenLastCalledWith('s-1', 60, 2, { sort: 'title', library_id: 'lib-1' });
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('re-sorts from the first page', async () => {
    mockStudioItems.mockResolvedValue({ items: [item(1)], total: 1 });
    render(StudioPage);
    const select = (await screen.findByLabelText('Sort')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: 'year' } });
    await waitFor(() =>
      expect(mockStudioItems).toHaveBeenLastCalledWith('s-1', 60, 0, { sort: 'year', library_id: 'lib-1' }));
  });

  it('shows the error for a studio the user can\'t see', async () => {
    mockStudioGet.mockRejectedValue(new Error('not found'));
    mockStudioItems.mockRejectedValue(new Error('not found'));
    render(StudioPage);
    expect(await screen.findByText('not found')).toBeTruthy();
  });
});

describe('Library studios page', () => {
  beforeEach(() => {
    pageState.id = 'lib-1';
    pageState.href = 'http://localhost/libraries/lib-1/studios';
  });
  afterEach(() => {
    pageState.id = 's-1';
    pageState.href = 'http://localhost/studios/s-1?library_id=lib-1';
  });

  it('lists a show library\'s networks and links each to its page in this library', async () => {
    mockLibraryGet.mockResolvedValue({ id: 'lib-1', name: 'TV', type: 'show' });
    mockStudioList.mockResolvedValue([{ id: 'n-1', name: 'AMC', kind: 'network', item_count: 2 }]);
    render(LibraryStudiosPage);
    const link = (await screen.findByRole('link', { name: /AMC/ })) as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/studios/n-1?library_id=lib-1');
    expect(mockStudioList).toHaveBeenCalledWith({ library_id: 'lib-1', kind: 'network' });
    expect(screen.getByRole('heading', { name: 'Browse by Network' })).toBeTruthy();
  });

  it('explains where studios come from when there are none', async () => {
    mockLibraryGet.mockResolvedValue({ id: 'lib-1', name: 'Movies', type: 'movie' });
    mockStudioList.mockResolvedValue([]);
    render(LibraryStudiosPage);
    expect(await screen.findByText(/No studios yet/)).toBeTruthy();
  });
});
