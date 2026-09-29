import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockList = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockRename = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  photoAlbumApi: { list: mockList, create: mockCreate, rename: mockRename, delete: mockDelete },
  assetUrl: (p: string) => `http://srv${p}`,
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

import Page from './+page.svelte';

const albums = [
  { id: 'a-1', name: 'Alps', item_count: 4, cover_path: 'Photos/alps.jpg', created_at: '', updated_at: '' },
  { id: 'a-2', name: 'Beach', item_count: 1, created_at: '', updated_at: '' },
];

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockList.mockResolvedValue(albums.map((a) => ({ ...a })));
});

async function openMenu(name: string) {
  await fireEvent.click(await screen.findByRole('button', { name: `More actions for ${name}` }));
}

describe('Photo albums page', () => {
  it('lists the caller\'s albums with cover and count', async () => {
    render(Page);
    const alps = await screen.findByRole('link', { name: 'Alps, 4 photos' });
    expect(alps.getAttribute('href')).toBe('/photos/albums/a-1');
    expect(alps.querySelector('img')?.getAttribute('src')).toBe('http://srv/artwork/Photos/alps.jpg?w=300');
    const beach = screen.getByRole('link', { name: 'Beach, 1 photo' });
    expect(beach.querySelector('img')).toBeNull();
  });

  it('explains the empty state', async () => {
    mockList.mockResolvedValue([]);
    render(Page);
    expect(await screen.findByText('No albums yet')).toBeTruthy();
  });

  it('creates an album and puts it first', async () => {
    mockCreate.mockResolvedValue({ id: 'a-3', name: 'City', created_at: '', updated_at: '' });
    render(Page);
    await screen.findByRole('link', { name: 'Alps, 4 photos' });

    await fireEvent.click(screen.getByRole('button', { name: '+ New album' }));
    const input = screen.getByLabelText('Album name');
    await fireEvent.input(input, { target: { value: '  City  ' } });
    await fireEvent.submit(input.closest('form')!);

    await waitFor(() => expect(mockCreate).toHaveBeenCalledWith('City'));
    const first = (await screen.findAllByRole('link', { name: /photo/ }))[0];
    expect(first.getAttribute('aria-label')).toBe('City, 0 photos');
    expect(mockToastSuccess).toHaveBeenCalledWith('Created "City"');
  });

  it('won\'t create an album with a blank name', async () => {
    render(Page);
    await screen.findByRole('link', { name: 'Alps, 4 photos' });
    await fireEvent.click(screen.getByRole('button', { name: '+ New album' }));
    const input = screen.getByLabelText('Album name');
    await fireEvent.input(input, { target: { value: '   ' } });
    expect((screen.getByRole('button', { name: 'Create' }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.submit(input.closest('form')!);
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it('renames inline and keeps the count', async () => {
    mockRename.mockResolvedValue({ id: 'a-1', name: 'Alps 2024', created_at: '', updated_at: 'x' });
    render(Page);
    await openMenu('Alps');
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Rename' }));

    const input = screen.getByLabelText('Album name') as HTMLInputElement;
    expect(input.value).toBe('Alps');
    await fireEvent.input(input, { target: { value: 'Alps 2024' } });
    await fireEvent.submit(input.closest('form')!);

    await waitFor(() => expect(mockRename).toHaveBeenCalledWith('a-1', 'Alps 2024'));
    expect(await screen.findByRole('link', { name: 'Alps 2024, 4 photos' })).toBeTruthy();
  });

  it('cancels a rename with Escape', async () => {
    render(Page);
    await openMenu('Beach');
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Rename' }));
    const input = screen.getByLabelText('Album name');
    await fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByLabelText('Album name')).toBeNull();
    expect(mockRename).not.toHaveBeenCalled();
  });

  it('reports a failed rename and keeps the old name', async () => {
    mockRename.mockRejectedValue(new Error('Not found'));
    render(Page);
    await openMenu('Alps');
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Rename' }));
    const input = screen.getByLabelText('Album name');
    await fireEvent.input(input, { target: { value: 'New' } });
    await fireEvent.submit(input.closest('form')!);
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('Not found'));
  });

  it('deletes after confirming', async () => {
    mockDelete.mockResolvedValue(undefined);
    const confirmSpy = vi.fn().mockReturnValueOnce(false).mockReturnValueOnce(true);
    vi.stubGlobal('confirm', confirmSpy);
    render(Page);

    await openMenu('Beach');
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));
    expect(mockDelete).not.toHaveBeenCalled();

    await openMenu('Beach');
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith('a-2'));
    await waitFor(() => expect(screen.queryByRole('link', { name: 'Beach, 1 photo' })).toBeNull());
    expect(mockToastSuccess).toHaveBeenCalledWith('Deleted "Beach"');
    expect(confirmSpy).toHaveBeenCalledTimes(2);
    vi.unstubAllGlobals();
  });

  it('shows a load failure with retry', async () => {
    mockList.mockRejectedValueOnce(new Error('HTTP 500'));
    render(Page);
    expect((await screen.findByRole('alert')).textContent).toContain('HTTP 500');
    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('link', { name: 'Alps, 4 photos' })).toBeTruthy();
  });
});
