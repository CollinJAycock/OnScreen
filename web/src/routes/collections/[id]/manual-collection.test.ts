import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGet = vi.hoisted(() => vi.fn());
const mockItems = vi.hoisted(() => vi.fn());
const mockUpdateSettings = vi.hoisted(() => vi.fn());
const mockReorder = vi.hoisted(() => vi.fn());
const mockRemoveItem = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());
const mockGoto = vi.hoisted(() => vi.fn());
const mockConfirm = vi.hoisted(() => vi.fn());
const mockUploadPoster = vi.hoisted(() => vi.fn());
const mockDeletePoster = vi.hoisted(() => vi.fn());
const mockLibraries = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL('http://localhost/collections/c-1'), params: { id: 'c-1' } }).subscribe(fn),
  },
}));
vi.mock('$lib/native', () => ({ confirmAction: mockConfirm }));
// jsdom can't decode images: the picked file goes up as it is.
vi.mock('$lib/coverImage', () => ({ fitCoverImage: async (f: Blob) => f }));
vi.mock('$lib/api', () => ({
  collectionApi: {
    get: mockGet,
    items: mockItems,
    update: vi.fn(),
    updateSettings: mockUpdateSettings,
    reorder: mockReorder,
    removeItem: mockRemoveItem,
    delete: mockDelete,
    uploadPoster: mockUploadPoster,
    deletePoster: mockDeletePoster,
  },
  libraryApi: { list: mockLibraries },
  assetUrl: (p: string) => p,
  collectionPosterUrl: (c: { id: string; poster_version?: number }) =>
    c.poster_version == null ? null : `/api/v1/collections/${c.id}/poster?v=${c.poster_version}`,
}));

import Page from './+page.svelte';

const spooky = {
  id: 'c-1', name: 'Spooky', type: 'manual', description: 'For October', item_order: 'custom',
  item_count: 3, created_at: '',
};
const members = [
  { id: 'm-1', title: 'Halloween', type: 'movie', year: 1978 },
  { id: 'm-2', title: 'Scream', type: 'movie', year: 1996 },
  { id: 'm-3', title: 'Coraline', type: 'movie', year: 2009 },
];

function signIn(isAdmin: boolean) {
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: '1', username: 'u', is_admin: isAdmin }));
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockGet.mockResolvedValue({ ...spooky });
  mockItems.mockResolvedValue({ items: members.map(m => ({ ...m })), total: members.length });
  mockReorder.mockResolvedValue(undefined);
  mockRemoveItem.mockResolvedValue(undefined);
  mockLibraries.mockResolvedValue([]);
});

afterEach(() => {
  localStorage.clear();
});

describe('Manual collection page — admin', () => {
  beforeEach(() => signIn(true));

  it('shows the description and changes the sort order', async () => {
    mockUpdateSettings.mockResolvedValue({ ...spooky, item_order: 'title' });
    render(Page);
    expect(await screen.findByText('For October')).toBeInTheDocument();
    const sort = screen.getByLabelText('Sort by') as HTMLSelectElement;
    expect(sort.value).toBe('custom');
    await fireEvent.change(sort, { target: { value: 'title' } });
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledWith('c-1', { item_order: 'title' }));
    // The items are listed again in the new order.
    await waitFor(() => expect(mockItems).toHaveBeenCalledTimes(2));
  });

  it('moves a member later in the custom order', async () => {
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Move Halloween later' }));
    await waitFor(() => expect(mockReorder).toHaveBeenCalledWith('c-1', ['m-2', 'm-1', 'm-3']));
    expect((screen.getByRole('button', { name: 'Move Scream earlier' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('puts the order back when a reorder fails', async () => {
    mockReorder.mockRejectedValue(new Error('nope'));
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Move Halloween later' }));
    expect(await screen.findByText('nope')).toBeInTheDocument();
    expect((screen.getByRole('button', { name: 'Move Halloween earlier' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('has no move buttons outside the custom order', async () => {
    mockGet.mockResolvedValue({ ...spooky, item_order: 'release' });
    render(Page);
    await screen.findByText('Halloween');
    expect(screen.queryByRole('button', { name: /Move .* later/ })).toBeNull();
  });

  it('makes a member the cover, and the same click goes back to the default', async () => {
    mockUpdateSettings.mockResolvedValueOnce({ ...spooky, poster_item_id: 'm-2' });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Use Scream as the cover' }));
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledWith('c-1', { poster_item_id: 'm-2' }));
    const star = screen.getByRole('button', { name: 'Use Scream as the cover' });
    await waitFor(() => expect(star.getAttribute('aria-pressed')).toBe('true'));
    mockUpdateSettings.mockResolvedValueOnce({ ...spooky });
    await fireEvent.click(star);
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenLastCalledWith('c-1', { poster_item_id: '' }));
  });

  it('renames with a description', async () => {
    mockUpdateSettings.mockResolvedValue({ ...spooky, name: 'Spookier', description: 'All year' });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit' }));
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Spookier' } });
    await fireEvent.input(screen.getByLabelText('Description'), { target: { value: 'All year' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledWith('c-1', { name: 'Spookier', description: 'All year' }));
    expect(await screen.findByRole('heading', { name: 'Spookier' })).toBeInTheDocument();
  });

  it('removes a member and deletes the collection', async () => {
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Remove Scream' }));
    await waitFor(() => expect(mockRemoveItem).toHaveBeenCalledWith('c-1', 'm-2'));
    expect(screen.queryByText('Scream')).toBeNull();

    mockConfirm.mockResolvedValue(true);
    mockDelete.mockResolvedValue(undefined);
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith('c-1'));
    expect(mockGoto).toHaveBeenCalledWith('/collections');
  });
});

describe('Manual collection page — phase 2 (admin)', () => {
  beforeEach(() => signIn(true));

  it('puts the collection on the home screen and takes it off again', async () => {
    mockUpdateSettings.mockResolvedValueOnce({ ...spooky, promoted: true });
    render(Page);
    const box = (await screen.findByLabelText('Show on Home')) as HTMLInputElement;
    expect(box.checked).toBe(false);
    await fireEvent.click(box);
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledWith('c-1', { promoted: true }));
    await waitFor(() => expect(box.checked).toBe(true));
    mockUpdateSettings.mockResolvedValueOnce({ ...spooky, promoted: false });
    await fireEvent.click(box);
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenLastCalledWith('c-1', { promoted: false }));
  });

  it('uploads a cover, shows it, and removes it', async () => {
    mockUploadPoster.mockResolvedValue({ ...spooky, poster_version: 42 });
    mockDeletePoster.mockResolvedValue(undefined);
    const { container } = render(Page);
    await screen.findByRole('button', { name: 'Upload cover' });
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(['img'], 'cover.png', { type: 'image/png' });
    await fireEvent.change(input, { target: { files: [file] } });
    await waitFor(() => expect(mockUploadPoster).toHaveBeenCalledWith('c-1', file));
    const thumb = (await screen.findByAltText('Cover')) as HTMLImageElement;
    expect(thumb.getAttribute('src')).toBe('/api/v1/collections/c-1/poster?v=42');

    mockGet.mockResolvedValue({ ...spooky });
    await fireEvent.click(screen.getByRole('button', { name: 'Remove cover' }));
    await waitFor(() => expect(mockDeletePoster).toHaveBeenCalledWith('c-1'));
    await waitFor(() => expect(screen.queryByAltText('Cover')).toBeNull());
  });

  it('shows an upload failure', async () => {
    mockUploadPoster.mockRejectedValue(new Error("the upload isn't an image OnScreen can read"));
    const { container } = render(Page);
    await screen.findByRole('button', { name: 'Upload cover' });
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    await fireEvent.change(input, { target: { files: [new File(['x'], 'x.txt', { type: 'text/plain' })] } });
    expect(await screen.findByText("the upload isn't an image OnScreen can read")).toBeInTheDocument();
  });

  it('makes the collection smart from the rules form', async () => {
    const rules = { types: ['movie'], genres: ['Horror'], year_min: 1980, year_max: 1989 };
    mockUpdateSettings.mockResolvedValue({ ...spooky, rules });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Make smart' }));
    await fireEvent.input(screen.getByLabelText('Genre'), { target: { value: 'Horror' } });
    await fireEvent.input(screen.getByLabelText('From year'), { target: { value: '1980' } });
    await fireEvent.input(screen.getByLabelText('To year'), { target: { value: '1989' } });
    const form = screen.getByRole('form', { name: 'Smart collection rules' });
    await fireEvent.click(form.querySelector('button[type="submit"]')!);
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledWith('c-1', { rules }));
    expect(await screen.findByText(/Horror movies from 1980–1989/)).toBeInTheDocument();
    // Members follow the rules now: no removing or reordering by hand.
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Remove Scream' })).toBeNull());
    expect(screen.queryByRole('button', { name: /Move .* later/ })).toBeNull();
  });

  it('refuses incomplete rules in the form', async () => {
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Make smart' }));
    await fireEvent.click(screen.getByLabelText('Movies'));
    const form = screen.getByRole('form', { name: 'Smart collection rules' });
    await fireEvent.click(form.querySelector('button[type="submit"]')!);
    expect(await screen.findByRole('alert')).toHaveTextContent('Choose movies, shows or both');
    expect(mockUpdateSettings).not.toHaveBeenCalled();
  });

  it('stops smart updates, keeping the members', async () => {
    mockGet.mockResolvedValue({ ...spooky, rules: { types: ['movie'] } });
    mockConfirm.mockResolvedValue(true);
    mockUpdateSettings.mockResolvedValue({ ...spooky });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop smart updates' }));
    await waitFor(() => expect(mockUpdateSettings).toHaveBeenCalledWith('c-1', { rules: null }));
    expect(await screen.findByRole('button', { name: 'Remove Scream' })).toBeInTheDocument();
  });
});

describe('Manual collection page — viewer', () => {
  beforeEach(() => signIn(false));

  it('shows the collection without any editing controls', async () => {
    render(Page);
    expect(await screen.findByText('Halloween')).toBeInTheDocument();
    expect(screen.getByText('For October')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    expect(screen.queryByLabelText('Sort by')).toBeNull();
    expect(screen.queryByRole('button', { name: /as the cover/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Remove/ })).toBeNull();
    expect(screen.queryByLabelText('Show on Home')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Upload cover' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Make smart' })).toBeNull();
  });
});
