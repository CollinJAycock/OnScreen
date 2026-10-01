// Which optional surfaces the web app shows, from the server's public
// capabilities and the signed-in account. Kept pure so the rules can be
// tested apart from the components that apply them.
//
// The pattern for everyone but admins: a surface appears once the server has
// said it applies, never on a guess — capabilities or the account's request
// allowance still loading reads as "off", so a link may pop in but never
// flashes up and disappears. Admins keep their surfaces regardless: they
// configure these features and manage what's already there.

import type { CapabilitiesResponse, RequestQuota } from '$lib/api';

type Caps = CapabilitiesResponse | null | undefined;
/** undefined = not loaded yet, null = the load failed. */
type Quota = RequestQuota | null | undefined;

/** Whether the request surfaces — Search's "Ask your admin to add" section
 *  and its TMDB results, Request buttons, the Requests page's tabs — are open
 *  to the signed-in user: the server must have requests on (a TMDB key) and
 *  the account must be allowed to request. An admin is always allowed, so
 *  only the server flag applies to them. */
export function requestsUsable(caps: Caps, quota: Quota, isAdmin: boolean): boolean {
  if (caps?.features?.requests !== true) return false;
  if (isAdmin) return true;
  return quota?.can_request === true;
}

/** The sidebar's Requests link. Admins always keep it (the approval queue
 *  and the Upcoming setup hint live there, and requests made before a TMDB
 *  key was removed still need managing); everyone else only when they can
 *  use requests. */
export function requestsNavVisible(caps: Caps, quota: Quota, isAdmin: boolean): boolean {
  return isAdmin || requestsUsable(caps, quota, false);
}

/** The Requests page's Upcoming tab. Admins always keep it ("No Radarr or
 *  Sonarr connected" is their setup hint); everyone else needs the request
 *  surfaces and an enabled Radarr or Sonarr. A server older than
 *  features.upcoming keeps the tab, as before. */
export function upcomingTabVisible(caps: Caps, quota: Quota, isAdmin: boolean): boolean {
  if (isAdmin) return true;
  return requestsUsable(caps, quota, false) && caps?.features?.upcoming !== false;
}

/** The sidebar's Live TV link (the guide, and the channels and recordings
 *  behind it). Admins always keep it; everyone else needs a configured tuner.
 *  A server older than live_tv_configured falls back to live_tv, the old
 *  behaviour. */
export function liveTVNavVisible(caps: Caps, isAdmin: boolean): boolean {
  if (isAdmin) return true;
  const f = caps?.features;
  if (!f) return false;
  return f.live_tv_configured ?? f.live_tv === true;
}

/** The player's "Search online…" subtitle entry: the server has
 *  OpenSubtitles enabled with a key. */
export function onlineSubtitlesVisible(caps: Caps): boolean {
  return caps?.features?.subtitles_external === true;
}
