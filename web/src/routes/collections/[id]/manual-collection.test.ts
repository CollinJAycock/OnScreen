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

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL('http://localhost/collections/c-1'), params: { id: 'c-1' } }).subscribe(fn),
  },
}));
vi.mock('$lib/native', () => ({ confirmAction: mockConfirm }));
vi.mock('$lib/api', () => ({
  collectionApi: {
    get: mockGet,
    items: mockItems,
    update: vi.fn(),
    updateSettings: mockUpdateSettings,
    reorder: mockReorder,
    removeItem: mockRemoveItem,
    delete: mockDelete,
  },
  assetUrl: (p: string) => p,
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
  });
});
