// Server-side audiobook speed, made to look like the element's own.
//
// Samsung's webview plays media at 1x whatever playbackRate says (measured on
// a 2022 Q80B: <audio> and <video> alike). So on this TV a book at 1.5x plays
// a server session the server has sped up (transcode start `audio_rate`,
// ffmpeg atempo): its stream runs at content / rate. The player, though, works
// in content time throughout (session.ts: content = stream + offsetMs, the
// seek window, progress, the scrubber, chapters). This wraps the page's
// element so that, while a sped session plays, the element's clock reads in
// content-relative seconds again: currentTime, duration, seekable and
// buffered are multiplied by the rate, a seek is divided by it, and
// playbackRate reads as the rate the stream already plays at (the element
// itself stays at 1x). With no server rate it is the element, unchanged.

export interface TempoMedia {
  /** The element as the page uses it. */
  readonly media: HTMLVideoElement;
  /** The rate the current stream was sped up by on the server; null for a
   *  normal stream (direct play, or a session at 1x). */
  setServerRate(rate: number | null): void;
  readonly serverRate: number | null;
}

/** A TimeRanges whose times are multiplied by `k`. */
function scaledRanges(r: TimeRanges, k: number): TimeRanges {
  return {
    length: r.length,
    start: (i: number) => r.start(i) * k,
    end: (i: number) => r.end(i) * k,
  } as unknown as TimeRanges;
}

export function createTempoMedia(el: HTMLVideoElement): TempoMedia {
  let rate: number | null = null;
  // What the page set as playbackRate while a sped stream plays.
  let virtualRate = 1;
  let virtualDefaultRate = 1;

  const media = new Proxy(el, {
    get(target, prop) {
      if (rate !== null) {
        switch (prop) {
          case 'currentTime':
            return target.currentTime * rate;
          case 'duration':
            return target.duration * rate;
          case 'seekable':
            return scaledRanges(target.seekable, rate);
          case 'buffered':
            return scaledRanges(target.buffered, rate);
          case 'playbackRate':
            return virtualRate;
          case 'defaultPlaybackRate':
            return virtualDefaultRate;
        }
      }
      const v = Reflect.get(target, prop, target);
      return typeof v === 'function' ? v.bind(target) : v;
    },
    set(target, prop, value) {
      if (rate !== null) {
        switch (prop) {
          case 'currentTime':
            target.currentTime = Number(value) / rate;
            return true;
          case 'playbackRate':
            virtualRate = Number(value);
            return true;
          case 'defaultPlaybackRate':
            virtualDefaultRate = Number(value);
            return true;
        }
      }
      return Reflect.set(target, prop, value, target);
    },
  }) as HTMLVideoElement;

  return {
    media,
    get serverRate() {
      return rate;
    },
    setServerRate(r: number | null) {
      const next = r !== null && Number.isFinite(r) && r > 0 && Math.abs(r - 1) > 0.001 ? r : null;
      if (next !== null) {
        // The stream is already sped: the element plays it at 1x, and the
        // page sees the rate it plays at.
        try {
          el.playbackRate = 1;
          el.defaultPlaybackRate = 1;
        } catch { /* some engines throw on an unloaded element */ }
        virtualRate = next;
        virtualDefaultRate = next;
      }
      rate = next;
    },
  };
}
