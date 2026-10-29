// "Is the browser actually decoding the video?" — extracted from the watch
// page so the rules are unit-testable without mounting the player.
//
// The player used to read videoWidth once, at loadedmetadata, and treat 0 as
// "this browser can't decode the codec" — escalating direct play → remux →
// full transcode. WebKit reports videoWidth 0 at loadedmetadata for an MSE
// stream until it has decoded a frame, so on Safari every HLS start looked
// undecodable: the remux was abandoned, then each H.264 transcode was too, and
// every restart re-armed the check. The loop ran until the server's
// transcode-start rate limit answered 429 ("rate limit exceeded").
//
// watchForPicture waits for evidence instead of trusting one event:
//  - a picture (videoWidth > 0 on any watched event)       → 'picture';
//  - playback running without one: NO_PICTURE_PLAYED_SEC of
//    real playback (audio plays, the video doesn't)        → 'no-picture';
//  - nothing at all: NO_PICTURE_GRACE_MS of trying to play
//    with neither a picture nor progress                   → 'no-picture'.
// Time spent paused doesn't count: a blocked autoplay hasn't asked for a
// frame yet, so the grace timer only runs between `playing` and `pause`.

export type PictureVerdict = 'picture' | 'no-picture';

export const NO_PICTURE_GRACE_MS = 8000;
export const NO_PICTURE_PLAYED_SEC = 2;

// A timeupdate step larger than this is a seek (the resume jump, a scrub), not
// playback, and doesn't count toward NO_PICTURE_PLAYED_SEC.
const MAX_PLAYBACK_STEP_SEC = 1.5;

const EVENTS = ['resize', 'loadeddata', 'playing', 'pause', 'timeupdate'] as const;

/** watchForPicture calls onVerdict exactly once, unless cancelled first.
 *  Returns the cancel function; cancelling after the verdict is a no-op. */
export function watchForPicture(
  video: HTMLVideoElement,
  onVerdict: (v: PictureVerdict) => void,
  graceMs: number = NO_PICTURE_GRACE_MS,
): () => void {
  let done = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let lastCt = video.currentTime;
  let played = 0;

  const stopTimer = () => {
    clearTimeout(timer);
    timer = undefined;
  };
  const cancel = () => {
    done = true;
    stopTimer();
    for (const ev of EVENTS) video.removeEventListener(ev, onEvent);
  };
  const finish = (v: PictureVerdict) => {
    if (done) return;
    cancel();
    onVerdict(v);
  };
  const armTimer = () => {
    stopTimer();
    if (video.paused) return; // re-armed by the next `playing`
    timer = setTimeout(() => {
      timer = undefined;
      if (video.videoWidth > 0) finish('picture');
      else if (!video.paused) finish('no-picture');
    }, graceMs);
  };
  function onEvent(e: Event) {
    if (video.videoWidth > 0) {
      finish('picture');
      return;
    }
    switch (e.type) {
      case 'playing':
        armTimer();
        break;
      case 'pause':
        stopTimer();
        break;
      case 'timeupdate': {
        const step = video.currentTime - lastCt;
        lastCt = video.currentTime;
        if (step > 0 && step <= MAX_PLAYBACK_STEP_SEC) played += step;
        if (played >= NO_PICTURE_PLAYED_SEC) finish('no-picture');
        break;
      }
    }
  }

  if (video.videoWidth > 0) {
    // Already decoding: answer asynchronously, like every other verdict.
    queueMicrotask(() => finish('picture'));
    return cancel;
  }
  for (const ev of EVENTS) video.addEventListener(ev, onEvent);
  armTimer();
  return cancel;
}
