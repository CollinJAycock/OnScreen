import type { ChildItem, PodcastEpisode } from '$lib/api';

/**
 * Orders a podcast's episodes newest first: by release date (a subscribed
 * episode's publish date), then by index, then by title descending — which
 * keeps date-stamped file names ("2024-04-01 - Title") of hand-added
 * episodes in publication order too. Undated episodes sort after dated ones.
 */
export function sortPodcastEpisodes(items: ChildItem[]): ChildItem[] {
  return items.slice().sort((a, b) => {
    const ad = a.originally_available_at ?? '';
    const bd = b.originally_available_at ?? '';
    if (ad !== bd) {
      if (!ad) return 1;
      if (!bd) return -1;
      return bd.localeCompare(ad);
    }
    const ai = a.index ?? 0;
    const bi = b.index ?? 0;
    if (ai !== bi) return bi - ai;
    return b.title.localeCompare(a.title);
  });
}

/** What a feed episode's status means to an admin. */
export function episodeStatusLabel(ep: PodcastEpisode): string {
  switch (ep.status) {
    case 'available': return 'Not downloaded';
    case 'downloading': return 'Downloading…';
    case 'downloaded': return 'Downloaded';
    case 'failed': return 'Download failed';
    case 'removed': return 'Deleted';
  }
}

/** Whether the admin can start a download of the episode. */
export function canDownload(ep: PodcastEpisode): boolean {
  return ep.status === 'available' || ep.status === 'failed' || ep.status === 'removed';
}

/** "1.2 GB" / "54 MB" for an enclosure length in bytes; "" when unknown. */
export function formatBytes(n?: number): string {
  if (!n || n <= 0) return '';
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`;
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`;
  return `${Math.max(1, Math.round(n / 1e3))} KB`;
}
