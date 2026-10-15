import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ItemMetadataEditor from './ItemMetadataEditor.svelte';

const mockGet = vi.hoisted(() => vi.fn());
const mockEdit = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  itemApi: { get: mockGet, editMetadata: mockEdit },
}));

const show = {
  id: 'show-1',
  library_id: 'lib',
  title: 'Andor',
  sort_title: 'Andor',
  type: 'show',
  year: 2022,
  summary: 'A rebel.',
  genres: ['Sci-Fi'],
  content_rating: 'TV-14',
  locked_fields: ['summary'],
  view_offset_ms: 0,
  updated_at: 0,
  is_favorite: false,
  files: [],
};

function saveButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: /^Save|Saving/ }) as HTMLButtonElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGet.mockResolvedValue(structuredClone(show));
});

describe('ItemMetadataEditor', () => {
  it('loads the item, shows its lock state and saves only the changes', async () => {
    const onsaved = vi.fn();
    mockEdit.mockResolvedValue({ id: 'show-1', title: 'Andor', locked_fields: ['genres', 'summary'] });
    render(ItemMetadataEditor, { itemId: 'show-1', open: true, onsaved });

    const genres = (await screen.findByLabelText('Genres')) as HTMLInputElement;
    expect(mockGet).toHaveBeenCalledWith('show-1');
    expect(genres.value).toBe('Sci-Fi');
    expect(screen.getByRole('button', { name: /Summary is locked/ })).toBeTruthy();
    expect(saveButton().disabled).toBe(true);

    await fireEvent.input(genres, { target: { value: 'Sci-Fi, Thriller' } });
    expect(screen.getByRole('button', { name: /Genres is edited and will be locked/ })).toBeTruthy();
    await fireEvent.click(saveButton());

    await waitFor(() => expect(mockEdit).toHaveBeenCalledWith('show-1', { genres: ['Sci-Fi', 'Thriller'] }));
    await waitFor(() => expect(onsaved).toHaveBeenCalled());
  });

  it('unlocks a locked field without editing it', async () => {
    mockEdit.mockResolvedValue({ id: 'show-1', title: 'Andor', locked_fields: [] });
    render(ItemMetadataEditor, { itemId: 'show-1', open: true });

    await fireEvent.click(await screen.findByRole('button', { name: /Summary is locked/ }));
    expect(screen.getByRole('button', { name: /Summary unlocks when you save/ })).toBeTruthy();
    await fireEvent.click(saveButton());

    await waitFor(() => expect(mockEdit).toHaveBeenCalledWith('show-1', { unlock_fields: ['summary'] }));
  });

  it('blocks an invalid year and reports a save conflict', async () => {
    mockEdit.mockRejectedValue(Object.assign(new Error('conflict'), { status: 409 }));
    render(ItemMetadataEditor, { itemId: 'show-1', open: true });

    const year = (await screen.findByLabelText('Year')) as HTMLInputElement;
    await fireEvent.input(year, { target: { value: '22' } });
    expect(screen.getByRole('alert').textContent).toMatch(/four-digit/);
    expect(saveButton().disabled).toBe(true);

    await fireEvent.input(year, { target: { value: '2023' } });
    await fireEvent.click(saveButton());
    await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/already has this title and year/));
  });
});
