// Text on the item detail page's hero, worded like the Android TV detail page
// (DetailFragment.bindDetail / configurePlayButtons) so both TV clients read
// the same. Pure; unit-tested.

/** "1:02:03" with hours, "4:05" without (Android's fmtTimecode). */
export function formatTimecode(ms: number): string {
  const total = Math.max(0, Math.floor((Number.isFinite(ms) ? ms : 0) / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = String(s).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
}

/** The leaf Play button with a resume point: "Resume from 1:02:03". */
export function resumeLabel(offsetMs: number): string {
  return `Resume from ${formatTimecode(offsetMs)}`;
}

/**
 * Runtime on the meta line: "2h 5m", "45m". Whole minutes, rounded down like
 * Android. Empty under a minute (a short track would otherwise read "0m"),
 * and for a missing or nonsense duration.
 */
export function formatRuntime(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 60_000) return '';
  const min = Math.floor(ms / 60_000);
  return min >= 60 ? `${Math.floor(min / 60)}h ${min % 60}m` : `${min}m`;
}

/** Up to `max` genres for the meta line, comma-separated ('' for none).
 *  Blank and repeated names are dropped so a messy tag list can't show
 *  "Drama, , Drama". */
export function genreLine(genres: readonly string[] | null | undefined, max = 3): string {
  if (!Array.isArray(genres)) return '';
  const out: string[] = [];
  for (const g of genres) {
    const name = typeof g === 'string' ? g.trim() : '';
    if (!name || out.indexOf(name) >= 0) continue;
    out.push(name);
    if (out.length >= max) break;
  }
  return out.join(', ');
}
