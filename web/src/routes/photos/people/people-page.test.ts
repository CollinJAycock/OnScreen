import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockLibs = vi.hoisted(() => vi.fn());
const mockPeople = vi.hoisted(() => vi.fn());
const mockUnnamed = vi.hoisted(() => vi.fn());
const mockItemFaces = vi.hoisted(() => vi.fn());
const mockNameFace = vi.hoisted(() => vi.fn());
const mockDeleteFace = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: readable({ url: new URL('http://localhost/photos/people?library=lib-1'), params: {} }),
}));
vi.mock('$lib/api', () => ({
  libraryApi: { list: mockLibs },
  photoPeopleApi: {
    people: mockPeople, unnamedFaces: mockUnnamed, itemFaces: mockItemFaces,
    nameFace: mockNameFace, deleteFace: mockDeleteFace,
  },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: mockToastError } }));

import Page from './+page.svelte';

const face = (id: string, item = `p-${id}`) => ({ id, item_id: item, x: 0.2, y: 0.2, w: 0.2, h: 0.2, score: 9 });

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockLibs.mockResolvedValue([
    { id: 'lib-1', name: 'Family', type: 'photo', scan_paths: [] },
    { id: 'lib-m', name: 'Movies', type: 'movie', scan_paths: [] },
  ]);
  mockPeople.mockResolvedValue([
    { id: 'per-1', library_id: 'lib-1', name: 'Alice', photo_count: 3, cover: face('c1'), can_edit: true },
  ]);
  mockUnnamed.mockResolvedValue([face('f-1'), face('f-2')]);
});

describe('People page', () => {
  it('lists people and unnamed faces of the library', async () => {
    render(Page);
    const alice = await screen.findByText('Alice');
    expect(alice.closest('a')?.getAttribute('href')).toBe('/photos/people/per-1');
    expect(screen.getByText('3 photos')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: 'Name' })).toHaveLength(2);
    expect(mockPeople).toHaveBeenCalledWith('lib-1');
    // The face crop is drawn from the server render of the photo.
    const crop = screen.getAllByRole('link', { name: 'Open the photo' })[0];
    expect(crop.getAttribute('style')).toContain('/api/v1/items/p-f-1/image?w=800');
  });

  it('names an unnamed face into an existing person', async () => {
    mockNameFace.mockResolvedValue({ ...face('f-1'), person_id: 'per-1', person_name: 'Alice' });
    render(Page);
    await screen.findByText('Alice');
    await fireEvent.click(screen.getAllByRole('button', { name: 'Name' })[0]);
    const input = screen.getByPlaceholderText('Name');
    await fireEvent.input(input, { target: { value: 'Alice' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mockNameFace).toHaveBeenCalledWith('f-1', 'Alice'));
    await waitFor(() => expect(screen.getByText('4 photos')).toBeTruthy());
    expect(screen.getAllByRole('button', { name: 'Name' })).toHaveLength(1);
  });

  it('removes a box that is not a face', async () => {
    mockDeleteFace.mockResolvedValue(undefined);
    render(Page);
    await screen.findByText('Alice');
    await fireEvent.click(screen.getAllByRole('button', { name: 'Not a face' })[1]);
    await waitFor(() => expect(mockDeleteFace).toHaveBeenCalledWith('f-2'));
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Name' })).toHaveLength(1));
  });

  it('hides naming for a viewer who may not name faces', async () => {
    mockPeople.mockResolvedValue([]);
    mockItemFaces.mockResolvedValue({ faces: [], can_edit: false });
    render(Page);
    await waitFor(() => expect(mockItemFaces).toHaveBeenCalledWith('p-f-1'));
    expect(screen.getByText('Who is this?')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Name' })).toBeNull();
  });

  it('explains how faces get found when there are none', async () => {
    mockPeople.mockResolvedValue([]);
    mockUnnamed.mockResolvedValue([]);
    render(Page);
    expect(await screen.findByText('No faces yet')).toBeTruthy();
  });
});
