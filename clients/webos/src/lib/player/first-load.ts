// The first load of a server session: hls.js is built with autoStartLoad off
// (hlsSessionConfig), and the load starts here once the session's first
// playlist is parsed, at a position the page picks from it
// (firstLoadPositionSec: a start at the edge of a growing playlist begins at
// the start of the segment it falls in instead of waiting a whole live
// reload with nothing on screen).
//
// The hard part is making that start stick. hls.js dispatches
// MANIFEST_PARSED synchronously from LevelController's MANIFEST_LOADED
// listener, and its own autostart listener for MANIFEST_LOADED is
// registered last (hls.ts, end of the constructor). Turning autoStartLoad
// back on inside MANIFEST_PARSED (where a media-error recovery's re-attach
// needs it from then on) let that autostart run right after ours and call
// startLoad(config.startPosition), replacing the early start: resuming 1917
// still waited ~27 s for its first frame, while the bar had already been
// moved back to the early start's position. So autoStartLoad comes back on
// only once the whole dispatch is over (a microtask), and the page hears of
// an early start only if hls.js really kept it (its startPosition getter
// reads back what the stream controller will load from).
//
// The wiring is tested against the real hls.js (first-load.test.ts), so an
// hls.js upgrade that reorders those listeners fails a test, not a TV.

import type HlsType from 'hls.js';
import type { FirstPlaylist } from './session';

/** An hls.js instance (the parts armFirstLoad touches). */
export type FirstLoadHls = Pick<InstanceType<typeof HlsType>, 'once' | 'startLoad' | 'config' | 'startPosition'>;

export interface FirstLoadOptions {
  /** The start the session was built with (hls.js's config.startPosition,
   *  stream seconds or -1). */
  startSec: number;
  /** Where to start loading, given the first playlist's parsed details
   *  (undefined for a multivariant playlist, whose level isn't loaded yet).
   *  Returning `startSec` is a plain start. */
  choose: (playlist: FirstPlaylist | undefined) => number;
  /** The instance is still the one the page plays: a replaced one starts
   *  nothing and reports nothing. */
  current: () => boolean;
  /** hls.js kept an early start: loading begins at `at` (stream seconds),
   *  not at `startSec`, so the playhead belongs there. Not called when the
   *  start is `startSec`, or when hls.js replaced it. */
  onEarlyStart: (at: number) => void;
}

/** hls.js stores the number it was given; this only absorbs float noise. */
const SAME_POSITION_SEC = 0.001;

/** Starts the session's loading once its first playlist is parsed. Call it
 *  before loadSource(), on an instance built with autoStartLoad off. */
export function armFirstLoad(inst: FirstLoadHls, Hls: typeof HlsType, opts: FirstLoadOptions): void {
  inst.once(Hls.Events.MANIFEST_PARSED, (_event, data) => {
    if (!opts.current()) return;
    const at = opts.choose(data.levels[0]?.details);
    inst.startLoad(at);
    // After the rest of this MANIFEST_LOADED dispatch (hls.js's autostart
    // listener among it) has run; before any other task (the media
    // element's sourceopen, a timer) can.
    queueMicrotask(() => {
      // From now on hls.js restarts loading by itself again: a media-error
      // recovery's re-attach does so only with autoStartLoad on (at
      // config.startPosition, the requested start). hls.js reads its config
      // live (it lowers maxMaxBufferLength the same way on a full buffer).
      inst.config.autoStartLoad = true;
      if (!opts.current() || at === opts.startSec || at < 0) return;
      if (Math.abs(inst.startPosition - at) < SAME_POSITION_SEC) opts.onEarlyStart(at);
    });
  });
}
