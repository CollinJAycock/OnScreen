import { describe, expect, it, vi } from 'vitest';
import type { AvPlayApi, AvPlayListener } from './avplay';
import {
  AvplayMedia,
  HAVE_ENOUGH_DATA,
  MEDIA_ERR_NETWORK,
  MEDIA_ERR_SRC_NOT_SUPPORTED,
  PLAYLIST_POLL_MS,
  errorCode,
} from './avplayMedia';

// A stand-in for webapis.avplay: records calls, and lets a test drive the
// listener and the prepare / seek callbacks as the firmware would.
function fakeAvplay() {
  let listener: AvPlayListener = {};
  let prepared: { ok: () => void; fail: (e: unknown) => void } | null = null;
  const seeks: { ms: number; ok?: () => void }[] = [];
  const calls: string[] = [];
  const api: AvPlayApi = {
    open: (url) => void calls.push(`open ${url}`),
    setListener: (l) => void (listener = l),
    setStreamingProperty: (k, v) => void calls.push(`prop ${k}=${v}`),
    setDisplayRect: (x, y, w, h) => void calls.push(`rect ${x},${y},${w},${h}`),
    setDisplayMethod: () => {},
    prepare: () => {},
    prepareAsync: (ok, fail) => void (prepared = { ok: ok ?? (() => {}), fail: fail ?? (() => {}) }),
    play: () => void calls.push('play'),
    pause: () => void calls.push('pause'),
    stop: () => void calls.push('stop'),
    close: () => void calls.push('close'),
    seekTo: (ms, ok) => void seeks.push({ ms, ok }),
    getCurrentTime: () => 0,
    getDuration: () => 0,
    getState: () => 'PLAYING',
  };
  return {
    api,
    calls,
    seeks,
    get listener() {
      return listener;
    },
    prepare: () => prepared?.ok(),
    failPrepare: (e: unknown) => prepared?.fail(e),
  };
}

const PLAYLIST = '#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\na.ts\n#EXTINF:4.0,\nb.ts\n';

function setup(playlist = PLAYLIST) {
  const av = fakeAvplay();
  let clock = 0;
  const timers: { fn: () => void; ms: number }[] = [];
  const fetchText = vi.fn(async () => playlist);
  const media = new AvplayMedia({
    api: av.api,
    anchor: () => ({ offsetLeft: 0, offsetTop: 0, offsetWidth: 1920, offsetHeight: 1080 }) as HTMLElement,
    fetchText,
    now: () => clock,
    setTimer: (fn, ms) => timers.push({ fn, ms }),
    clearTimer: () => {},
  });
  const events: string[] = [];
  for (const t of ['loadstart', 'loadedmetadata', 'play', 'playing', 'pause', 'waiting', 'canplay', 'seeking', 'seeked', 'timeupdate', 'ended', 'error', 'emptied', 'durationchange']) {
    media.addEventListener(t, () => events.push(t));
  }
  return { av, media, events, timers, fetchText, tick: (ms: number) => (clock += ms) };
}

const URL = 'https://tv.example/api/v1/transcode/sessions/s1/index.m3u8?token=t';

/** Lets the first playlist read (a resolved promise here) answer. */
async function flush() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

describe('AvplayMedia', () => {
  it('opens a source and reports its metadata once AVPlay has prepared it, without playing', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    expect(av.calls).toContain(`open ${URL}`);
    expect(av.calls).toContain('prop ADAPTIVE_INFO=STREAMING_FORMAT=HLS');
    expect(av.calls).toContain('rect 0,0,1920,1080');
    expect(events).toEqual(['loadstart']);
    av.prepare();
    expect(events).toContain('loadedmetadata');
    expect(av.calls).not.toContain('play');
    expect(media.paused).toBe(true);
  });

  it('tells AVPlay to play once, whether play() comes before the prepare or from loadedmetadata', async () => {
    const early = setup();
    early.media.src = URL;
    await flush();
    await early.media.play();
    expect(early.av.calls.filter((c) => c === 'play')).toHaveLength(0);
    early.av.prepare();
    expect(early.av.calls.filter((c) => c === 'play')).toHaveLength(1);

    // The page's way: play() from its 'loadedmetadata' listener.
    const late = setup();
    late.media.addEventListener('loadedmetadata', () => void late.media.play());
    late.media.src = URL;
    await flush();
    late.av.prepare();
    expect(late.av.calls.filter((c) => c === 'play')).toHaveLength(1);
  });

  it('plays and pauses with the element events', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    await media.play();
    expect(av.calls).toContain('play');
    expect(media.paused).toBe(false);
    av.listener.onbufferingcomplete?.();
    expect(events).toContain('playing');
    media.pause();
    expect(av.calls).toContain('pause');
    expect(events[events.length - 1]).toBe('pause');
  });

  it('holds a seek made before the first frame and applies it then (seekTo that early is unreliable)', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    media.currentTime = 42.5;
    expect(av.seeks).toEqual([]);
    expect(media.currentTime).toBe(42.5);
    await media.play();
    // The firmware's first tick still reads the start: not reported.
    av.listener.oncurrentplaytime?.(0);
    expect(av.seeks.map((s) => s.ms)).toEqual([42500]);
    expect(events).toContain('seeking');
    expect(media.currentTime).toBe(42.5);
    av.seeks[0].ok?.();
    expect(events).toContain('seeked');
    expect(media.seeking).toBe(false);
  });

  it('seeks at once once playing, with seeking / seeked around it', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    await media.play();
    av.listener.onbufferingcomplete?.();
    media.currentTime = 10;
    expect(av.seeks.map((s) => s.ms)).toEqual([10000]);
    expect(media.seeking).toBe(true);
    av.seeks[0].ok?.();
    expect(events.slice(-3)).toEqual(['timeupdate', 'seeked', 'playing']);
  });

  it("reports AVPlay's clock as currentTime, at most every 200 ms", async () => {
    const { av, media, events, tick } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    await media.play();
    av.listener.oncurrentplaytime?.(1000);
    tick(100);
    av.listener.oncurrentplaytime?.(1100);
    expect(media.currentTime).toBe(1.1);
    expect(media.readyState).toBe(HAVE_ENOUGH_DATA);
    expect(events.filter((e) => e === 'timeupdate')).toHaveLength(1);
    tick(150);
    av.listener.oncurrentplaytime?.(1250);
    expect(events.filter((e) => e === 'timeupdate')).toHaveLength(2);
  });

  it('rebuffers: waiting, then canplay and playing', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    await media.play();
    av.listener.onbufferingcomplete?.();
    av.listener.onbufferingstart?.();
    expect(events[events.length - 1]).toBe('waiting');
    expect(media.readyState).toBeLessThan(3);
    av.listener.onbufferingcomplete?.();
    expect(events.slice(-2)).toEqual(['canplay', 'playing']);
  });

  it('ends: pause, then ended, as the element does', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    await media.play();
    av.listener.onstreamcompleted?.();
    expect(media.ended).toBe(true);
    expect(media.paused).toBe(true);
    expect(events.slice(-2)).toEqual(['pause', 'ended']);
  });

  it('reads the growing playlist for seekable and duration, until ENDLIST', async () => {
    const { av, media, timers, fetchText } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    await Promise.resolve();
    await Promise.resolve();
    expect(fetchText).toHaveBeenCalledWith(URL);
    expect(media.seekable.length).toBe(1);
    expect(media.seekable.end(0)).toBe(8);
    expect(media.duration).toBe(Infinity);
    expect(timers[timers.length - 1].ms).toBe(PLAYLIST_POLL_MS);
    fetchText.mockResolvedValue(`${PLAYLIST}#EXTINF:2.5,\nc.ts\n#EXT-X-ENDLIST\n`);
    timers[timers.length - 1].fn();
    await Promise.resolve();
    await Promise.resolve();
    expect(media.duration).toBe(10.5);
    expect(media.seekable.end(0)).toBe(10.5);
  });

  it('turns an AVPlay failure into a media error the page can act on', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    av.failPrepare('PLAYER_ERROR_NOT_SUPPORTED_FILE');
    expect(events).toContain('error');
    expect(media.error?.code).toBe(MEDIA_ERR_SRC_NOT_SUPPORTED);
  });

  it('closes AVPlay when the source is dropped, and ignores the old source after', async () => {
    const { av, media, events } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    const old = av.listener;
    media.removeAttribute('src');
    media.load();
    expect(av.calls.slice(-2)).toEqual(['stop', 'close']);
    expect(media.getAttribute('src')).toBeNull();
    expect(events).toContain('emptied');
    const before = events.length;
    old.onstreamcompleted?.();
    old.onerror?.('late');
    expect(events.length).toBe(before);
  });

  it("prepares AVPlay only once the session's playlist has answered (AVPlay times out first)", async () => {
    const { av, media } = setup();
    let answer: (s: string) => void = () => {};
    const prepares: number[] = [];
    const prepareAsync = av.api.prepareAsync;
    av.api.prepareAsync = (ok, fail) => {
      prepares.push(1);
      prepareAsync(ok, fail);
    };
    const slow = new Promise<string>((r) => (answer = r));
    (media as unknown as { fetchText: (u: string) => Promise<string> }).fetchText = () => slow;
    media.src = URL;
    await flush();
    expect(av.calls).toContain(`open ${URL}`);
    expect(prepares).toHaveLength(0); // the server is still waiting on segment 0
    answer(PLAYLIST);
    await flush();
    expect(prepares).toHaveLength(1);
  });

  it('prepares AVPlay even when the playlist read fails, so AVPlay reports the failure', async () => {
    const { av, media, events } = setup();
    (media as unknown as { fetchText: (u: string) => Promise<string> }).fetchText = () => Promise.reject(new Error('503'));
    media.src = URL;
    await flush();
    av.failPrepare('PLAYER_ERROR_CONNECTION_FAILED');
    expect(events).toContain('error');
    expect(media.error?.code).toBe(MEDIA_ERR_NETWORK);
  });

  it('prepares a file source at once, and never prepares a source replaced during its read', async () => {
    const file = setup();
    const prepared: string[] = [];
    const orig = file.av.api.prepareAsync;
    file.av.api.prepareAsync = (ok, fail) => {
      prepared.push(file.media.src);
      orig(ok, fail);
    };
    file.media.src = 'https://tv.example/media/files/f1';
    expect(prepared).toEqual(['https://tv.example/media/files/f1']);

    const { av, media } = setup();
    const seen: string[] = [];
    const orig2 = av.api.prepareAsync;
    av.api.prepareAsync = (ok, fail) => {
      seen.push(media.src);
      orig2(ok, fail);
    };
    let answerOld: (s: string) => void = () => {};
    let first = true;
    (media as unknown as { fetchText: (u: string) => Promise<string> }).fetchText = () =>
      first ? ((first = false), new Promise<string>((r) => (answerOld = r))) : Promise.resolve(PLAYLIST);
    media.src = URL;
    const next = 'https://tv.example/api/v1/transcode/sessions/s2/index.m3u8?token=t';
    media.src = next;
    await flush();
    answerOld(PLAYLIST);
    await flush();
    expect(seen).toEqual([next]);
  });

  it("starts a session at its beginning, not at the playlist's live edge where AVPlay opens it", async () => {
    const { av, media, events } = setup();
    av.api.getCurrentTime = () => 24_000; // AVPlay began 3 segments from the end
    media.src = URL;
    await flush();
    av.prepare();
    await media.play();
    expect(media.currentTime).toBe(0);
    av.listener.oncurrentplaytime?.(24_000);
    expect(av.seeks.map((s) => s.ms)).toEqual([0]);
    expect(events).toContain('seeking');
    av.seeks[0].ok?.();
    expect(media.seeking).toBe(false);
    expect(media.currentTime).toBe(0);
  });

  it('does not seek a session that opened at its start', async () => {
    const { av, media } = setup();
    media.src = URL;
    await flush();
    av.prepare();
    await media.play();
    av.listener.oncurrentplaytime?.(40);
    expect(av.seeks).toEqual([]);
    expect(media.currentTime).toBe(0.04);
  });

  it('says it plays HLS only where AVPlay is', () => {
    expect(setup().media.canPlayType('application/vnd.apple.mpegurl')).toBe('maybe');
    const none = new AvplayMedia({ api: null, anchor: () => null });
    expect(none.canPlayType('application/vnd.apple.mpegurl')).toBe('');
  });
});

describe('errorCode', () => {
  it('reads connection failures as network errors, the rest as not supported', () => {
    expect(errorCode('PLAYER_ERROR_CONNECTION_FAILED')).toBe(MEDIA_ERR_NETWORK);
    expect(errorCode({ name: 'PLAYER_ERROR_NETWORK' })).toBe(MEDIA_ERR_NETWORK);
    expect(errorCode('PLAYER_ERROR_NOT_SUPPORTED_FILE')).toBe(MEDIA_ERR_SRC_NOT_SUPPORTED);
    expect(errorCode('PLAYER_ERROR_INVALID_URI')).toBe(MEDIA_ERR_SRC_NOT_SUPPORTED);
  });
});
