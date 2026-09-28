import { render, screen } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockList = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  collectionApi: { list: mockList, create: vi.fn(), delete: vi.fn() },
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: 'u1', username: 'u', is_admin: false }));
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
});
