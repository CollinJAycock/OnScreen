// The parental watch limit as the player shows it.
//
// The server refuses playback with 403 PARENTAL_LIMIT (playback decision,
// transcode start, a 'playing' heartbeat) and puts a reason code in the
// message: `outside_allowed_hours`, `daily_limit_reached`, … The players turn
// that code into a sentence; the cast path used to show the code itself.
import { t } from '$lib/i18n/index.svelte';

export const PARENTAL_LIMIT_CODE = 'PARENTAL_LIMIT';

/** The viewer-facing sentence for a PARENTAL_LIMIT reason code. */
export function parentalBlockMessage(reason: string): string {
  switch (reason) {
    case 'outside_allowed_hours':
      return t('player.parental.outsideHours');
    case 'daily_limit_reached':
      return t('player.parental.dailyLimit');
    default:
      return t('player.parental.blocked');
  }
}

/** Whether `e` is the API's 403 PARENTAL_LIMIT error (ApiRequestError's
 *  shape; checked structurally so callers needn't import the API module). */
export function isParentalLimitError(e: unknown): e is { code: string; message: string } {
  return !!e && typeof e === 'object' && (e as { code?: unknown }).code === PARENTAL_LIMIT_CODE;
}
