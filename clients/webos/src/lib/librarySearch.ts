// "Search this library" on the library page: the search page's rules (two
// characters, a 250 ms pause after the last key, only the newest search's
// answer shown) for one library (the server's library_id). Pure: the request
// and the clock are injected, so the ordering is unit-tested; the panel
// (components/LibrarySearch.svelte) only draws what it's told.

import type { SearchResult } from './api/types';
import { t, type MessageKey } from './i18n/index.svelte';
import { latestOnly } from './latest';

export const MIN_QUERY = 2;
export const SEARCH_DELAY_MS = 250;
export const SEARCH_LIMIT = 30;

const TYPE_LABELS: Readonly<Record<string, MessageKey>> = {
  movie: 'browse.typeMovie',
  show: 'browse.typeShow',
  season: 'browse.typeSeason',
  episode: 'browse.typeEpisode',
  artist: 'browse.typeArtist',
  album: 'browse.typeAlbum',
  track: 'browse.typeTrack',
  music_video: 'browse.typeMusicVideo',
  photo: 'browse.typePhoto',
  home_video: 'browse.typeHomeVideo',
  audiobook: 'browse.typeAudiobook',
  book: 'browse.typeBook',
  book_author: 'browse.typeBookAuthor',
  book_series: 'browse.typeBookSeries',
  podcast: 'browse.typePodcast',
  podcast_episode: 'browse.typePodcastEpisode',
  collection: 'browse.typeCollection',
  playlist: 'browse.typePlaylist',
};

/** A result's type in the app's language, for the line under a card with
 *  no year (search, Search this library, Discover). English is the type as
 *  the server sends it, as these screens always showed it; a type with no
 *  label shows as sent. */
export function resultTypeLabel(type: string): string {
  const key = Object.prototype.hasOwnProperty.call(TYPE_LABELS, type) ? TYPE_LABELS[type] : undefined;
  return key ? t(key) : type;
}

/** A result card's focus key (lib/focus/memory): apart from the grid's
 *  cards under the panel, which can be the same items. */
export function searchResultKey(id: string): string {
  return `search:${id}`;
}

/** endpoints.search.query's shape. */
export type SearchFn = (q: string, limit: number, libraryId: string) => Promise<SearchResult[]>;

export type SearchState =
  /** Under MIN_QUERY characters: nothing asked. */
  | { kind: 'idle' }
  | { kind: 'searching' }
  /** The newest search's answer (empty: nothing matched). */
  | { kind: 'results'; results: SearchResult[] }
  /** The newest search failed. */
  | { kind: 'failed'; error: unknown };

export interface LibrarySearchOptions {
  /** The library searched (read at each search). */
  libraryId: () => string;
  search: SearchFn;
  onstate: (s: SearchState) => void;
  delay?: number;
  /** setTimeout / clearTimeout, for the tests' clock. */
  schedule?: (fn: () => void, ms: number) => unknown;
  cancel?: (handle: unknown) => void;
}

export class LibrarySearch {
  private readonly searches = latestOnly();
  private timer: unknown = null;
  private query = '';
  private disposed = false;

  constructor(private readonly o: LibrarySearchOptions) {}

  /** The text changed (a key on the keyboard): search once typing pauses. */
  type(text: string): void {
    this.query = text;
    this.clearTimer();
    if (text.trim().length < MIN_QUERY) {
      // Nothing to show, and nothing still on its way may land on top.
      this.searches.supersede();
      this.o.onstate({ kind: 'idle' });
      return;
    }
    const schedule = this.o.schedule ?? ((fn: () => void, ms: number) => setTimeout(fn, ms));
    this.timer = schedule(() => {
      this.timer = null;
      void this.now();
    }, this.o.delay ?? SEARCH_DELAY_MS);
  }

  /** Search for the current text straight away (Back to the panel puts the
   *  last search up again). True when its results were applied. */
  async now(text: string = this.query): Promise<boolean> {
    this.query = text;
    this.clearTimer();
    const q = text.trim();
    if (q.length < MIN_QUERY || this.disposed) return false;
    this.o.onstate({ kind: 'searching' });
    try {
      return await this.searches(this.o.search(q, SEARCH_LIMIT, this.o.libraryId()), (r) => {
        if (!this.disposed) this.o.onstate({ kind: 'results', results: Array.isArray(r) ? r : [] });
      });
    } catch (error) {
      // Only the newest search's failure gets here (latestOnly).
      if (!this.disposed) this.o.onstate({ kind: 'failed', error });
      return false;
    }
  }

  /** The panel closed: no timer left, no late answer drawn. */
  dispose(): void {
    this.disposed = true;
    this.clearTimer();
    this.searches.supersede();
  }

  private clearTimer() {
    if (this.timer === null) return;
    const cancel = this.o.cancel ?? ((h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>));
    cancel(this.timer);
    this.timer = null;
  }
}
