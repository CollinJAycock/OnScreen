import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import type { Notification } from '$lib/api';

const mockGoto = vi.hoisted(() => vi.fn());
const mockMarkRead = vi.hoisted(() => vi.fn());
const store = vi.hoisted(() => ({ list: null as unknown }));

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/stores/notifications', async () => {
  const { writable: w } = await import('svelte/store');
  const notifications = w<Notification[]>([]);
  store.list = notifications;
  return { notifications, markRead: mockMarkRead, markAllRead: vi.fn() };
});

import NotificationPanel from './NotificationPanel.svelte';

function n(over: Partial<Notification> & Pick<Notification, 'id' | 'type' | 'title'>): Notification {
  return { body: '', read: true, created_at: Date.now(), ...over };
}

function setList(items: Notification[]) {
  (store.list as ReturnType<typeof writable<Notification[]>>).set(items);
}

beforeEach(() => {
  vi.clearAllMocks();
  mockMarkRead.mockResolvedValue(undefined);
});

describe('NotificationPanel — request notices', () => {
  it('gives request notices their own icons', () => {
    setList([
      n({ id: '1', type: 'request_pending', title: 'New request' }),
      n({ id: '2', type: 'request_declined', title: 'Request declined: Heat' }),
      n({ id: '3', type: 'request_available', title: 'Now available: Dune', item_id: 'item-9' }),
    ]);
    const { container } = render(NotificationPanel);
    const kinds = [...container.querySelectorAll('.icon')].map((el) => el.getAttribute('data-kind'));
    expect(kinds).toEqual(['request_pending', 'request_problem', 'request_available']);
  });

  it('opens the Requests page for a pending-request alert and marks it read', async () => {
    setList([n({ id: '1', type: 'request_pending', title: 'New request', read: false })]);
    render(NotificationPanel);
    await fireEvent.click(screen.getByText('New request'));
    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/requests'));
    expect(mockMarkRead).toHaveBeenCalledWith('1');
  });

  it('opens the title for a fulfilled request', async () => {
    setList([n({ id: '3', type: 'request_available', title: 'Now available: Dune', item_id: 'item-9' })]);
    render(NotificationPanel);
    await fireEvent.click(screen.getByText('Now available: Dune'));
    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/watch/item-9'));
  });

  it('does nothing on click for a notice without a destination', async () => {
    setList([n({ id: '4', type: 'scan_complete', title: 'Library scan complete' })]);
    render(NotificationPanel);
    await fireEvent.click(screen.getByText('Library scan complete'));
    expect(mockGoto).not.toHaveBeenCalled();
  });
});
