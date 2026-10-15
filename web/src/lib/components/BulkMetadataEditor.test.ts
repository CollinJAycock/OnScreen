import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const mockBulkEdit = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({
  itemApi: { bulkEdit: mockBulkEdit },
  labelApi: { list: vi.fn().mockResolvedValue([{ label: 'kids', item_count: 3 }]) },
  normalizeLabel: (s: string) => s.trim().split(/\s+/).join(' ').toLowerCase(),
}));

import BulkMetadataEditor from './BulkMetadataEditor.svelte';

const items = [
  { id: 'm1', type: 'movie', title: 'Alien' },
  { id: 'a1', type: 'album', title: 'Kind of Blue' },
  { id: 'p1', type: 'photo', title: 'Beach' },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockBulkEdit.mockResolvedValue({ matched: 3, updated: 2, labels_added: 0, labels_removed: 0, skipped: [] });
});

describe('BulkMetadataEditor', () => {
  it('starts with everything unchanged, so there is nothing to apply', async () => {
    render(BulkMetadataEditor, { props: { items, open: true } });
    const apply = (await screen.findByRole('button', { name: 'Apply to 3' })) as HTMLButtonElement;
    expect(apply.disabled).toBe(true);
  });

  it('says how many selected items a field applies to', async () => {
    render(BulkMetadataEditor, { props: { items, open: true } });
    await fireEvent.change(await screen.findByLabelText('Content rating change'), { target: { value: 'clear' } });
    expect(await screen.findByText(/Applies to 1 of 3/)).toBeTruthy();
    await fireEvent.change(screen.getByLabelText('Genre change'), { target: { value: 'replace' } });
    expect(await screen.findByText(/Applies to 2 of 3 selected/)).toBeTruthy();
  });

  it('applies a genre replace with a lock change and reports the result', async () => {
    const onapplied = vi.fn();
    render(BulkMetadataEditor, { props: { items, open: true, onapplied } });
    await fireEvent.change(await screen.findByLabelText('Genre change'), { target: { value: 'replace' } });
    await fireEvent.input(await screen.findByLabelText('Genres, comma-separated'), { target: { value: 'Jazz, jazz, Cool' } });
    await fireEvent.change(screen.getByLabelText('Summary lock'), { target: { value: 'unlock' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Apply to 3' }));
    await waitFor(() => expect(mockBulkEdit).toHaveBeenCalledWith({
      item_ids: ['m1', 'a1', 'p1'],
      set: { genres: ['Jazz', 'Cool'] },
      unlock: ['summary'],
    }));
    expect(await screen.findByText('Updated 2 of 3 items.')).toBeTruthy();
    expect(onapplied).toHaveBeenCalled();
  });

  it('blocks an add with no genres', async () => {
    render(BulkMetadataEditor, { props: { items, open: true } });
    await fireEvent.change(await screen.findByLabelText('Genre change'), { target: { value: 'add' } });
    expect(await screen.findByText('Enter at least one genre.')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Apply to 3' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('shows the server error', async () => {
    mockBulkEdit.mockRejectedValue(Object.assign(new Error('nothing to change'), { status: 400 }));
    render(BulkMetadataEditor, { props: { items, open: true } });
    await fireEvent.change(await screen.findByLabelText('Content rating change'), { target: { value: 'clear' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Apply to 3' }));
    expect(await screen.findByText(/nothing to change/)).toBeTruthy();
  });
});
