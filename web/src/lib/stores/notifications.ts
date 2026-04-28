import { writable, derived } from 'svelte/store';
import { notificationApi, assetUrl, type Notification } from '$lib/api';

export const notifications = writable<Notification[]>([]);
export const unreadCount = derived(notifications, ($n) => $n.filter((x) => !x.read).length);

/** Cross-device sync: shape of a `progress.updated` SSE event. The
 *  server publishes one of these every time the same user posts new
 *  progress on an item from any device, so other devices can refresh
 *  their resume position without polling. Subscribers filter on
 *  itemId before reacting — most subscribers care about a single
 *  item (e.g. the watch page they're rendering). */
export type ProgressUpdate = {
  itemId: string;
  positionMs: number;
  durationMs: number;
  state: 'playing' | 'paused' | 'stopped';
};

/** Pub-sub for sync events. Components on the watch/play surfaces
 *  subscribe and update their resume position when a matching event
 *  lands. Not a writable<state> because each event is a discrete
 *  update — keeping the most recent value would let stale events
 *  fire on late subscribers. */
export const progressUpdates = writable<ProgressUpdate | null>(null);

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
      const ev = JSON.parse(e.data) as Notification & {
        type: string;
        data?: { item_id: string; position_ms: number; duration_ms: number; state: ProgressUpdate['state'] };
      };
      // Sync events (cross-device resume-position broadcast) are
      // routed to a separate store — they aren't user-facing and
      // shouldn't pollute the bell-icon list. Other event types
      // are persisted notifications and land in the list.
      if (ev.type === 'progress.updated' && ev.data) {
        progressUpdates.set({
          itemId: ev.data.item_id,
          positionMs: ev.data.position_ms,
          durationMs: ev.data.duration_ms,
          state: ev.data.state,
        });
        return;
      }
      notifications.update((prev) => [ev, ...prev].slice(0, 50));
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
