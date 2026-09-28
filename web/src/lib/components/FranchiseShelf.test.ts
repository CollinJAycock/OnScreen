import { render, screen, waitFor } from '@testing-library/svelte';
import FranchiseShelf from './FranchiseShelf.svelte';

const mockGet = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  collectionApi: { get: mockGet },
}));

const collection = { id: 'col-1', name: 'Alien Collection', part_count: 4, owned_count: 2 };

beforeEach(() => {
  vi.clearAllMocks();
});

describe('FranchiseShelf', () => {
  it('lists the other films, owned first, with missing ones greyed', async () => {
    mockGet.mockResolvedValue({
      id: 'col-1',
      name: 'Alien Collection',
      type: 'franchise',
      created_at: '',
      parts: [
        { tmdb_id: 348, title: 'Alien', year: 1979, item_id: 'cur', request_status: null },
        { tmdb_id: 679, title: 'Aliens', year: 1986, item_id: null, request_status: 'pending' },
        { tmdb_id: 8077, title: 'Alien³', year: 1992, item_id: 'i-3', request_status: null },
        { tmdb_id: 8078, title: 'Alien Resurrection', year: 1997, item_id: null, request_status: null },
      ],
    });
    render(FranchiseShelf, { collection, currentItemId: 'cur' });

    expect(await screen.findByRole('heading', { name: 'Part of the Alien Collection' })).toBeTruthy();
    expect(mockGet).toHaveBeenCalledWith('col-1');

    const region = screen.getByRole('region', { name: 'Part of the Alien Collection' });
    const links = [...region.querySelectorAll('a.part')];
    expect(links.map((a) => a.querySelector('.title')?.textContent)).toEqual(['Alien³', 'Aliens', 'Alien Resurrection']);
    // Owned → its page; missing → the collection page to request it.
    expect(links[0].getAttribute('href')).toBe('/watch/i-3');
    expect(links[1].getAttribute('href')).toBe('/collections/col-1');
    expect(links[1].classList.contains('missing')).toBe(true);
    expect(links[1].textContent).toContain('Requested');
    expect(links[2].textContent).toContain('Not in library');
    expect(screen.getByRole('link', { name: 'See all' }).getAttribute('href')).toBe('/collections/col-1');
  });

  it('renders nothing when there is nothing else to show', async () => {
    mockGet.mockResolvedValue({
      id: 'col-1',
      name: 'Alien Collection',
      type: 'franchise',
      created_at: '',
      parts: [{ tmdb_id: 348, title: 'Alien', item_id: 'cur', request_status: null }],
    });
    render(FranchiseShelf, { collection, currentItemId: 'cur' });
    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole('heading')).toBeNull();
  });

  it('stays hidden when the collection cannot be loaded', async () => {
    mockGet.mockRejectedValue(new Error('HTTP 404'));
    render(FranchiseShelf, { collection, currentItemId: 'cur' });
    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole('heading')).toBeNull();
  });
});
