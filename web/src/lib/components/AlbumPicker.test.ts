import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import AlbumPicker from './AlbumPicker.svelte';

const mockList = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockAddItem = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  photoAlbumApi: { list: mockList, create: mockCreate, addItem: mockAddItem },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

const albums = [
  { id: 'a-1', name: 'Alps', item_count: 4, created_at: '', updated_at: '' },
  { id: 'a-2', name: 'Beach', item_count: 0, created_at: '', updated_at: '' },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockList.mockResolvedValue(albums);
  mockAddItem.mockResolvedValue(undefined);
});

function setup(mediaItemIds: string[]) {
  const onclose = vi.fn();
  const ondone = vi.fn();
  render(AlbumPicker, { open: true, mediaItemIds, onclose, ondone });
  return { onclose, ondone };
}

describe('AlbumPicker', () => {
  it('renders nothing while closed', () => {
    render(AlbumPicker, { open: false, mediaItemIds: ['p-1'], onclose: vi.fn() });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(mockList).not.toHaveBeenCalled();
  });

  it('lists the caller\'s albums and adds the photo to the one picked', async () => {
    const { onclose, ondone } = setup(['p-1']);
    expect(screen.getByRole('dialog', { name: 'Add photo to album' })).toBeTruthy();

    await fireEvent.click(await screen.findByRole('button', { name: /Alps/ }));

    await waitFor(() => expect(onclose).toHaveBeenCalled());
    expect(mockAddItem).toHaveBeenCalledWith('a-1', 'p-1');
    expect(mockToastSuccess).toHaveBeenCalledWith('Added to "Alps"');
    expect(ondone).toHaveBeenCalledWith(expect.objectContaining({ id: 'a-1', name: 'Alps' }));
  });

  it('adds every selected photo, once each', async () => {
    const { onclose } = setup(['p-1', 'p-2', 'p-1']);
    expect(screen.getByRole('dialog', { name: 'Add 2 photos to album' })).toBeTruthy();

    await fireEvent.click(await screen.findByRole('button', { name: /Beach/ }));

    await waitFor(() => expect(onclose).toHaveBeenCalled());
    expect(mockAddItem.mock.calls).toEqual([['a-2', 'p-1'], ['a-2', 'p-2']]);
    expect(mockToastSuccess).toHaveBeenCalledWith('Added 2 photos to "Beach"');
  });

  it('reports a partial failure as an error', async () => {
    mockAddItem.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('HTTP 500'));
    const { ondone } = setup(['p-1', 'p-2']);
    await fireEvent.click(await screen.findByRole('button', { name: /Alps/ }));

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('Added 1 of 2 photos to "Alps"; 1 failed: HTTP 500'));
    expect(ondone).toHaveBeenCalled();
  });

  it('creates a new album and adds to it', async () => {
    mockCreate.mockResolvedValue({ id: 'a-9', name: 'City', item_count: 0, created_at: '', updated_at: '' });
    const { onclose } = setup(['p-1']);

    await fireEvent.click(await screen.findByRole('button', { name: '+ New album' }));
    const input = screen.getByLabelText('New album name');
    const submit = screen.getByRole('button', { name: 'Create & add' }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    await fireEvent.input(input, { target: { value: '  City ' } });
    await fireEvent.submit(input.closest('form')!);

    await waitFor(() => expect(onclose).toHaveBeenCalled());
    expect(mockCreate).toHaveBeenCalledWith('City');
    expect(mockAddItem).toHaveBeenCalledWith('a-9', 'p-1');
    expect(mockToastSuccess).toHaveBeenCalledWith('Added to "City"');
  });

  it('keeps the dialog open when creating fails', async () => {
    mockCreate.mockRejectedValue(new Error('boom'));
    const { onclose } = setup(['p-1']);
    await fireEvent.click(await screen.findByRole('button', { name: '+ New album' }));
    const input = screen.getByLabelText('New album name');
    await fireEvent.input(input, { target: { value: 'City' } });
    await fireEvent.submit(input.closest('form')!);

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('boom'));
    expect(mockAddItem).not.toHaveBeenCalled();
    expect(onclose).not.toHaveBeenCalled();
  });

  it('explains an empty list and a failed load', async () => {
    mockList.mockResolvedValueOnce([]);
    setup(['p-1']);
    expect(await screen.findByText(/No albums yet/)).toBeTruthy();
  });

  it('shows a load error', async () => {
    mockList.mockRejectedValueOnce(new Error('HTTP 503'));
    setup(['p-1']);
    expect((await screen.findByRole('alert')).textContent).toContain('HTTP 503');
  });

  it('closes on Escape and on the close button', async () => {
    const { onclose } = setup(['p-1']);
    await screen.findByRole('button', { name: /Alps/ });
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onclose).toHaveBeenCalledTimes(1);
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onclose).toHaveBeenCalledTimes(2);
  });
});
