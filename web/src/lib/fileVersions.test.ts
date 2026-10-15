import { describe, expect, it } from 'vitest';
import type { ItemFile } from './api';
import { groupVersions, playableDurationMs, promoteVersion, stackOf, versionLabel } from './fileVersions';

function file(id: string, extra: Partial<ItemFile> = {}): ItemFile {
  return {
    id,
    stream_url: `/media/stream/${id}`,
    container: 'mkv',
    faststart: true,
    audio_streams: [],
    subtitle_streams: [],
    chapters: [],
    ...extra,
  };
}

const tech = (f: ItemFile) => `${f.resolution_h ?? 0}p`;

describe('fileVersions', () => {
  const uhd = file('uhd', { resolution_h: 2160, duration_ms: 9_000 });
  const cd2 = file('cd2', { part_index: 2, resolution_h: 1080, duration_ms: 2_000 });
  const cd1 = file('cd1', { part_index: 1, resolution_h: 1080, duration_ms: 3_000 });
  const dc = file('dc', { edition: "Director's Cut", resolution_h: 1080, duration_ms: 8_000 });
  const files = [uhd, cd2, cd1, dc];

  it('groups a stack into one version, part 1 first', () => {
    const v = groupVersions(files);
    expect(v.map((x) => x.files.map((f) => f.id))).toEqual([['uhd'], ['cd1', 'cd2'], ['dc']]);
    expect(v[1].primary.id).toBe('cd1');
  });

  it('labels with the edition, else the tech description, plus the part count', () => {
    const v = groupVersions(files);
    expect(v.map((x) => versionLabel(x, tech))).toEqual(['2160p', '1080p · 2 parts', "Director's Cut"]);
  });

  it('sums a stack for the duration', () => {
    expect(playableDurationMs(files, cd2)).toBe(5_000);
    expect(playableDurationMs(files, dc)).toBe(8_000);
    expect(playableDurationMs([cd1, file('cd2', { part_index: 2 })], cd1)).toBeUndefined();
  });

  it('promotes a whole stack to the front', () => {
    expect(promoteVersion(files, cd2).map((f) => f.id)).toEqual(['cd1', 'cd2', 'uhd', 'dc']);
    expect(promoteVersion(files, dc).map((f) => f.id)).toEqual(['dc', 'uhd', 'cd2', 'cd1']);
  });

  it('keeps stacks of different editions apart', () => {
    const e1 = file('e1', { part_index: 1, edition: 'Extended' });
    const e2 = file('e2', { part_index: 2, edition: 'Extended' });
    expect(stackOf([cd1, cd2, e1, e2], e2)?.map((f) => f.id)).toEqual(['e1', 'e2']);
    expect(stackOf([cd1, e2], cd1)).toBeNull();
  });
});
