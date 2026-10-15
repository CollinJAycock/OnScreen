// Library grid selection: shift-click ranges and "select all on this page".
// Pure helpers over the ids in grid order, tested in gridSelection.test.ts.

/** Shift-click: select every id from the anchor (the last tile clicked)
 *  through target, inclusive, in grid order, keeping what was already
 *  selected. Without a usable anchor it toggles target alone. */
export function selectRange(
  selected: ReadonlySet<string>,
  ids: readonly string[],
  anchor: string | null,
  target: string,
): Set<string> {
  const next = new Set(selected);
  const a = anchor ? ids.indexOf(anchor) : -1;
  const b = ids.indexOf(target);
  if (a < 0 || b < 0) {
    if (next.has(target)) next.delete(target);
    else next.add(target);
    return next;
  }
  const [from, to] = a <= b ? [a, b] : [b, a];
  for (let i = from; i <= to; i++) next.add(ids[i]);
  return next;
}

/** Whether every id is selected (false for an empty page). */
export function allSelected(selected: ReadonlySet<string>, ids: readonly string[]): boolean {
  return ids.length > 0 && ids.every((id) => selected.has(id));
}

/** "Select all on this page", or — when that's already the case — clear
 *  those ids again. */
export function toggleAll(selected: ReadonlySet<string>, ids: readonly string[]): Set<string> {
  const next = new Set(selected);
  if (allSelected(selected, ids)) for (const id of ids) next.delete(id);
  else for (const id of ids) next.add(id);
  return next;
}

/** Runs fn over items with at most `limit` in flight, collecting failures
 *  rather than stopping at the first. */
export async function runLimited<T>(
  items: readonly T[],
  limit: number,
  fn: (item: T) => Promise<unknown>,
): Promise<T[]> {
  const failed: T[] = [];
  let next = 0;
  async function worker() {
    while (next < items.length) {
      const it = items[next++];
      try { await fn(it); } catch { failed.push(it); }
    }
  }
  await Promise.all(Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, worker));
  return failed;
}
