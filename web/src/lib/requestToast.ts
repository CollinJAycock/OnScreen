// What to tell the user after a request was created — shared by the Search
// page and the show page's "Request more seasons" button so both say the same
// thing about auto-approval and quotas. The wording is the Search page's
// own (search.toast*), so the two can't drift apart.

import { get } from 'svelte/store';
import type { CreatedRequest, MediaRequest } from '$lib/api';
import { i18n, type Translator } from '$lib/i18n/index.svelte';

/** The server approves a request on the spot when the requester has
 *  auto-approve for the type (or is an admin) and the hand-off to
 *  Radarr/Sonarr succeeded; it then comes back approved or downloading. */
export function wasAutoApproved(req: Pick<MediaRequest, 'auto_approved' | 'status'>): boolean {
  return req.auto_approved === true || req.status === 'approved' || req.status === 'downloading';
}

/** The toast for a created request: info when it went over the quota (it
 *  waits for an admin), otherwise success saying whether it's on its way. */
export function createdRequestToast(
  created: CreatedRequest,
  label: string,
  tx: Translator = get(i18n),
): { kind: 'info' | 'success'; text: string } {
  // search.toastOverQuota is search.overQuota (overQuotaMessage()) with the
  // title, as one sentence for translators.
  if (created.over_quota) return { kind: 'info', text: tx('search.toastOverQuota', { title: label }) };
  return {
    kind: 'success',
    text: wasAutoApproved(created)
      ? tx('search.toastAutoApproved', { title: label })
      : tx('search.toastRequested', { title: label }),
  };
}
