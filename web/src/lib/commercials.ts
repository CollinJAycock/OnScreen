// DVR commercial breaks in the web player. The server lists them per file
// (`files[].commercials`, from DVR commercial detection); the watch page asks
// which break, if any, the playhead is inside, to show "Skip Ad".
import type { CommercialMarker, ItemFile } from '$lib/api';

/** localStorage key for the per-browser "always skip ads" preference. */
export const AUTO_SKIP_ADS_KEY = 'onscreen_auto_skip_ads';

/** The breaks of the playing file (files[0] is always the one playing). */
export function fileCommercials(files: Pick<ItemFile, 'commercials'>[] | undefined | null): CommercialMarker[] {
  return files?.[0]?.commercials ?? [];
}

/** The break containing tMs, or null. */
export function commercialAt(breaks: CommercialMarker[], tMs: number): CommercialMarker | null {
  for (const b of breaks) {
    if (tMs >= b.start_ms && tMs < b.end_ms) return b;
  }
  return null;
}

/** Stable id for "already auto-skipped this one" bookkeeping. */
export function commercialId(b: CommercialMarker): string {
  return `${b.start_ms}-${b.end_ms}`;
}
