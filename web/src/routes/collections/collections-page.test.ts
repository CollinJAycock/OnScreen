import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockList = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockCreateManual = vi.hoisted(() => vi.fn());
const mockGoto = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  collectionApi: { list: mockList, create: mockCreate, createManual: mockCreateManual, delete: vi.fn() },
  assetUrl: (p: string) => `http://srv${p}`,
}));

function signIn(isAdmin: boolean) {
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: 'u1', username: 'u', is_admin: isAdmin }));
}

beforeEach(() => {
  vi.clearAllMocks();
  signIn(false);
});

afterEach(() => {
  localStorage.clear();
});

describe('Collections page', () => {
  it('opts in to franchise collections and shows them under Film series', async () => {
    mockList.mockResolvedValue([
      { id: 'g1', name: 'Action', type: 'auto_genre', created_at: '' },
      { id: 'f1', name: 'Alien Collection', type: 'franchise', created_at: '' },
    ]);
    render(Page);
    expect(await screen.findByText('Film series')).toBeInTheDocument();
    expect(screen.getByText('Alien Collection')).toBeInTheDocument();
    // GET /collections leaves franchises out unless asked (native home
    // screens render every collection as a row).
    expect(mockList).toHaveBeenCalledWith({ includeFranchise: true });
  });

  it('shows shared collections first, with their cover and count', async () => {
    mockList.mockResolvedValue([
      { id: 'p1', name: 'My list', type: 'playlist', created_at: '' },
      { id: 'c1', name: 'Spooky', type: 'manual', poster_path: 'M/halloween.jpg', item_count: 4, created_at: '' },
      { id: 'c2', name: 'Empty one', type: 'manual', item_count: 1, created_at: '' },
    ]);
    render(Page);
    const heading = await screen.findByRole('heading', { name: 'Collections', level: 2 });
    const playlists = screen.getByRole('heading', { name: 'Playlists' });
    expect(heading.compareDocumentPosition(playlists) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const spooky = screen.getByRole('link', { name: /Spooky/ });
    expect(spooky.getAttribute('href')).toBe('/collections/c1');
    expect(spooky.querySelector('img')?.getAttribute('src')).toBe('http://srv/artwork/M/halloween.jpg?w=300');
    expect(screen.getByText('4 titles')).toBeInTheDocument();
    expect(screen.getByText('1 title')).toBeInTheDocument();
  });

  it('offers New Collection to admins only', async () => {
    mockList.mockResolvedValue([]);
    render(Page);
    await screen.findByRole('button', { name: '+ New Playlist' });
    expect(screen.queryByRole('button', { name: '+ New Collection' })).toBeNull();
  });

  it('creates a shared collection and opens it', async () => {
    signIn(true);
    mockList.mockResolvedValue([]);
    mockCreateManual.mockResolvedValue({ id: 'c9', name: 'Halloween night', type: 'manual', created_at: '' });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: '+ New Collection' }));
    await fireEvent.input(screen.getByLabelText('Collection name'), { target: { value: 'Halloween night' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(mockCreateManual).toHaveBeenCalledWith('Halloween night'));
    expect(mockCreate).not.toHaveBeenCalled();
    expect(mockGoto).toHaveBeenCalledWith('/collections/c9');
  });

  it('still creates playlists', async () => {
    signIn(true);
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue({ id: 'p9', name: 'Road trip', type: 'playlist', created_at: '' });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: '+ New Playlist' }));
    await fireEvent.input(screen.getByLabelText('Playlist name'), { target: { value: 'Road trip' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(mockCreate).toHaveBeenCalledWith('Road trip'));
    expect(mockCreateManual).not.toHaveBeenCalled();
  });
});
