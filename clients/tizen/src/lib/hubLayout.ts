// The saved home layout (GET /users/me/preferences → hub_layout): row order
// and visibility, set on the web home page and shared by every client on the
// account. Pure so the ordering rules are unit-tested. Same rules as the web
// home (web/src/routes/+page.svelte orderedSections) and the Android TV home
// (HomeFragment.buildRows):
//   - rows named in the layout come first, in the saved order;
//   - a row saved as disabled is hidden;
//   - a key with no matching row is skipped (a deleted library, or the web's
//     "libraries" tile grid, which the TV doesn't render);
//   - rows the layout doesn't mention are appended in default order, so a
//     new library (or a row added after the layout was saved, or the TV-only
//     recently_added / collections rows) still shows up without a re-save.
//
// Keys: continue_tv, next_up, continue_movies, continue_other, plan_to_watch,
// trending, library:<uuid> and collection:<uuid> (a promoted collection) are
// shared with the web; recently_added (older
// servers' flat strip) and collections are TV-only extras.

import type { HubRowPref } from './api/types';

/**
 * `rows` in display order under `layout`. `rows` is the full candidate list
 * in default order (empty rows included; the caller drops those after). A
 * missing or malformed layout means the default order. A key listed twice
 * counts once (its first entry), so a row can never render twice.
 */
export function orderRows<T extends { key: string }>(
  rows: readonly T[],
  layout: readonly HubRowPref[] | null | undefined,
): T[] {
  if (!Array.isArray(layout) || layout.length === 0) return rows.slice();
  const byKey = new Map<string, T>();
  for (const r of rows) if (!byKey.has(r.key)) byKey.set(r.key, r);
  const used = new Set<string>();
  const out: T[] = [];
  for (const pref of layout) {
    if (!pref || typeof pref.key !== 'string') continue;
    const row = byKey.get(pref.key);
    if (!row || used.has(pref.key)) continue;
    used.add(pref.key);
    // `enabled` defaults to true (Android's HubRowPref does the same).
    if (pref.enabled !== false) out.push(row);
  }
  for (const r of rows) {
    if (used.has(r.key)) continue;
    used.add(r.key);
    out.push(r);
  }
  return out;
}
