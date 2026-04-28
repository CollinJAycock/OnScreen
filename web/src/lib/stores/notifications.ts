import { writable, derived } from 'svelte/store';
import { notificationApi, assetUrl, type Notification } from '$lib/api';

export const notifications = writable<Notification[]>([]);
export const unreadCount = derived(notifications, ($n) => $n.filter((x) => !x.read).length);

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

export function initNotifications() {
  loadInitial();
  connectSSE();
}

export function stopNotifications() {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  notifications.set([]);
}

async function loadInitial() {
  try {
    const items = await notificationApi.list(30, 0);
    notifications.set(items);
  } catch {
    /* ignore — user might not be authenticated yet */
  }
}

function connectSSE() {
  if (eventSource) eventSource.close();
  // EventSource can't attach an Authorization header — same problem
  // as <img>/<audio> tags. Route through assetUrl so the bearer
  // lands as `?token=<paseto>` for cross-origin builds; same-origin
  // browser builds get the path back unchanged and rely on the
  // httpOnly cookie like before.
  eventSource = new EventSource(assetUrl('/api/v1/notifications/stream'));

  eventSource.onmessage = (e) => {
    try {
      const notif: Notification = JSON.parse(e.data);
      notifications.update((prev) => [notif, ...prev].slice(0, 50));
    } catch { /* bad payload */ }
  };

  eventSource.onerror = () => {
    eventSource?.close();
    eventSource = null;
    reconnectTimer = setTimeout(connectSSE, 5000);
  };
}

export async function markRead(id: string) {
  await notificationApi.markRead(id);
  notifications.update((prev) => prev.map((n) => (n.id === id ? { ...n, read: true } : n)));
}

export async function markAllRead() {
  await notificationApi.markAllRead();
  notifications.update((prev) => prev.map((n) => ({ ...n, read: true })));
}
