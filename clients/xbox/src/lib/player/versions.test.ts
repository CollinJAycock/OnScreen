import { describe, expect, it } from 'vitest';
import type { ItemFile } from '../api/types';
import { fileDescription, groupVersions, isStackPart, playableFile } from './versions';

function file(id: string, extra: Partial<ItemFile> = {}): ItemFile {
  return {
    id,
    stream_url: `/media/stream/${id}`,
    faststart: true,
    audio_streams: [],
    subtitle_streams: [],
    chapters: [],
    ...extra,
  };
}

describe('groupVersions', () => {
  it('labels a version by its edition, else by its picture', () => {
    const v = groupVersions([
      file('a', { edition: "Director's Cut", resolution_w: 3840, resolution_h: 2160, video_codec: 'hevc' }),
      file('b', { resolution_w: 1920, resolution_h: 1080, video_codec: 'h264' }),
    ]);
    expect(v.map((x) => x.label)).toEqual(["Director's Cut", '1080p · H.264']);
    expect(v.map((x) => x.file.id)).toEqual(['a', 'b']);
  });

  it('shows the parts of a stacked movie as ONE version, played from the first part', () => {
    const v = groupVersions([
      file('cd2', { part_index: 2, resolution_h: 480, video_codec: 'mpeg4' }),
      file('cd1', { part_index: 1, resolution_h: 480, video_codec: 'mpeg4' }),
    ]);
    expect(v).toHaveLength(1);
    expect(v[0].label).toBe('480p · MPEG-4 · 2 parts');
    expect(v[0].file.id).toBe('cd1');
    expect(v[0].files.map((f) => f.id)).toEqual(['cd1', 'cd2']);
  });

  it('keeps two stacked editions apart, each one version', () => {
    const v = groupVersions([
      file('t1', { part_index: 1, edition: 'Theatrical' }),
      file('d1', { part_index: 1, edition: 'Extended' }),
      file('t2', { part_index: 2, edition: 'Theatrical' }),
      file('d2', { part_index: 2, edition: 'Extended' }),
    ]);
    expect(v.map((x) => x.label)).toEqual(['Theatrical · 2 parts', 'Extended · 2 parts']);
    expect(v.map((x) => x.file.id)).toEqual(['t1', 'd1']);
  });

  it('tells apart versions that would read the same', () => {
    const v = groupVersions([file('a', { resolution_h: 1080 }), file('b', { resolution_h: 1080 })]);
    expect(v.map((x) => x.label)).toEqual(['1080p (1)', '1080p (2)']);
    expect(groupVersions([file('x')])[0].label).toBe('Version');
  });

  it('is empty without files', () => {
    expect(groupVersions([])).toEqual([]);
    expect(groupVersions(undefined)).toEqual([]);
  });
});

describe('playableFile', () => {
  const files = [
    file('4k', { resolution_h: 2160 }),
    file('cd2', { part_index: 2 }),
    file('cd1', { part_index: 1 }),
  ];

  it('starts the first version, or the one asked for', () => {
    expect(playableFile(files)?.id).toBe('4k');
    expect(playableFile(files, 'cd1')?.id).toBe('cd1');
  });

  it('never starts a later part on its own: any part means the movie from part 1', () => {
    expect(playableFile(files, 'cd2')?.id).toBe('cd1');
    expect(playableFile([file('p2', { part_index: 2 }), file('p1', { part_index: 1 })])?.id).toBe('p1');
  });

  it('falls back to the first version for a file id it does not know', () => {
    expect(playableFile(files, 'gone')?.id).toBe('4k');
    expect(playableFile([])).toBeUndefined();
  });
});

describe('isStackPart / fileDescription', () => {
  it('knows a part by its part_index', () => {
    expect(isStackPart(file('a', { part_index: 1 }))).toBe(true);
    expect(isStackPart(file('a'))).toBe(false);
    expect(isStackPart(undefined)).toBe(false);
  });

  it('describes 4K by either side, and an unknown codec by its name', () => {
    expect(fileDescription({ resolution_w: 3840, resolution_h: 1600, video_codec: 'av1' })).toBe('4K · AV1');
    expect(fileDescription({ resolution_h: 720, video_codec: 'theora' })).toBe('720p · THEORA');
    expect(fileDescription({})).toBe('');
  });
});
