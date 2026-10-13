import { describe, it, expect } from 'vitest';
import type { ChildItem, PodcastEpisode } from './api';
import { sortPodcastEpisodes, canDownload, episodeStatusLabel, formatBytes } from './podcasts';

function child(over: Partial<ChildItem>): ChildItem {
  return { id: over.title ?? 'x', title: 'x', type: 'podcast_episode', created_at: '', updated_at: 0, ...over };
}

describe('sortPodcastEpisodes', () => {
  it('orders by release date, newest first, undated last', () => {
    const out = sortPodcastEpisodes([
      child({ title: 'Undated' }),
      child({ title: 'Old', originally_available_at: '2024-01-05' }),
      child({ title: 'New', originally_available_at: '2026-10-12' }),
    ]);
    expect(out.map((c) => c.title)).toEqual(['New', 'Old', 'Undated']);
  });

  it('keeps date-stamped file names of hand-added episodes in publication order', () => {
    const out = sortPodcastEpisodes([
      child({ title: '2024-04-01 - First' }),
      child({ title: '2024-05-01 - Second' }),
    ]);
    expect(out.map((c) => c.title)).toEqual(['2024-05-01 - Second', '2024-04-01 - First']);
  });
});

describe('episode helpers', () => {
  const ep = (status: PodcastEpisode['status']) => ({ id: 'e', feed_id: 'f', title: 't', status }) as PodcastEpisode;

  it('offers a download unless one is running or done', () => {
    for (const s of ['available', 'failed', 'removed'] as const) expect(canDownload(ep(s))).toBe(true);
    expect(canDownload(ep('downloading'))).toBe(false);
    expect(canDownload(ep('downloaded'))).toBe(false);
  });

  it('labels each status', () => {
    expect(episodeStatusLabel(ep('available'))).toBe('Not downloaded');
    expect(episodeStatusLabel(ep('failed'))).toBe('Download failed');
  });

  it('formats enclosure sizes', () => {
    expect(formatBytes(undefined)).toBe('');
    expect(formatBytes(54_300_000)).toBe('54 MB');
    expect(formatBytes(1_230_000_000)).toBe('1.2 GB');
  });
});
