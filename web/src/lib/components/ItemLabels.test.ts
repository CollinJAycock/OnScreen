import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ItemLabels from './ItemLabels.svelte';
import LabelPicker from './LabelPicker.svelte';

const mockList = vi.hoisted(() => vi.fn());
const mockSetItem = vi.hoisted(() => vi.fn());
const mockApply = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('$lib/api')>()),
  labelApi: { list: mockList, setItem: mockSetItem, apply: mockApply },
}));

beforeEach(() => {
  vi.clearAllMocks();
  mockList.mockResolvedValue([{ label: 'kids', item_count: 4 }, { label: 'horror', item_count: 2 }]);
  mockSetItem.mockImplementation(async (_id: string, labels: string[]) => labels);
  mockApply.mockResolvedValue({ added: 2, removed: 0 });
});

describe('ItemLabels', () => {
  it('shows the item labels as chips', () => {
    render(ItemLabels, { itemId: 'item-1', labels: ['kids', 'war'] });
    expect(screen.getByText('kids')).toBeTruthy();
    expect(screen.getByText('war')).toBeTruthy();
  });

  it('adds a typed label normalised, on Enter, and saves the sorted list', async () => {
    render(ItemLabels, { itemId: 'item-1', labels: ['war'] });
    const box = screen.getByLabelText('Labels: add') as HTMLInputElement;
    await fireEvent.focus(box);
    await waitFor(() => expect(mockList).toHaveBeenCalled());
    await fireEvent.input(box, { target: { value: '  Scary   Stuff ' } });
    await fireEvent.keyDown(box, { key: 'Enter' });
    await waitFor(() => expect(mockSetItem).toHaveBeenCalledWith('item-1', ['scary stuff', 'war']));
    expect(await screen.findByText('scary stuff')).toBeTruthy();
  });

  it('removes a label with its × button', async () => {
    render(ItemLabels, { itemId: 'item-2', labels: ['kids', 'war'] });
    await fireEvent.click(screen.getByRole('button', { name: 'Remove label kids' }));
    await waitFor(() => expect(mockSetItem).toHaveBeenCalledWith('item-2', ['war']));
  });

  it('restores the chips and shows the error when saving fails', async () => {
    mockSetItem.mockRejectedValue(new Error('at most 50 labels'));
    render(ItemLabels, { itemId: 'item-3', labels: ['kids'] });
    await fireEvent.click(screen.getByRole('button', { name: 'Remove label kids' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('at most 50 labels');
    expect(screen.getByText('kids')).toBeTruthy();
  });
});

describe('LabelPicker', () => {
  it('adds the chosen labels to every selected item', async () => {
    render(LabelPicker, { open: true, mediaItemIds: ['a', 'b'] });
    await waitFor(() => expect(mockList).toHaveBeenCalled());
    const add = screen.getByRole('button', { name: 'Add' }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    const box = screen.getByLabelText('Labels to apply: add');
    await fireEvent.input(box, { target: { value: 'Kids' } });
    await fireEvent.keyDown(box, { key: 'Enter' });
    expect(add.disabled).toBe(false);
    await fireEvent.click(add);
    await waitFor(() => expect(mockApply).toHaveBeenCalledWith(['a', 'b'], { add: ['kids'] }));
    expect(await screen.findByRole('status')).toHaveTextContent('Labelled 2 of 2 items');
  });

  it('removes the chosen labels', async () => {
    mockApply.mockResolvedValue({ added: 0, removed: 1 });
    render(LabelPicker, { open: true, mediaItemIds: ['a'] });
    const box = screen.getByLabelText('Labels to apply: add');
    await fireEvent.input(box, { target: { value: 'horror' } });
    await fireEvent.keyDown(box, { key: 'Enter' });
    await fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(mockApply).toHaveBeenCalledWith(['a'], { remove: ['horror'] }));
  });

  it('refuses a selection over the 500-item cap', async () => {
    const ids = Array.from({ length: 501 }, (_, i) => `id-${i}`);
    render(LabelPicker, { open: true, mediaItemIds: ids });
    expect(screen.getByText(/at most 500 items/)).toBeTruthy();
  });
});
