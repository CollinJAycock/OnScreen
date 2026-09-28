// What to tell the user after a request was created — shared by the Search
// page and the show page's "Request more seasons" button so both say the same
// thing about auto-approval and quotas.

import type { CreatedRequest, MediaRequest } from '$lib/api';
import { OVER_QUOTA_MESSAGE } from '../routes/search/quota';

/** The server approves a request on the spot when the requester has
 *  auto-approve for the type (or is an admin) and the hand-off to
 *  Radarr/Sonarr succeeded; it then comes back approved or downloading. */
export function wasAutoApproved(req: Pick<MediaRequest, 'auto_approved' | 'status'>): boolean {
  return req.auto_approved === true || req.status === 'approved' || req.status === 'downloading';
}

/** The toast for a created request: info when it went over the quota (it
 *  waits for an admin), otherwise success saying whether it's on its way. */
export function createdRequestToast(created: CreatedRequest, label: string): { kind: 'info' | 'success'; text: string } {
  if (created.over_quota) return { kind: 'info', text: `${OVER_QUOTA_MESSAGE}: ${label}` };
  return {
    kind: 'success',
    text: wasAutoApproved(created)
      ? `Approved automatically — it's on its way: ${label}`
      : `Requested: ${label} — awaiting admin approval`,
  };
}
