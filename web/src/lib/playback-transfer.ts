// Receiver side of "Play on…" (playback.transfer SSE event, see
// $lib/play-on for the sender). Pure — the layout subscription only fetches
// the item and executes the action returned here, so the routing and the
// start-position handling are unit-tested directly.

import type { AudioTrack } from '$lib/stores/audio';

/** Item types the receiver starts in the audio player; everything else
 *  opens the watch page. */
export const TRANSFER_AUDIO_TYPES: ReadonlySet<string> = new Set(['track', 'audiobook', 'audiobook_chapter']);

/** The fields of an ItemDetail the receiver needs. */
export interface TransferItemDetail {
  id: string;
  type: string;
  title: string;
  duration_ms?: number | null;
  poster_path?: string | null;
  files?: { id: string }[];
}

export type TransferAction =
  | { kind: 'audio'; track: AudioTrack; startMS: number }
  | { kind: 'watch'; href: string };

/** Normalise a transfer's position_ms into a usable start offset: a
 *  non-negative integer, 0 for garbage, and 0 when it sits at or past the
 *  known end (starting there would finish instantly). */
export function transferStartMs(positionMs: unknown, durationMs?: number | null): number {
  const v = typeof positionMs === 'number' && Number.isFinite(positionMs) && positionMs > 0 ? Math.round(positionMs) : 0;
  if (durationMs && durationMs > 0 && v >= durationMs) return 0;
  return v;
}

/** What to do with a transfer aimed at this device: play audio at the sent
 *  position, or open the watch page with `?at=<ms>` (the watch page resumes
 *  there — see parseAtParam). */
export function transferAction(detail: TransferItemDetail, positionMs: number): TransferAction {
  if (TRANSFER_AUDIO_TYPES.has(detail.type)) {
    return {
      kind: 'audio',
      track: {
        id: detail.id,
        fileId: detail.files?.[0]?.id ?? '',
        title: detail.title,
        artist: undefined,
        album: undefined,
        durationMS: detail.duration_ms ?? undefined,
        posterPath: detail.poster_path ?? undefined,
      },
      startMS: transferStartMs(positionMs, detail.duration_ms),
    };
  }
  const at = transferStartMs(positionMs);
  return { kind: 'watch', href: `/watch/${detail.id}${at > 0 ? `?at=${at}` : ''}` };
}

/** The watch page's `?at=<ms>` start override (set by a transfer): a
 *  positive integer, else null. */
export function parseAtParam(value: string | null | undefined): number | null {
  if (!value || !/^\d+$/.test(value)) return null;
  const n = Number(value);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}
