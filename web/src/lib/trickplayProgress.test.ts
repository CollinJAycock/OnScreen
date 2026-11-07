import { isVideoLibraryType, formatTrickplayProgress, trickplayQueuedMessage } from './trickplayProgress';
import { setLocale } from '$lib/i18n/index.svelte';

describe('isVideoLibraryType', () => {
  it('accepts the video library types', () => {
    for (const t of ['movie', 'show', 'home_video', 'anime', 'cartoons']) {
      expect(isVideoLibraryType(t)).toBe(true);
    }
  });
  it('rejects everything else', () => {
    for (const t of ['music', 'photo', 'dvr', 'audiobook', 'book', 'podcast', '', undefined, null]) {
      expect(isVideoLibraryType(t)).toBe(false);
    }
  });
});

describe('formatTrickplayProgress', () => {
  const s = (total: number, done: number, pending: number, failed: number) =>
    ({ enabled: true, total, done, pending, failed });

  it('is empty without a status', () => {
    expect(formatTrickplayProgress(null)).toBe('');
  });
  it('explains an empty library', () => {
    expect(formatTrickplayProgress(s(0, 0, 0, 0))).toMatch(/no videos/i);
  });
  it('summarises partial progress with thousands separators', () => {
    expect(formatTrickplayProgress(s(1030, 412, 600, 18)))
      .toBe('412 of 1,030 videos have thumbnails · 600 pending · 18 failed');
  });
  it('omits zero pending / failed parts', () => {
    expect(formatTrickplayProgress(s(10, 7, 3, 0))).toBe('7 of 10 videos have thumbnails · 3 pending');
    expect(formatTrickplayProgress(s(10, 9, 0, 1))).toBe('9 of 10 videos have thumbnails · 1 failed');
  });
  it('celebrates completion', () => {
    expect(formatTrickplayProgress(s(5, 5, 0, 0))).toBe('All 5 videos have thumbnails.');
    expect(formatTrickplayProgress(s(1, 1, 0, 0))).toBe('The video has thumbnails.');
  });
});

describe('trickplayQueuedMessage', () => {
  it('says when nothing was queued', () => {
    expect(trickplayQueuedMessage(0)).toMatch(/nothing new to queue/i);
  });
  it('counts queued videos', () => {
    expect(trickplayQueuedMessage(1)).toMatch(/^Queued 1 video\./);
    expect(trickplayQueuedMessage(1200)).toMatch(/^Queued 1,200 videos\./);
  });
});

describe('translations', () => {
  it('follows the interface language', async () => {
    const st = (total: number, done: number, pending: number, failed: number) =>
      ({ total, done, pending, failed }) as Parameters<typeof formatTrickplayProgress>[0];
    await setLocale('es');
    try {
      // Spanish groups thousands with a dot, from five digits up.
      expect(formatTrickplayProgress(st(12345, 412, 600, 0))).toBe('412 de 12.345 vídeos tienen miniaturas · 600 pendientes');
      expect(trickplayQueuedMessage(1)).toMatch(/^1 vídeo en cola\./);
      await setLocale('zh-Hans');
      expect(trickplayQueuedMessage(3)).toMatch(/^已将 3 个视频加入队列/);
    } finally {
      await setLocale('en');
    }
  });
});
