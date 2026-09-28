// Admin "stop this stream" — the viewer side.
//
// POST /api/v1/sessions/{id}/stop (admin, Now Playing) publishes a
// `playback.stop` event on the viewer's SSE channel:
//
//   { item_id, session_id?, client_name?, decision?, message? }
//
// The channel is per-USER, not per-device, so every one of the user's open
// players receives it and each decides whether it's the target:
//   - session_id set  → the player whose transcode/remux session it is (or,
//                        failing that, the one whose client name matches);
//   - client_name set → the player reporting that name in its heartbeats;
//   - neither         → untargeted: any player on that item stops.
// The server also refuses the stopped device's stream for ~2 minutes
// (403 PLAYBACK_STOPPED), so a player that misses the event still stops.
// Subscribers: the watch page (video) and the global AudioPlayer (music).

export const PLAYBACK_STOP_EVENT = 'playback.stop';
/** 403 code on media bytes / transcode start / progress for a stopped stream. */
export const PLAYBACK_STOPPED_CODE = 'PLAYBACK_STOPPED';

export type PlaybackStopEvent = {
  itemId: string;
  sessionId?: string;
  clientName?: string;
  decision?: string;
  message?: string;
};

/** Parse the SSE `data` blob; null when it isn't a usable stop event. */
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

export type PlayerIdentity = {
  /** The item this player is playing (null when nothing is loaded). */
  itemId: string | null;
  /** This player's transcode/remux session id, when it has one. */
  sessionId: string | null;
  /** The client name this player reports in its progress heartbeats. */
  clientName: string;
};

/** Does this stop event target this player? */
export function isStopForPlayer(evt: PlaybackStopEvent, me: PlayerIdentity): boolean {
  if (!me.itemId || evt.itemId !== me.itemId) return false;
  const nameMatches = !!evt.clientName && evt.clientName === me.clientName;
  if (evt.sessionId) return evt.sessionId === me.sessionId || nameMatches;
  if (evt.clientName) return nameMatches;
  return true;
}

/** The sentence the player shows — the same wording the server's 403 uses. */
export function adminStopText(message?: string): string {
  const m = (message ?? '').trim();
  return m ? `Playback was stopped by the server admin: ${m}` : 'Playback was stopped by the server admin.';
}

/** Is this thrown API error the server refusing a stopped stream? Duck-typed
 *  on the error `code` (ApiRequestError carries it) so callers don't need the
 *  class itself. */
export function isPlaybackStoppedError(e: unknown): e is { code: string; message: string } {
  return !!e && typeof e === 'object' && (e as { code?: unknown }).code === PLAYBACK_STOPPED_CODE;
}

/** A media element's `error` event carries no HTTP status, so a stream the
 *  server refused after an admin stop looks exactly like an unplayable file.
 *  Re-request one byte of the same URL (same auth: cookie, or the asset token
 *  already on the URL) and read the answer: the sentence to show when it's a
 *  403 PLAYBACK_STOPPED, null for anything else — a real decode failure, a
 *  network error — so the caller keeps its usual error handling. */
export async function probePlaybackStopped(src: string): Promise<string | null> {
  try {
    const res = await fetch(src, { headers: { Range: 'bytes=0-0' } });
    if (res.status !== 403) return null;
    const body = (await res.json()) as { error?: { code?: unknown; message?: unknown } } | null;
    if (body?.error?.code !== PLAYBACK_STOPPED_CODE) return null;
    const msg = body.error.message;
    return typeof msg === 'string' && msg.trim() !== '' ? msg : adminStopText();
  } catch {
    return null;
  }
}
