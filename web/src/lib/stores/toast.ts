import { writable } from 'svelte/store';

export type ToastType = 'error' | 'success' | 'info';

export interface Toast {
  id: string;
  message: string;
  type: ToastType;
  timeout: number;
}

const MAX_TOASTS = 5;

function createToastStore() {
  const { subscribe, update } = writable<Toast[]>([]);

  let counter = 0;

  function addToast(message: string, type: ToastType, timeout?: number) {
    const id = `toast-${Date.now()}-${++counter}`;
    const resolvedTimeout = timeout ?? (type === 'error' ? 8000 : 5000);

    update(toasts => {
      const next = [...toasts, { id, message, type, timeout: resolvedTimeout }];
      // Keep only the most recent MAX_TOASTS
      return next.length > MAX_TOASTS ? next.slice(next.length - MAX_TOASTS) : next;
    });

    setTimeout(() => removeToast(id), resolvedTimeout);

    return id;
  }

  function removeToast(id: string) {
    update(toasts => toasts.filter(t => t.id !== id));
  }

  return {
    subscribe,
    addToast,
    removeToast,
    success: (message: string, timeout?: number) => addToast(message, 'success', timeout),
    error: (message: string, timeout?: number) => addToast(message, 'error', timeout),
    info: (message: string, timeout?: number) => addToast(message, 'info', timeout),
  };
}

export const toast = createToastStore();
