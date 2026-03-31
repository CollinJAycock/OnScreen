import { writable, derived } from 'svelte/store';
import { notificationApi, type Notification } from '$lib/api';

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
  eventSource = new EventSource('/api/v1/notifications/stream');

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
