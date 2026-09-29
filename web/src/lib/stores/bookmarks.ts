import { writable } from 'svelte/store';
import type { Bookmark } from '$lib/api';

// The bookmark the audio player added last, with its book, so an open
// audiobook page can list it without re-reading the book's bookmarks. The
// page inserts by id, so the replayed last value on subscribe is harmless.
export const bookmarkAdded = writable<{ bookId: string; bookmark: Bookmark } | null>(null);
