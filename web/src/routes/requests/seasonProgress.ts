// Season progress on the Requests page: "Seasons 1–3 (2 of 3 available)"
// and the "Partially available" chip for a TV request whose first seasons
// have landed while the rest are still coming. The request's status stays
// approved / downloading / failed until every requested season is complete
// (clients parse the status strictly), so this reads seasons_available.

import type { MediaRequest } from '$lib/api';
import { seasonsLabel } from './requestRows';

type SeasonReq = Pick<MediaRequest, 'type' | 'status' | 'seasons' | 'seasons_available'>;

const IN_FLIGHT = new Set(['approved', 'downloading', 'failed']);

/** The requested seasons that are complete, limited to the ones asked for
 *  (an all-seasons request counts every regular season it has). */
function availableSeasons(req: SeasonReq): number[] {
  const got = [...new Set((req.seasons_available ?? []).filter((n) => Number.isInteger(n) && n >= 0))];
  const asked = req.seasons ?? [];
  const list = asked.length ? got.filter((n) => asked.includes(n)) : got;
  return list.sort((a, b) => a - b);
}

/** True for a TV request still in flight with at least one season complete. */
export function isPartiallyAvailable(req: SeasonReq): boolean {
  return req.type === 'show' && IN_FLIGHT.has(req.status) && availableSeasons(req).length > 0;
}

/**
 * The seasons line for a TV request: seasonsLabel, plus how far along it is
 * once a season has landed — "Seasons 1–3 (2 of 3 available)", or for an
 * all-seasons request "All seasons (2 available)".
 */
export function seasonsProgressLabel(req: SeasonReq): string {
  const base = seasonsLabel(req.seasons);
  if (!isPartiallyAvailable(req)) return base;
  const done = availableSeasons(req).length;
  const asked = new Set((req.seasons ?? []).filter((n) => Number.isInteger(n) && n >= 0)).size;
  return asked > 0 ? `${base} (${done} of ${asked} available)` : `${base} (${done} available)`;
}
