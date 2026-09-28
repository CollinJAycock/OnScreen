import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import PlayOnButton from './PlayOnButton.svelte';

const mockListDevices = vi.hoisted(() => vi.fn());
const mockTransfer = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  itemApi: { listDevices: mockListDevices, transfer: mockTransfer },
  getClientName: () => 'Web — Chrome on Windows',
}));

const recent = (minsAgo: number) => new Date(Date.now() - minsAgo * 60_000).toISOString();

async function openMenu() {
  await fireEvent.click(screen.getByRole('button', { name: 'Play on another device' }));
}

beforeEach(() => {
  vi.clearAllMocks();
  mockTransfer.mockResolvedValue(undefined);
});

describe('PlayOnButton', () => {
  it('lists other devices with last-seen times and excludes this browser', async () => {
    mockListDevices.mockResolvedValue([
      { client_name: 'Web — Chrome on Windows', last_seen: recent(0) },
      { client_name: 'Living Room TV', last_seen: recent(5) },
      { client_name: 'Pixel 8', last_seen: recent(180) },
    ]);
    render(PlayOnButton, { itemId: 'item-1' });
    await openMenu();

    await waitFor(() => expect(screen.getByRole('menuitem', { name: /Living Room TV/ })).toBeTruthy());
    const items = screen.getAllByRole('menuitem');
    expect(items.map((b) => b.querySelector('.device-name')?.textContent)).toEqual(['Living Room TV', 'Pixel 8']);
    expect(screen.queryByText('Web — Chrome on Windows')).toBeNull();
    expect(items[0].textContent).toContain('Last seen 5 min ago');
    expect(items[1].textContent).toContain('Last seen 3 hr ago');
  });

  it('transfers with the current position, confirms, and calls onsent', async () => {
    mockListDevices.mockResolvedValue([{ client_name: 'Living Room TV', last_seen: recent(1) }]);
    const onsent = vi.fn();
    render(PlayOnButton, { itemId: 'item-42', getPositionMs: () => 83_456.7, onsent, variant: 'overlay' });
    await openMenu();
    await fireEvent.click(await screen.findByRole('menuitem', { name: /Living Room TV/ }));

    await waitFor(() => expect(screen.getByRole('status').textContent).toBe('Sent to Living Room TV'));
    expect(mockTransfer).toHaveBeenCalledWith('item-42', 'Living Room TV', 83_457);
    expect(onsent).toHaveBeenCalledWith('Living Room TV');
    // Menu closes on success.
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('sends position 0 when no position getter is given', async () => {
    mockListDevices.mockResolvedValue([{ client_name: 'Pixel 8', last_seen: recent(1) }]);
    render(PlayOnButton, { itemId: 'item-7' });
    await openMenu();
    await fireEvent.click(await screen.findByRole('menuitem', { name: /Pixel 8/ }));
    await waitFor(() => expect(mockTransfer).toHaveBeenCalledWith('item-7', 'Pixel 8', 0));
  });

  it('shows the empty state when no other device has been seen', async () => {
    mockListDevices.mockResolvedValue([{ client_name: 'Web — Chrome on Windows', last_seen: recent(0) }]);
    render(PlayOnButton, { itemId: 'item-1' });
    await openMenu();
    await waitFor(() =>
      expect(
        screen.getByText('No other devices seen recently — open OnScreen on your TV or phone first.'),
      ).toBeTruthy(),
    );
    expect(screen.queryAllByRole('menuitem')).toHaveLength(0);
  });

  it('surfaces a device-list error inline with a retry', async () => {
    mockListDevices.mockRejectedValueOnce(new Error('HTTP 500'));
    render(PlayOnButton, { itemId: 'item-1' });
    await openMenu();
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain("Couldn't load devices: HTTP 500");

    mockListDevices.mockResolvedValueOnce([{ client_name: 'Pixel 8', last_seen: recent(2) }]);
    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.getByRole('menuitem', { name: /Pixel 8/ })).toBeTruthy());
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('surfaces a transfer error inline, keeps the menu open, and does not call onsent', async () => {
    mockListDevices.mockResolvedValue([{ client_name: 'Living Room TV', last_seen: recent(1) }]);
    mockTransfer.mockRejectedValueOnce(new Error('target_client_name required'));
    const onsent = vi.fn();
    render(PlayOnButton, { itemId: 'item-1', onsent });
    await openMenu();
    await fireEvent.click(await screen.findByRole('menuitem', { name: /Living Room TV/ }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain("Couldn't send to Living Room TV: target_client_name required");
    expect(screen.getByRole('menu')).toBeTruthy();
    expect(screen.queryByRole('status')).toBeNull();
    expect(onsent).not.toHaveBeenCalled();
  });

  it('keeps clicks from reaching the host (player overlay toggles play on click)', async () => {
    mockListDevices.mockResolvedValue([]);
    // host (e.g. the controls overlay) > mount target > component.
    const host = document.createElement('div');
    const target = document.createElement('div');
    host.appendChild(target);
    const hostClick = vi.fn();
    host.addEventListener('click', hostClick);
    document.body.appendChild(host);
    render(PlayOnButton, { target, props: { itemId: 'item-1', variant: 'overlay' } });
    await openMenu();
    expect(screen.getByRole('menu')).toBeTruthy();
    expect(hostClick).not.toHaveBeenCalled();
    host.remove();
  });

  it('closes on an outside click and on Escape', async () => {
    mockListDevices.mockResolvedValue([]);
    render(PlayOnButton, { itemId: 'item-1' });
    await openMenu();
    expect(screen.getByRole('menu')).toBeTruthy();
    await fireEvent.click(document.body);
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());

    await openMenu();
    expect(screen.getByRole('menu')).toBeTruthy();
    await fireEvent.keyDown(window, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
  });
});
