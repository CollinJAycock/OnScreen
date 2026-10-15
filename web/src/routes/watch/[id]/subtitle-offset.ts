// Subtitle timing: the player's session-only adjustment and the server-wide
// offset an admin can save from it. The server shifts the cues it serves by
// its offset, so the player only ever applies the local adjustment on top —
// never the server offset again.

/** The server's bound on a stored offset either way (10 minutes). */
export const MAX_OFFSET_MS = 600_000;

/** The steps the sync control offers, in milliseconds. */
export const OFFSET_STEPS_MS = [-1000, -100, 100, 1000] as const;

function clampMs(ms: number): number {
  return Math.max(-MAX_OFFSET_MS, Math.min(MAX_OFFSET_MS, Math.round(ms)));
}

/** The local adjustment (seconds, positive = later) after one step. Kept on
 *  whole milliseconds so repeated 0.1 s steps don't drift into 0.30000000004. */
export function stepDelay(delaySec: number, stepMs: number): number {
  return clampMs(delaySec * 1000 + stepMs) / 1000;
}

/** "+1.2 s", "−0.3 s", "0.0 s" (one decimal, a real minus sign). */
export function formatOffset(ms: number): string {
  const v = Math.round(ms / 100) / 10;
  if (v === 0) return '0.0 s';
  return `${v > 0 ? '+' : '−'}${Math.abs(v).toFixed(1)} s`;
}

/** The server offset to save so the cues land where the local adjustment
 *  puts them now: the current server offset plus the adjustment, clamped to
 *  what the server accepts. */
export function offsetToSave(serverMs: number, delaySec: number): number {
  return clampMs(serverMs + delaySec * 1000);
}
