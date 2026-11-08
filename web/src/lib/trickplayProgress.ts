// Pure helpers for the library settings page's "Seek-bar thumbnails" section.
// Kept out of the .svelte file so they can be unit-tested directly.
//
// Wording comes from the i18n catalog. The display helpers take the
// translator as an optional last argument: Svelte callers pass $i18n so the
// text re-renders when the language changes; it defaults to the current one.

import { get } from 'svelte/store';
import type { LibraryTrickplayStatus } from '$lib/api';
import { i18n, type Translator } from '$lib/i18n/index.svelte';

// Library types whose items are videos the server can build seek-bar
// thumbnails for. Mirrors library.DefaultTrickplayEnabled on the server (the
// types that default to on); other types hide the section entirely.
const VIDEO_LIBRARY_TYPES = new Set(['movie', 'show', 'home_video', 'anime', 'cartoons']);

export function isVideoLibraryType(type: string | undefined | null): boolean {
  return !!type && VIDEO_LIBRARY_TYPES.has(type);
}

// One-line progress summary, e.g. "412 of 1,030 videos have thumbnails ·
// 600 pending · 18 failed". Zero-valued pending/failed parts are omitted.
// Numbers are grouped in the interface language's style.
export function formatTrickplayProgress(
  s: LibraryTrickplayStatus | null | undefined,
  tx: Translator = get(i18n),
): string {
  if (!s) return '';
  if (s.total <= 0) return tx('lib.trickplayProgress.noVideos');
  if (s.done >= s.total) {
    return s.total === 1 ? tx('lib.trickplayProgress.oneDone') : tx('lib.trickplayProgress.allDone', { count: s.total });
  }
  const parts = [tx('lib.trickplayProgress.progress', { done: s.done, total: s.total })];
  if (s.pending > 0) parts.push(tx('lib.trickplayProgress.pending', { count: s.pending }));
  if (s.failed > 0) parts.push(tx('lib.trickplayProgress.failed', { count: s.failed }));
  return parts.join(' · ');
}

// Feedback after "Generate now".
export function trickplayQueuedMessage(queued: number, tx: Translator = get(i18n)): string {
  if (queued <= 0) return tx('lib.trickplayProgress.nothingQueued');
  return tx.n('lib.trickplayProgress.queued', queued);
}
