// Admin "stop this stream" — the viewer side. Pure; mirrors
// web/src/lib/playback-stop.ts and the Android TV client's PlaybackStop.
//
// POST /api/v1/sessions/{id}/stop (admin, Now Playing) does two things the
// player answers to (server: internal/api/v1/playback_stop.go, sessions.go):
//
//  1. It publishes a `playback.stop` event on the viewer's SSE channel:
//       { item_id, session_id?, client_name?, decision?, message? }
//     The channel is per USER, so each of the user's players decides whether
//     it is the target (isStopForPlayer). client_name is the name the
//     stopped player reports in its progress heartbeats. This app reports
//     'LG webOS TV — <model> #<tag>' (clientName() in device.ts, unique per
//     TV), so a stop aimed at this TV names it, and a direct play (which
//     has no session) is targeted by that name rather than stopping every
//     player of the item. The player passes the same clientName() here.
//  2. For direct play / remux it refuses that (user, item, client IP) for
//     ~2 minutes: the 'playing' progress beacon, media bytes and transcode
//     start answer 403 {"error":{"code":"PLAYBACK_STOPPED","message":…}},
//     so a player that missed the event still stops on its next heartbeat.
//
// Both paths end in the same message: "Playback was stopped by the server
// admin: <message>". Kept identical between the Tizen and webOS apps.

export const PLAYBACK_STOP_EVENT = 'playback.stop';
/** 403 code on media bytes / transcode start / progress for a stopped stream. */
export const PLAYBACK_STOPPED_CODE = 'PLAYBACK_STOPPED';

export interface PlaybackStopEvent {
  itemId: string;
  sessionId?: string;
  clientName?: string;
  decision?: string;
  message?: string;
}

/** Parse the SSE event's `data` blob; null when it isn't a usable stop. */
export function parsePlaybackStop(data: unknown): PlaybackStopEvent | null {
  if (!data || typeof data !== 'object') return null;
  const d = data as Record<string, unknown>;
  if (typeof d.item_id !== 'string' || d.item_id === '') return null;
  const str = (v: unknown) => (typeof v === 'string' && v !== '' ? v : undefined);
  return {
    itemId: d.item_id,
    sessionId: str(d.session_id),
    clientName: str(d.client_name),
    decision: str(d.decision),
    message: str(d.message),
  };
}

export interface PlayerIdentity {
  /** The item this player is playing (null when nothing is loaded). */
  itemId: string | null;
  /** This player's transcode session id, when it has one. */
  sessionId: string | null;
  /** The name this player reports in its heartbeats (device.ts
   *  clientName(): 'LG webOS TV — <model> #<tag>'). It must be the exact
   *  string the heartbeats carry, or a stop the admin aimed at this TV
   *  (always by name for a direct play) is taken for another device's. ''
   *  only for a player that reports none: then a stop naming any device
   *  never matches. */
  clientName: string;
}

/** Does this stop event target this player? Same rules as the web and
 *  Android players:
 *   - a different item (or nothing loaded) → no;
 *   - session_id set → the player whose session it is, or failing that the
 *     one whose client name matches;
 *   - client_name set → the player known by that name;
 *   - neither → untargeted: every player of that item stops. */
export function isStopForPlayer(evt: PlaybackStopEvent, me: PlayerIdentity): boolean {
  if (!me.itemId || evt.itemId !== me.itemId) return false;
  const nameMatches = !!evt.clientName && !!me.clientName && evt.clientName === me.clientName;
  if (evt.sessionId) return evt.sessionId === me.sessionId || nameMatches;
  if (evt.clientName) return nameMatches;
  return true;
}

/** The sentence the player shows — the same wording the server's 403 uses. */
export function adminStopText(message?: string | null): string {
  const m = (message ?? '').trim();
  return m ? `Playback was stopped by the server admin: ${m}` : 'Playback was stopped by the server admin.';
}

/** The sentence carried by a 403 PLAYBACK_STOPPED — the server already
 *  phrases it; the generic sentence when it's missing. */
export function stoppedTextFromServer(serverMessage?: string | null): string {
  const m = (serverMessage ?? '').trim();
  return m || adminStopText();
}

/** Is this thrown API error the server refusing a stopped stream? Duck-typed
 *  on the error `code` so callers don't need the ApiError class. */
export function isPlaybackStoppedError(e: unknown): e is { code: string; message: string } {
  return !!e && typeof e === 'object' && (e as { code?: unknown }).code === PLAYBACK_STOPPED_CODE;
}

/** A media element's `error` event carries no HTTP status, so a direct-play
 *  stream the server refused after an admin stop looks like an unplayable
 *  file. Re-request the same URL (the asset token is already on it) and read
 *  the answer: the sentence to show for a 403 PLAYBACK_STOPPED, null for
 *  anything else (a real decode failure, a network error) so the caller
 *  keeps its usual handling.
 *
 *  A plain GET, not the web client's `Range: bytes=0-0`: the TV app runs
 *  cross-origin, and on the webviews these TVs ship Range still triggers a
 *  CORS preflight the server doesn't allow. So the request stays "simple",
 *  and a stream that isn't refused is aborted as soon as its status is in
 *  rather than downloaded. */
export async function probePlaybackStopped(
  src: string,
  fetchFn: typeof fetch = fetch,
): Promise<string | null> {
  const ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null;
  try {
    const res = await fetchFn(src, { redirect: 'manual', signal: ctrl?.signal });
    if (res.status !== 403) {
      ctrl?.abort();
      return null;
    }
    const body = (await res.json()) as { error?: { code?: unknown; message?: unknown } } | null;
    if (body?.error?.code !== PLAYBACK_STOPPED_CODE) return null;
    const msg = body.error.message;
    return typeof msg === 'string' ? stoppedTextFromServer(msg) : adminStopText();
  } catch {
    return null;
  }
}
