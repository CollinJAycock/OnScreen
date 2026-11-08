// Studios (movies) and networks (shows): links, labels, and the clear logo
// the movie / show detail hero shows in place of its text title.

import { get } from 'svelte/store';
import type { ItemDetail, StudioRef } from './api';
import { i18n, type Translator } from '$lib/i18n/index.svelte';

/** Where a studio's titles are listed; scoped to one library when given. */
export function studioHref(s: Pick<StudioRef, 'id'>, libraryId?: string): string {
  return libraryId ? `/studios/${s.id}?library_id=${encodeURIComponent(libraryId)}` : `/studios/${s.id}`;
}

/** "Studio" / "Network" (or the plural). Pass $i18n from a template so it
 *  follows a language change. */
export function studioKindLabel(kind: StudioRef['kind'], plural = false, tx: Translator = get(i18n)): string {
  if (kind === 'network') return tx(plural ? 'studios.networks' : 'studios.network');
  return tx(plural ? 'studios.studios' : 'studios.studio');
}

/** Which kind a library's titles have: networks for show libraries, studios
 *  for movie libraries, none for everything else. */
export function libraryStudioKind(libraryType: string | undefined): StudioRef['kind'] | null {
  switch (libraryType) {
    case 'movie':
      return 'studio';
    case 'show':
    case 'anime':
    case 'cartoons':
      return 'network';
  }
  return null;
}

/** The artwork path of the clear logo the detail hero shows instead of the
 *  text title: movies and shows only. Fetched at full size — a resize
 *  (?w=) re-encodes as JPEG and turns the transparent background black. */
export function heroLogoPath(item: Pick<ItemDetail, 'type' | 'logo_path'> | null | undefined): string | null {
  if (!item?.logo_path) return null;
  return item.type === 'movie' || item.type === 'show' ? item.logo_path : null;
}
