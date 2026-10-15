// Multi-episode files: an "S01E01-E02" file is one episode item with
// index 1 and index_end 2 (server v2.6+; older servers never send
// index_end). Pure helpers for showing the range and for stepping to the
// episode before / after one — unit-tested in episodeRange.test.ts.

/** The last episode an item covers: index_end when it's a range, else index. */
export function lastEpisode(index?: number | null, indexEnd?: number | null): number | null {
  if (index == null) return null;
  return indexEnd != null && indexEnd > index ? indexEnd : index;
}

/** "1" or "1–2". "" when there's no number. */
export function episodeNumberLabel(index?: number | null, indexEnd?: number | null): string {
  if (index == null) return '';
  const last = lastEpisode(index, indexEnd)!;
  return last > index ? `${index}–${last}` : `${index}`;
}

/** "Episode 3" or "Episodes 1–2". "" when there's no number. */
export function episodeHeading(index?: number | null, indexEnd?: number | null): string {
  if (index == null) return '';
  const last = lastEpisode(index, indexEnd)!;
  return last > index ? `Episodes ${index}–${last}` : `Episode ${index}`;
}

/** "E5" or "E1–E2" (the episode half of "S1 · E1–E2"). "" when there's no number. */
export function episodeShortCode(index?: number | null, indexEnd?: number | null): string {
  if (index == null) return '';
  const last = lastEpisode(index, indexEnd)!;
  return last > index ? `E${index}–E${last}` : `E${index}`;
}

interface Numbered {
  index?: number | null;
  index_end?: number | null;
}

/**
 * The sibling that plays after `item` in its season: the one numbered right
 * after the item's last episode, so after an E01–E02 file comes E03. null
 * when there is none (the same rule as for single episodes: a gap stops
 * auto-play).
 */
export function nextEpisode<T extends Numbered>(siblings: T[], item: Numbered): T | null {
  const last = lastEpisode(item.index, item.index_end);
  if (last == null) return null;
  return siblings.find((s) => s.index != null && s.index === last + 1) ?? null;
}

/**
 * The sibling that plays before `item`: the one covering the episode right
 * before it, so before E03 comes an E01–E02 file. null when there is none.
 */
export function previousEpisode<T extends Numbered>(siblings: T[], item: Numbered): T | null {
  if (item.index == null || item.index <= 1) return null;
  const want = item.index - 1;
  return (
    siblings.find((s) => {
      const last = lastEpisode(s.index, s.index_end);
      return s.index != null && last != null && s.index <= want && want <= last;
    }) ?? null
  );
}
