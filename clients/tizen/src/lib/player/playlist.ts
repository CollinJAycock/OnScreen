// What a server session's HLS playlist says about the stream, for AVPlay,
// which plays the playlist itself and reports none of it. The player decides
// whether a seek stays in the running session or re-opens one at the target
// from how far the session has got (session.ts planContentSeek, as the webOS
// app does from hls.js's parsed playlist), and the item's end from whether
// the playlist is complete. Pure: the adapter (avplayMedia.ts) fetches.

/** A media playlist's span: how much has been written (seconds, from its
 *  first segment), and whether it is complete (#EXT-X-ENDLIST). */
export interface PlaylistSpan {
  producedEndSec: number;
  ended: boolean;
}

/** The first variant of a master playlist, resolved against the master's
 *  URL; null when `text` is a media playlist (or no playlist at all). */
export function firstVariantUrl(text: string, baseUrl: string): string | null {
  const lines = text.split(/\r?\n/).map((l) => l.trim());
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].startsWith('#EXT-X-STREAM-INF')) continue;
    for (let j = i + 1; j < lines.length; j++) {
      const uri = lines[j];
      if (!uri || uri.startsWith('#')) continue;
      return resolveUrl(uri, baseUrl);
    }
  }
  return null;
}

/** A media playlist's span; null for anything else (a master playlist, an
 *  error page). */
export function mediaPlaylistSpan(text: string): PlaylistSpan | null {
  if (!/^#EXTM3U/m.test(text) || /#EXT-X-STREAM-INF/.test(text)) return null;
  let total = 0;
  let any = false;
  // An exec loop, not matchAll (Chromium 73; Tizen 5.5 runs 69).
  const re = /^#EXTINF:\s*([0-9.]+)/gm;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    const d = Number(m[1]);
    if (Number.isFinite(d) && d > 0) {
      total += d;
      any = true;
    }
  }
  const ended = /^#EXT-X-ENDLIST/m.test(text);
  if (!any && !ended) return { producedEndSec: 0, ended: false };
  return { producedEndSec: Math.round(total * 1000) / 1000, ended };
}

/** Whether a media playlist plays from its first segment: an EVENT or VOD
 *  playlist, or one that's complete. A live sliding window (no type, no
 *  ENDLIST) plays from its live edge. */
export function startsAtBeginning(text: string): boolean {
  return /^#EXT-X-PLAYLIST-TYPE:\s*(EVENT|VOD)\s*$/m.test(text) || /^#EXT-X-ENDLIST/m.test(text);
}

/** `uri` against `base`, keeping base's query (the session token rides
 *  there) only when `uri` has none of its own. */
export function resolveUrl(uri: string, base: string): string {
  try {
    const u = new URL(uri, base);
    if (!u.search) u.search = new URL(base).search;
    return u.toString();
  } catch {
    return uri;
  }
}
