// Pure helpers for the library settings page's "Seek-bar thumbnails" section.
// Kept out of the .svelte file so they can be unit-tested directly.

import type { LibraryTrickplayStatus } from '$lib/api';

// Library types whose items are videos the server can build seek-bar
// thumbnails for. Mirrors library.DefaultTrickplayEnabled on the server (the
// types that default to on); other types hide the section entirely.
const VIDEO_LIBRARY_TYPES = new Set(['movie', 'show', 'home_video', 'anime', 'cartoons']);

export function isVideoLibraryType(type: string | undefined | null): boolean {
  return !!type && VIDEO_LIBRARY_TYPES.has(type);
}

const fmt = (n: number) => n.toLocaleString('en-US');

// One-line progress summary, e.g. "412 of 1,030 videos have thumbnails ·
// 600 pending · 18 failed". Zero-valued pending/failed parts are omitted.
export function formatTrickplayProgress(s: LibraryTrickplayStatus | null | undefined): string {
  if (!s) return '';
  if (s.total <= 0) return 'No videos with a playable file yet.';
  if (s.done >= s.total) {
    return s.total === 1 ? 'The video has thumbnails.' : `All ${fmt(s.total)} videos have thumbnails.`;
  }
  const parts = [`${fmt(s.done)} of ${fmt(s.total)} videos have thumbnails`];
  if (s.pending > 0) parts.push(`${fmt(s.pending)} pending`);
  if (s.failed > 0) parts.push(`${fmt(s.failed)} failed`);
  return parts.join(' · ');
}

// Feedback after "Generate now".
export function trickplayQueuedMessage(queued: number): string {
  if (queued <= 0) {
    return 'Nothing new to queue: every video already has thumbnails or is waiting in the queue.';
  }
  return `Queued ${fmt(queued)} ${queued === 1 ? 'video' : 'videos'}. Thumbnails are generated one at a time in the background and pause while someone is transcoding.`;
}
