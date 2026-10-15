import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockPerson = vi.hoisted(() => vi.fn());
const mockItems = vi.hoisted(() => vi.fn());
const mockSuggestions = vi.hoisted(() => vi.fn());
const mockAssign = vi.hoisted(() => vi.fn());
const mockRename = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: readable({ url: new URL('http://localhost/photos/people/per-1'), params: { id: 'per-1' } }),
}));
vi.mock('$lib/api', () => ({
  photoPeopleApi: {
    person: mockPerson, personItems: mockItems, suggestions: mockSuggestions,
    assignFace: mockAssign, renamePerson: mockRename, deletePerson: vi.fn(),
  },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import Page from './+page.svelte';

const photo = (id: string) => ({ id, library_id: 'lib-1', title: id, poster_path: `x/${id}.jpg`, created_at: '', updated_at: '1' });

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockPerson.mockResolvedValue({ id: 'per-1', library_id: 'lib-1', name: 'Alice', photo_count: 2, can_edit: true });
  mockItems.mockResolvedValue({ items: [photo('p-1'), photo('p-2')], total: 2 });
  mockSuggestions.mockResolvedValue([{ id: 'f-9', item_id: 'p-9', x: 0.1, y: 0.1, w: 0.2, h: 0.2 }]);
  mockAssign.mockResolvedValue({});
});

describe('Person page', () => {
  it("lists the person's photos, opening them in the person's context", async () => {
    render(Page);
    expect(await screen.findByRole('heading', { name: 'Alice' })).toBeTruthy();
    expect(screen.getByText('2 photos')).toBeTruthy();
    const tiles = document.querySelectorAll('a.tile');
    expect(tiles[0].getAttribute('href')).toBe('/photos/p-1?person=per-1');
  });

  it('confirms a suggested face', async () => {
    mockItems
      .mockResolvedValueOnce({ items: [photo('p-1'), photo('p-2')], total: 2 })
      .mockResolvedValueOnce({ items: [photo('p-9'), photo('p-1'), photo('p-2')], total: 3 });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Yes, this is Alice' }));
    await waitFor(() => expect(mockAssign).toHaveBeenCalledWith('f-9', 'per-1'));
    await waitFor(() => expect(screen.getByText('3 photos')).toBeTruthy());
    expect(screen.queryByRole('button', { name: 'Yes, this is Alice' })).toBeNull();
  });

  it('shows no suggestions or edit actions to a viewer who may not name', async () => {
    mockPerson.mockResolvedValue({ id: 'per-1', library_id: 'lib-1', name: 'Alice', photo_count: 2, can_edit: false });
    render(Page);
    await screen.findByRole('heading', { name: 'Alice' });
    expect(mockSuggestions).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Rename' })).toBeNull();
  });
});
