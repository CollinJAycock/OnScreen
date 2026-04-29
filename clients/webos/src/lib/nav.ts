// Centralised routing for card clicks. Mirrors the Android Navigator —
// type-aware destination so callers don't have to special-case the
// types that don't drill into the standard /item detail page (photos
// go to a full-screen viewer; collections drill into their item grid).
//
// Keeping this in one place means adding a new type later is a one-
// file change instead of a sweep across hub / library / search /
// favorites / history.

import { goto } from '$app/navigation';

export function openItem(id: string, type: string) {
  switch (type) {
    case 'photo':
      goto(`/photo/${id}`);
      return;
    case 'collection':
    case 'playlist':
      goto(`/collection/${id}`);
      return;
    default:
      goto(`/item/${id}`);
  }
}
