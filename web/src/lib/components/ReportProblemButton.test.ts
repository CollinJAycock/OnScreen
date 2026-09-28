import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ReportProblemButton from './ReportProblemButton.svelte';

const mockListMine = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  issuesApi: { listMine: mockListMine, create: mockCreate },
}));

const DAY = 86_400_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

async function openDialog() {
  await fireEvent.click(screen.getByRole('button', { name: 'Report a problem' }));
}

function radio(name: RegExp): HTMLInputElement {
  return screen.getByRole('radio', { name }) as HTMLInputElement;
}

function sendButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: /Send report|Sending/ }) as HTMLButtonElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockListMine.mockResolvedValue([]);
});

describe('ReportProblemButton', () => {
  it('lists the kinds with friendly labels and needs one chosen before sending', async () => {
    render(ReportProblemButton, { itemId: 'item-1', itemLabel: 'Heat (1995)' });
    await openDialog();
    await waitFor(() => expect(mockListMine).toHaveBeenCalledWith('item-1'));

    expect(screen.getByRole('heading', { name: 'Report a problem' })).toBeTruthy();
    expect(screen.getByText('Heat (1995)')).toBeTruthy();
    for (const label of [
      /Video won't play or looks wrong/,
      /Audio problem/,
      /Subtitles problem/,
      /Wrong movie\/show/,
      /Something else/,
    ]) {
      expect(radio(label)).toBeTruthy();
    }
    expect(sendButton().disabled).toBe(true);
    await fireEvent.click(radio(/Audio problem/));
    expect(sendButton().disabled).toBe(false);
  });

  it('sends kind, trimmed note and the playing file, then thanks the user', async () => {
    mockCreate.mockResolvedValue({
      id: 'new', item_id: 'item-9', kind: 'subtitles', status: 'open', created_at: new Date().toISOString(),
    });
    render(ReportProblemButton, { itemId: 'item-9', fileId: 'file-3', variant: 'overlay' });
    await openDialog();
    await fireEvent.click(radio(/Subtitles problem/));
    const note = screen.getByLabelText(/Details/) as HTMLTextAreaElement;
    await fireEvent.input(note, { target: { value: '  out of sync after 20 min  ' } });
    expect(screen.getByText(/characters left/).textContent).toBe(`${1000 - 'out of sync after 20 min'.length} characters left`);
    await fireEvent.click(sendButton());

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith('item-9', {
        kind: 'subtitles',
        note: 'out of sync after 20 min',
        file_id: 'file-3',
      }),
    );
    const status = await screen.findByRole('status');
    expect(status.textContent).toContain('an admin has been notified');
  });

  it('omits an empty note and file', async () => {
    mockCreate.mockResolvedValue({ id: 'n', item_id: 'item-2', kind: 'other', status: 'open', created_at: new Date().toISOString() });
    render(ReportProblemButton, { itemId: 'item-2' });
    await openDialog();
    await fireEvent.click(radio(/Something else/));
    await fireEvent.click(sendButton());
    await waitFor(() => expect(mockCreate).toHaveBeenCalledWith('item-2', { kind: 'other' }));
  });

  it("shows the user's earlier reports and blocks re-reporting an open kind", async () => {
    mockListMine.mockResolvedValue([
      { id: 'a', item_id: 'item-1', kind: 'video', status: 'open', created_at: ago(2 * DAY + 60_000) },
      {
        id: 'b', item_id: 'item-1', kind: 'audio', status: 'resolved', created_at: ago(5 * DAY),
        resolved_at: ago(DAY + 60_000), resolution_note: 'Replaced the file',
      },
    ]);
    render(ReportProblemButton, { itemId: 'item-1' });
    await openDialog();

    const history = await screen.findByRole('list', { name: 'Your earlier reports' });
    expect(history.textContent).toContain("You reported “Video won't play or looks wrong” 2 days ago");
    expect(history.textContent).toContain('Resolved yesterday: “Audio problem” — “Replaced the file”');
    // The open kind can't be chosen again; the resolved one can.
    expect(radio(/Video won't play/).disabled).toBe(true);
    expect(screen.getByText('already reported')).toBeTruthy();
    expect(radio(/Audio problem/).disabled).toBe(false);
  });

  it('explains a duplicate report', async () => {
    mockCreate.mockRejectedValue(Object.assign(new Error('dup'), { status: 409, code: 'ALREADY_REPORTED' }));
    render(ReportProblemButton, { itemId: 'item-1' });
    await openDialog();
    await fireEvent.click(radio(/Audio problem/));
    await fireEvent.click(sendButton());
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('You already reported this problem');
    // History is refreshed so the kind shows as reported.
    await waitFor(() => expect(mockListMine).toHaveBeenCalledTimes(2));
  });

  it('still lets the user report when the history fails to load', async () => {
    mockListMine.mockRejectedValue(new Error('HTTP 500'));
    mockCreate.mockResolvedValue({ id: 'n', item_id: 'item-1', kind: 'video', status: 'open', created_at: new Date().toISOString() });
    render(ReportProblemButton, { itemId: 'item-1' });
    await openDialog();
    await waitFor(() => expect(screen.queryByText(/Checking your earlier reports/)).toBeNull());
    await fireEvent.click(radio(/Video won't play/));
    await fireEvent.click(sendButton());
    await screen.findByRole('status');
  });

  it('keeps typing inside the dialog away from page shortcuts', async () => {
    const onWindowKey = vi.fn();
    window.addEventListener('keydown', onWindowKey);
    try {
      render(ReportProblemButton, { itemId: 'item-1' });
      await openDialog();
      const note = screen.getByLabelText(/Details/);
      await fireEvent.keyDown(note, { key: ' ' });
      await fireEvent.keyDown(note, { key: 'f' });
      expect(onWindowKey).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener('keydown', onWindowKey);
    }
  });

  it('closes on Cancel', async () => {
    render(ReportProblemButton, { itemId: 'item-1' });
    await openDialog();
    expect(screen.getByRole('heading', { name: 'Report a problem' })).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Report a problem' })).toBeNull());
  });
});
