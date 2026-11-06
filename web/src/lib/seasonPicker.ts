// Season picker rules for season-level TV requests: what each season row
// shows, which rows can be picked, what the picker starts with, and what goes
// in the request body. Pure module so the rules are unit-tested without
// mounting SeasonPicker.svelte.

import type { SeasonInfo } from '$lib/api';
import { t as translate, tn, type MessageKey } from '$lib/i18n/index.svelte';

/** Where a season stands for this user:
 *  owned     — every aired episode is in the library ("In library")
 *  requested — covered by one of the user's active requests
 *  partial   — some aired episodes are in the library
 *  missing   — aired, nothing in the library
 *  unaired   — nothing has aired yet (can still be requested ahead) */
export type SeasonState = 'owned' | 'requested' | 'partial' | 'missing' | 'unaired';

export function seasonState(s: SeasonInfo): SeasonState {
  if (s.requested) return 'requested';
  const aired = s.aired_episodes ?? 0;
  if (aired > 0 && (s.owned_episodes ?? 0) >= aired) return 'owned';
  if ((s.owned_episodes ?? 0) > 0) return 'partial';
  if (aired === 0) return 'unaired';
  return 'missing';
}

/** A season can be picked unless it's already in the library or requested. */
export function isSelectable(s: SeasonInfo): boolean {
  const st = seasonState(s);
  return st !== 'owned' && st !== 'requested';
}

/** Aired and not (fully) in the library, and not requested yet — what
 *  "Request more seasons" is for. */
export function isMissing(s: SeasonInfo): boolean {
  const st = seasonState(s);
  return st === 'missing' || st === 'partial';
}

/** Whether the show has anything worth a "Request more seasons" button. */
export function hasRequestableMissing(seasons: readonly SeasonInfo[]): boolean {
  return seasons.some((s) => s.season_number > 0 && isMissing(s));
}

/** Row title: TMDB's season name, "Specials" for season 0. */
export function seasonTitle(s: SeasonInfo): string {
  if (s.season_number === 0) return translate('ui.seasons.specials');
  const name = (s.name ?? '').trim();
  return name || translate('player.detail.seasonN', { n: s.season_number });
}

const REQUEST_STATUS: Record<string, MessageKey> = {
  pending: 'ui.seasons.status.pending',
  approved: 'ui.seasons.status.approved',
  downloading: 'ui.seasons.status.downloading',
};

/** The row's right-hand note: "In library", "Requested · Pending",
 *  "3 of 10 episodes in library", "Airs Mar 4, 2027", "10 episodes". */
export function seasonNote(s: SeasonInfo): string {
  switch (seasonState(s)) {
    case 'owned':
      return translate('ui.seasons.inLibrary');
    case 'requested':
      return translate('ui.seasons.requested', { status: translate(REQUEST_STATUS[s.requested ?? ''] ?? 'ui.seasons.status.pending') });
    case 'partial':
      return translate('ui.seasons.partial', { owned: s.owned_episodes ?? 0, aired: s.aired_episodes ?? 0 });
    case 'unaired': {
      if (!s.air_date) return translate('ui.seasons.notAired');
      const t = Date.parse(s.air_date + 'T00:00:00Z');
      if (!Number.isFinite(t)) return translate('ui.seasons.notAired');
      const d = new Date(t).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' });
      return translate('ui.seasons.airs', { date: d });
    }
    default: {
      const n = s.episode_count ?? 0;
      return n > 0 ? tn('ui.seasons.episodes', n) : '';
    }
  }
}

/** The picker's starting selection: every pickable regular season. Specials
 *  start unselected. */
export function defaultSelection(seasons: readonly SeasonInfo[]): Set<number> {
  return new Set(seasons.filter((s) => s.season_number > 0 && isSelectable(s)).map((s) => s.season_number));
}

/** State of the "All seasons" checkbox over the pickable regular seasons. */
export function allState(seasons: readonly SeasonInfo[], selected: ReadonlySet<number>): 'all' | 'some' | 'none' {
  const pickable = seasons.filter((s) => s.season_number > 0 && isSelectable(s));
  const n = pickable.filter((s) => selected.has(s.season_number)).length;
  if (pickable.length > 0 && n === pickable.length) return 'all';
  return n === 0 ? 'none' : 'some';
}

/** Toggle "All seasons": select every pickable regular season, or clear them
 *  (a picked Specials row is left as it is). */
export function toggleAll(seasons: readonly SeasonInfo[], selected: ReadonlySet<number>): Set<number> {
  const next = new Set(selected);
  const pickable = seasons.filter((s) => s.season_number > 0 && isSelectable(s)).map((s) => s.season_number);
  if (allState(seasons, selected) === 'all') {
    for (const n of pickable) next.delete(n);
  } else {
    for (const n of pickable) next.add(n);
  }
  return next;
}

/**
 * The `seasons` to send with the request. `undefined` (omit it — "every
 * season", which also takes in seasons announced later) when the user picked
 * all of a show they have none of and haven't requested; otherwise the picked
 * season numbers, ascending. An empty array means nothing is picked (the
 * submit button stays disabled).
 */
export function requestSeasons(seasons: readonly SeasonInfo[], selected: ReadonlySet<number>): number[] | undefined {
  const picked = seasons
    .filter((s) => selected.has(s.season_number) && isSelectable(s))
    .map((s) => s.season_number)
    .sort((a, b) => a - b);
  if (picked.length === 0) return [];
  const regular = seasons.filter((s) => s.season_number > 0);
  const fresh = regular.every((s) => {
    const st = seasonState(s);
    return st === 'missing' || st === 'unaired';
  });
  const everyRegular = regular.length > 0 && regular.every((s) => selected.has(s.season_number));
  if (fresh && everyRegular && !selected.has(0)) return undefined;
  return picked;
}

/** "1–3, 5" for sorted season numbers (runs of three or more collapse). */
function runs(nums: number[]): string {
  const parts: string[] = [];
  let i = 0;
  while (i < nums.length) {
    let j = i;
    while (j + 1 < nums.length && nums[j + 1] === nums[j] + 1) j++;
    if (j - i >= 2) parts.push(`${nums[i]}–${nums[j]}`);
    else for (let k = i; k <= j; k++) parts.push(String(nums[k]));
    i = j + 1;
  }
  return parts.join(', ');
}

/** Submit-button label for a selection: "Request all seasons",
 *  "Request season 2", "Request seasons 2–4 + specials". */
export function submitLabel(payload: number[] | undefined): string {
  if (payload === undefined) return translate('ui.seasons.requestAll');
  const specials = payload.includes(0);
  const regular = payload.filter((n) => n > 0);
  if (regular.length === 0) return specials ? translate('ui.seasons.requestSpecials') : translate('ui.seasons.request');
  return tn(specials ? 'ui.seasons.requestSeasonsSpecials' : 'ui.seasons.requestSeasons', regular.length, { list: runs(regular) });
}
