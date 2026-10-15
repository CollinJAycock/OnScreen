// An item's versions (v2.6): what the version picker lists and which file
// the player starts. Files arrive best quality first. A file may name its
// edition ("Director's Cut"), which labels its version; the parts of ONE
// stacked movie (cd1 + cd2: part_index 1, 2, …) are a single version, played
// through the server from the first part's file_id: the server streams every
// part as one movie, and the item's duration_ms covers all of them. Pure;
// unit-tested.

import type { ItemFile } from '../api/types';

export interface PlayVersion {
  /** Stable across reads: the first file's id. */
  key: string;
  /** "Director's Cut", "4K · HEVC", "1080p · H.264 · 2 parts". */
  label: string;
  /** The file to play: a stacked movie's first part. */
  file: ItemFile;
  /** The version's files: its parts in order, or the one file. */
  files: ItemFile[];
}

/** Whether a file is one part of a stacked movie. */
export function isStackPart(f: Pick<ItemFile, 'part_index'> | null | undefined): boolean {
  return f?.part_index != null;
}

const CODEC_NAMES: Record<string, string> = {
  h264: 'H.264',
  avc: 'H.264',
  hevc: 'HEVC',
  h265: 'HEVC',
  av1: 'AV1',
  vp9: 'VP9',
  mpeg2video: 'MPEG-2',
  mpeg4: 'MPEG-4',
};

/** A file described by its picture: "4K · HEVC", "1080p · H.264". */
export function fileDescription(f: Pick<ItemFile, 'resolution_w' | 'resolution_h' | 'video_codec'>): string {
  const parts: string[] = [];
  const h = f.resolution_h ?? 0;
  const w = f.resolution_w ?? 0;
  if (h >= 2000 || w >= 3800) parts.push('4K');
  else if (h > 0) parts.push(`${h}p`);
  const codec = (f.video_codec ?? '').toLowerCase();
  if (codec) parts.push(CODEC_NAMES[codec] ?? codec.toUpperCase());
  return parts.join(' · ');
}

function editionOf(f: ItemFile): string {
  return (f.edition ?? '').trim();
}

/**
 * The item's versions in the server's order (best first). Each file is a
 * version, except the parts of a stacked movie, which are one (grouped by
 * edition, so a stacked Director's Cut beside a stacked theatrical cut
 * stays two versions), placed where its first file came.
 */
export function groupVersions(files: readonly ItemFile[] | null | undefined): PlayVersion[] {
  if (!Array.isArray(files)) return [];
  const out: PlayVersion[] = [];
  const stacks = new Map<string, PlayVersion>();
  for (const f of files) {
    if (!isStackPart(f)) {
      out.push({ key: f.id, label: '', file: f, files: [f] });
      continue;
    }
    const group = editionOf(f);
    const v = stacks.get(group);
    if (v) {
      v.files.push(f);
    } else {
      const fresh: PlayVersion = { key: f.id, label: '', file: f, files: [f] };
      stacks.set(group, fresh);
      out.push(fresh);
    }
  }
  for (const v of out) {
    if (v.files.length > 1) {
      // Parts in order; the first one is what plays.
      v.files.sort((a, b) => (a.part_index ?? 0) - (b.part_index ?? 0));
      v.file = v.files[0];
    }
    const name = editionOf(v.file) || fileDescription(v.file) || 'Version';
    v.label = v.files.length > 1 ? `${name} · ${v.files.length} parts` : name;
  }
  // Two versions that would read the same (two 1080p H.264 files with no
  // edition) are told apart by number.
  const seen = new Map<string, number>();
  for (const v of out) seen.set(v.label, (seen.get(v.label) ?? 0) + 1);
  const nth = new Map<string, number>();
  for (const v of out) {
    if ((seen.get(v.label) ?? 0) < 2) continue;
    const n = (nth.get(v.label) ?? 0) + 1;
    nth.set(v.label, n);
    v.label = `${v.label} (${n})`;
  }
  return out;
}

/**
 * The file the player starts: the version holding `fileId` (any of its
 * parts), else the first version; a stacked movie as its first part. Never
 * a later part on its own.
 */
export function playableFile(
  files: readonly ItemFile[] | null | undefined,
  fileId?: string | null
): ItemFile | undefined {
  const versions = groupVersions(files);
  const chosen = fileId ? versions.find((v) => v.files.some((f) => f.id === fileId)) : undefined;
  return (chosen ?? versions[0])?.file;
}
