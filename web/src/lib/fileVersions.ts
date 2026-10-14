// Versions of a movie for the version picker: each file is a version, except
// that the parts of a multi-part movie ("cd1" + "cd2", files[].part_index)
// are one version together. The server only sets part_index on files that
// really play as a stack, and streams the whole stack whichever part's
// file_id a transcode is started with, so the page keeps working off
// files[0] — promoteVersion just keeps a stack's parts together, part 1
// first.

import type { ItemFile } from './api';

export interface FileVersion {
  /** The file playback starts from: part 1 of a stack, else the file. */
  primary: ItemFile;
  /** Every file of the version, in part order (one for a plain file). */
  files: ItemFile[];
}

function stackKey(f: ItemFile): string | null {
  if (f.part_index == null) return null;
  return `${(f.edition ?? '').toLowerCase()}\u0000${(f.container ?? '').toLowerCase()}`;
}

/** The parts of the stack f belongs to, in part order; null when f is not a part. */
export function stackOf(files: ItemFile[], f: ItemFile): ItemFile[] | null {
  const key = stackKey(f);
  if (key == null) return null;
  const parts = files.filter((g) => stackKey(g) === key);
  if (parts.length < 2) return null;
  return [...parts].sort((a, b) => (a.part_index ?? 0) - (b.part_index ?? 0));
}

/** Files grouped into versions, in the server's order (best first). */
export function groupVersions(files: ItemFile[]): FileVersion[] {
  const out: FileVersion[] = [];
  const seen = new Set<string>();
  for (const f of files) {
    if (seen.has(f.id)) continue;
    const parts = stackOf(files, f) ?? [f];
    for (const p of parts) seen.add(p.id);
    out.push({ primary: parts[0], files: parts });
  }
  return out;
}

/** files reordered so f's version comes first, part 1 leading. */
export function promoteVersion(files: ItemFile[], f: ItemFile): ItemFile[] {
  const group = stackOf(files, f) ?? [f];
  const ids = new Set(group.map((g) => g.id));
  return [...group, ...files.filter((g) => !ids.has(g.id))];
}

/** Length of what playing f plays: the whole stack for a part, else f's own. */
export function playableDurationMs(files: ItemFile[], f: ItemFile): number | undefined {
  const parts = stackOf(files, f);
  if (!parts) return f.duration_ms;
  let total = 0;
  for (const p of parts) {
    if (!p.duration_ms) return undefined;
    total += p.duration_ms;
  }
  return total;
}

/**
 * Picker label: the edition the file name gives, else the technical
 * description, plus the part count for a multi-part version.
 */
export function versionLabel(v: FileVersion, techLabel: (f: ItemFile) => string): string {
  const base = v.primary.edition?.trim() || techLabel(v.primary);
  if (v.files.length > 1) return `${base} · ${v.files.length} parts`;
  return base;
}
