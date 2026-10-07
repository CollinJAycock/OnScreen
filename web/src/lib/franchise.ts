// Pure helpers for franchise (TMDB collection) UI: the library Collections
// tab, the collection page's owned/missing parts, and the movie page's
// "Part of the <Name>" shelf. Kept framework-free so they unit-test cheaply.
import { collectionPosterUrl, type CollectionItem, type FranchisePart, type LibraryCollection } from '$lib/api';

export type PartState = 'owned' | 'requested' | 'missing';

/** Owned = in the server and visible; requested = missing with an open
 *  request from this user; missing = requestable. */
export function partState(p: FranchisePart): PartState {
  if (p.item_id) return 'owned';
  if (p.request_status) return 'requested';
  return 'missing';
}

/** Button/badge label for a part that already has an open request. */
export function requestStatusLabel(status: FranchisePart['request_status'] | string | null | undefined): string {
  switch (status) {
    case 'approved':
      return 'Approved';
    case 'downloading':
      return 'Downloading';
    case 'pending':
      return 'Requested';
    default:
      return '';
  }
}

/** Mirror a just-created request into the parts list so the row flips to
 *  its requested state without a reload. Only an open status sticks —
 *  anything else (a declined/failed echo) leaves the row requestable. */
export function applyRequestResult(parts: FranchisePart[], tmdbId: number, status: string): FranchisePart[] {
  const open = status === 'pending' || status === 'approved' || status === 'downloading';
  if (!open) return parts;
  return parts.map((p) =>
    p.tmdb_id === tmdbId && !p.item_id ? { ...p, request_status: status as FranchisePart['request_status'] } : p,
  );
}

/** A part as the server sends it. item_ids lists every visible copy of an
 *  owned film, item_id first (absent on older servers) — the same film in
 *  two libraries (4K + 1080p) is one part, while the collection's items
 *  listing returns each copy. */
export type FranchisePartCopies = FranchisePart & { item_ids?: string[] };

/** Every item id that is this part's film: item_ids, else just item_id
 *  (older servers). Empty for a missing part. */
export function partItemIds(p: FranchisePartCopies): string[] {
  if (p.item_ids && p.item_ids.length > 0) return p.item_ids;
  return p.item_id ? [p.item_id] : [];
}

/** The collection page's rows. Parts come from the stored TMDB snapshot; an
 *  owned item the snapshot doesn't list (TMDB's parts list lagging, or no
 *  snapshot yet) is appended so nothing the user owns disappears. Another
 *  copy of a listed film (same TMDB id, another library) is not appended —
 *  it is the same row. */
export function collectionRows(parts: FranchisePartCopies[] | undefined, items: CollectionItem[]): FranchisePart[] {
  const rows = [...(parts ?? [])];
  const listed = new Set(rows.flatMap(partItemIds));
  for (const it of items) {
    if (listed.has(it.id)) continue;
    rows.push({
      tmdb_id: 0,
      title: it.title,
      year: it.year,
      item_id: it.id,
      request_status: null,
    });
  }
  return rows;
}

/** Local artwork path of an owned part, when the items listing has one
 *  (from the linked copy, else any other copy). */
export function ownedPosterPath(p: FranchisePartCopies, items: CollectionItem[]): string | undefined {
  for (const id of partItemIds(p)) {
    const path = items.find((it) => it.id === id)?.poster_path;
    if (path) return path;
  }
  return undefined;
}

/** The movie page shelf: the franchise's OTHER films, owned first (each
 *  group keeps release order). The current film is left out whichever of
 *  its copies is open — the part may link the other library's copy. */
export function shelfParts<P extends FranchisePartCopies>(parts: P[], currentItemId: string): P[] {
  const others = parts.filter((p) => !partItemIds(p).includes(currentItemId));
  return [...others.filter((p) => !!p.item_id), ...others.filter((p) => !p.item_id)];
}

/** "Part of the Alien Collection" — TMDB names almost always end in
 *  "Collection"; the article reads naturally either way. */
export function partOfHeading(name: string): string {
  return `Part of the ${name}`;
}

/** Cover for a library-tab card: an admin-uploaded cover, else the TMDB
 *  collection poster when known, else the chosen or first visible member's
 *  artwork (as an /artwork path), else none. */
export function collectionCover(
  c: Pick<LibraryCollection, 'poster_url' | 'poster_path'> & Partial<Pick<LibraryCollection, 'id' | 'poster_version'>>,
): { url?: string; artwork?: string } {
  if (c.id && c.poster_version != null) {
    const url = collectionPosterUrl({ id: c.id, poster_version: c.poster_version });
    if (url) return { url };
  }
  if (c.poster_url) return { url: c.poster_url };
  if (c.poster_path) return { artwork: c.poster_path };
  return {};
}

/** "2 of 4 films" / "3 films" for a collection summary line. */
export function ownedSummary(owned: number, total: number): string {
  if (total > owned) return `${owned} of ${total} films`;
  return `${owned} film${owned === 1 ? '' : 's'}`;
}
