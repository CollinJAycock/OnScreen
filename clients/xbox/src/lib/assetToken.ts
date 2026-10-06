// The purpose=asset token every image, stream and subtitle URL carries
// (api.assetUrl: `<img>` and the media elements can't send a header). The
// server mints it for 24 h (AssetTokenTTL), at sign-in and on every token
// refresh. API calls renew the access token by themselves on a 401; nothing
// did the same for this one before the first screen rendered, so a TV
// signed in more than a day ago (left on, or an app updated over an old
// sign-in) opened on a home screen of broken posters: every `<img>` carried
// the expired token, and the event stream's own renewal (lib/events) came
// after they had failed, with nothing to load them again.
//
// So the token is checked before the app shows anything, and again when the
// app comes back from the background: renewed when it is near or past its
// expiry, or when its age is unknown (stored by a build that didn't record
// it). The wait is capped, so a TV offline at launch still opens.

import { assetTokenNearExpiry } from './events';
import type { RefreshOutcome } from './api/client';

/** The longest the app waits on the renewal before it shows its first
 *  screen anyway (offline: a refresh only fails after the request timeout). */
export const ASSET_TOKEN_BOOT_WAIT_MS = 4000;

export interface AssetTokenDeps {
  /** The stored access token (null signed out). */
  accessToken(): string | null;
  /** The stored asset token (null signed out, or a server without them). */
  assetToken(): string | null;
  /** When the asset token was minted, by this TV's clock; null unknown. */
  issuedAt(): number | null;
  now(): number;
  /** api.refreshTokensOutcome: the app's one refresh in flight. */
  refresh(): Promise<RefreshOutcome>;
  /** navigator.onLine (absent: assumed online). */
  online?(): boolean;
}

/** Whether the stored asset token should be renewed before it is used. */
export function assetTokenStale(d: Pick<AssetTokenDeps, 'accessToken' | 'assetToken' | 'issuedAt' | 'now'>): boolean {
  if (!d.accessToken() || !d.assetToken()) return false;
  const at = d.issuedAt();
  return at === null || assetTokenNearExpiry(at, d.now());
}

/** Renew a stale asset token. Resolves once renewed, or after `waitMs`
 *  whatever the refresh is still doing (it lands in storage when it does);
 *  never rejects. True when a renewal was started. */
// The asset token the last renewal was started for (this run). A refresh
// whose answer was lost may still have rotated the token on the server; a
// second try with the retired refresh token reads there as reuse and signs
// every device out. So one try per stored token here, as the event stream's
// redial does (lib/events shouldRefreshBeforeRedial): a token that didn't
// change is left to the API's own 401 renewal.
let attemptedFor: string | null = null;

/** Test hook: forget the last attempt. */
export function resetAssetTokenAttempts(): void {
  attemptedFor = null;
}

export async function ensureFreshAssetToken(
  d: AssetTokenDeps,
  waitMs = ASSET_TOKEN_BOOT_WAIT_MS,
  timer: (fn: () => void, ms: number) => unknown = (fn, ms) => setTimeout(fn, ms),
): Promise<boolean> {
  if (!assetTokenStale(d)) return false;
  if (d.online && !d.online()) return false;
  const token = d.assetToken();
  if (token !== null && token === attemptedFor) return false;
  attemptedFor = token;
  const refresh = d.refresh().catch(() => 'failed' as RefreshOutcome);
  await Promise.race([refresh, new Promise<void>((resolve) => timer(resolve, waitMs))]);
  return true;
}
