// Helpers for the "Play on…" sender (PlayOnButton.svelte). Pure — no DOM,
// no API — so the list shaping and labels are unit-tested directly.
//
// Server contract (internal/api/v1/playback.go):
//   GET  /playback/devices  -> [{client_name, last_seen}] — distinct client
//        names this user has reported progress from in the last 30 days,
//        most recent first. A device shows up once it has played something.
//   POST /playback/transfer {item_id, target_client_name, position_ms}
//        -> 204. Fire-and-forget: the server fans a playback.transfer event
//        out over the user's SSE stream and the client whose own name
//        matches starts playback. Nothing confirms receipt, so the UI says
//        "Sent to …", not "Playing on …".

export interface PlaybackDevice {
  client_name: string;
  last_seen: string;
}

/** Devices the user can send to: everything except this browser (matched
 *  by the exact client_name it reports — sending to yourself would just
 *  restart playback here), de-duplicated, most recently seen first. */
export function otherDevices(
  list: readonly PlaybackDevice[] | null | undefined,
  ownName: string,
): PlaybackDevice[] {
  const seen = new Set<string>();
  const out: PlaybackDevice[] = [];
  for (const d of list ?? []) {
    const name = d?.client_name;
    if (!name || name.trim() === '' || name === ownName || seen.has(name)) continue;
    seen.add(name);
    out.push(d);
  }
  const ts = (d: PlaybackDevice) => {
    const t = Date.parse(d.last_seen);
    return Number.isFinite(t) ? t : 0;
  };
  // Array.prototype.sort is stable, so equal/unparseable timestamps keep
  // the server's order.
  return out.sort((a, b) => ts(b) - ts(a));
}

/** "just now" / "5 min ago" / "3 hr ago" / "yesterday" / "4 days ago" /
 *  "2 weeks ago". Empty string for an unparseable timestamp. */
export function formatLastSeen(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return '';
  const sec = Math.max(0, Math.floor((now - t) / 1000));
  if (sec < 60) return 'just now';
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} min ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} hr ago`;
  const day = Math.floor(hr / 24);
  if (day === 1) return 'yesterday';
  if (day < 7) return `${day} days ago`;
  const wk = Math.floor(day / 7);
  return wk === 1 ? '1 week ago' : `${wk} weeks ago`;
}

/** Normalise a playhead reading into the non-negative integer ms the
 *  transfer endpoint takes. A throwing / NaN getter sends 0 (start). */
export function transferPositionMs(get: (() => number) | undefined): number {
  let v = 0;
  try {
    v = get ? get() : 0;
  } catch {
    v = 0;
  }
  return Number.isFinite(v) && v > 0 ? Math.round(v) : 0;
}
