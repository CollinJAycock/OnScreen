import { describe, expect, it } from 'vitest';
import { NativeVideo, onHomeNetwork, type NativeTransport } from './nativeVideo';
import type { NativeEvent } from '../shell';

/** The last command posted (the TV apps target ES2020: no Array#at). */
const last = <T,>(xs: T[]): T | undefined => xs[xs.length - 1];

/** A shell stand-in: what was posted, and a way to answer. */
function fakeShell() {
  const posted: Record<string, unknown>[] = [];
  let listener: ((e: NativeEvent) => void) | null = null;
  const transport: NativeTransport = {
    post: (c) => (posted.push(c), true),
    subscribe: (fn) => ((listener = fn), () => (listener = null)),
  };
  const send = (ev: string, gen: number, extra: Partial<NativeEvent> = {}) =>
    listener?.({ ev, gen, t: 0, d: 120, ...extra });
  return { posted, transport, send };
}

function recorder(v: NativeVideo) {
  const seen: string[] = [];
  for (const t of ['emptied', 'loadstart', 'durationchange', 'loadedmetadata', 'loadeddata', 'canplay', 'play', 'playing', 'pause', 'waiting', 'seeking', 'seeked', 'timeupdate', 'ended', 'error', 'ratechange']) {
    v.addEventListener(t, () => seen.push(t));
  }
  return seen;
}

describe('NativeVideo', () => {
  it('loads with the HDR hint and plays events back in browser order', async () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    const seen = recorder(v);
    v.hdr = true;
    v.src = 'https://srv/media/stream/f1';
    expect(shell.posted).toEqual([{ op: 'load', url: 'https://srv/media/stream/f1', gen: 1, hdr: true, dv: false }]);
    expect(v.paused).toBe(true);
    expect(v.readyState).toBe(0);

    shell.send('loadedmetadata', 1);
    expect(v.duration).toBe(120);
    expect(v.readyState).toBe(4);
    await v.play();
    shell.send('state', 1, { state: 'playing' });
    shell.send('time', 1, { t: 1.5 });
    expect(v.currentTime).toBe(1.5);
    expect(seen).toEqual(['emptied', 'loadstart', 'durationchange', 'loadedmetadata', 'loadeddata', 'canplay', 'play', 'playing', 'timeupdate']);
    expect(last(shell.posted)).toEqual({ op: 'play' });
  });

  it("drops a replaced source's late events", () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    v.src = 'a';
    v.src = 'b';
    const seen = recorder(v);
    shell.send('loadedmetadata', 1);
    shell.send('ended', 1);
    expect(seen).toEqual([]);
    shell.send('loadedmetadata', 2);
    expect(seen).toContain('loadedmetadata');
  });

  it('seeks, holding the target until the shell has got there', () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    v.src = 'a';
    shell.send('loadedmetadata', 1);
    const seen = recorder(v);
    v.currentTime = 42;
    expect(last(shell.posted)).toEqual({ op: 'seek', t: 42 });
    expect(v.seeking).toBe(true);
    shell.send('time', 1, { t: 3 });
    expect(v.currentTime).toBe(42);
    shell.send('seeked', 1, { t: 42.04 });
    expect(v.seeking).toBe(false);
    expect(v.currentTime).toBeCloseTo(42.04);
    expect(seen).toEqual(['seeking', 'seeked', 'timeupdate']);
  });

  it('ends as a browser does: pause, then ended, at the duration', async () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    v.src = 'a';
    shell.send('loadedmetadata', 1);
    await v.play();
    const seen = recorder(v);
    shell.send('ended', 1, { t: 119.9 });
    expect(v.ended).toBe(true);
    expect(v.paused).toBe(true);
    expect(v.currentTime).toBe(120);
    expect(seen).toEqual(['timeupdate', 'pause', 'ended']);
  });

  it('pauses once, whoever asks first', async () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    v.src = 'a';
    shell.send('loadedmetadata', 1);
    // The shell sits paused before the first play: no 'pause'.
    shell.send('state', 1, { state: 'paused' });
    await v.play();
    const seen = recorder(v);
    v.pause();
    shell.send('state', 1, { state: 'paused' });
    expect(seen).toEqual(['pause']);
    // A pause from the console itself (the Guide) reads as one too.
    await v.play();
    shell.send('state', 1, { state: 'playing' });
    shell.send('state', 1, { state: 'paused' });
    expect(seen.filter((s) => s === 'pause')).toHaveLength(2);
  });

  it("reports a failure with HTMLMediaElement's code", () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    v.src = 'a';
    const seen = recorder(v);
    shell.send('error', 1, { code: 4, msg: 'SourceNotSupported' });
    expect(v.error).toEqual({ code: 4, message: 'SourceNotSupported' });
    expect(seen).toEqual(['error']);
  });

  it('releases the source on removeAttribute and says when it is active', () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    const active: boolean[] = [];
    v.onActive = (a) => active.push(a);
    v.src = 'a';
    expect(v.getAttribute('src')).toBe('a');
    v.removeAttribute('src');
    v.load();
    expect(v.getAttribute('src')).toBeNull();
    expect(last(shell.posted)).toEqual({ op: 'stop' });
    expect(active).toEqual([true, false]);
  });

  it('takes HLS and files the console plays, and passes the speed on', () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    expect(v.canPlayType('application/vnd.apple.mpegurl')).toBe('maybe');
    expect(v.canPlayType('video/x-matroska')).toBe('maybe');
    expect(v.canPlayType('video/webm')).toBe('');
    v.playbackRate = 1.5;
    expect(last(shell.posted)).toEqual({ op: 'rate', rate: 1.5 });
  });

  it('exposes the seekable and buffered ranges the shell reports', () => {
    const shell = fakeShell();
    const v = new NativeVideo(shell.transport);
    v.src = 'a';
    expect(v.seekable.length).toBe(0);
    shell.send('time', 1, { t: 2, s: [0, 96.5], b: [0, 10, 20, 30] });
    expect(v.seekable.length).toBe(1);
    expect(v.seekable.end(0)).toBe(96.5);
    expect(v.buffered.length).toBe(2);
    expect(v.buffered.start(1)).toBe(20);
    v.src = 'b';
    expect(v.seekable.length).toBe(0);
  });
});

describe('onHomeNetwork', () => {
  it('is true for private, loopback and local names', () => {
    for (const o of ['http://10.0.0.66:7070', 'http://192.168.1.10', 'https://172.20.1.2', 'http://127.0.0.1:7070', 'http://localhost:7070', 'http://nas.local', 'http://truenas:7070', 'http://[fd12::1]:7070', 'http://[::1]']) {
      expect(onHomeNetwork(o), o).toBe(true);
    }
  });

  it('is false for the internet and for junk', () => {
    for (const o of ['https://onscreen.wolverscreen.com', 'https://8.8.8.8', 'http://172.32.0.1', 'http://[2001:db8::1]', 'not a url', '']) {
      expect(onHomeNetwork(o), o).toBe(false);
    }
  });
});
