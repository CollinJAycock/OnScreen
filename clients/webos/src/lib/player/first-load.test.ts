// armFirstLoad against the REAL hls.js (the version the app ships), driven
// by a fake playlist loader: the bug this guards was in the wiring (hls.js's
// own autostart listener runs after MANIFEST_PARSED in the same dispatch and
// replaced the early start), which no test of firstLoadPositionSec alone
// could see. The decision is the page's real one, firstLoadPositionSec.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Hls from 'hls.js';
import { armFirstLoad } from './first-load';
import { firstLoadPositionSec, hlsSessionConfig } from './session';

// hls.js resolves the source URL against self.location (a browser global).
beforeEach(() => {
  vi.stubGlobal('self', globalThis);
  vi.stubGlobal('location', { href: 'http://onscreen.test/' });
});

let inst: Hls | null = null;
afterEach(() => {
  inst?.destroy();
  inst = null;
  vi.unstubAllGlobals();
});

/** A growing (EVENT, no ENDLIST) media playlist, as a server session serves
 *  it while ffmpeg writes. */
function eventPlaylist(segs: number[], target = 10): string {
  const lines = [
    '#EXTM3U',
    '#EXT-X-VERSION:7',
    `#EXT-X-TARGETDURATION:${target}`,
    '#EXT-X-MEDIA-SEQUENCE:0',
    '#EXT-X-PLAYLIST-TYPE:EVENT',
    '#EXT-X-MAP:URI="init.mp4"',
  ];
  segs.forEach((d, i) => lines.push(`#EXTINF:${d},`, `seg${String(i).padStart(5, '0')}.m4s`));
  return lines.join('\n') + '\n';
}

const MULTIVARIANT = '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=8000000\nmedia.m3u8\n';

/** hls.js's loader interface: answers playlist loads (asynchronously, as a
 *  network would) from `playlists` by URL suffix; fragment loads never
 *  answer (nothing is attached to play them anyway). */
function fakeLoader(playlists: Record<string, string>) {
  return class FakeLoader {
    stats = {
      aborted: false, loaded: 0, retry: 0, total: 0, chunkCount: 0, bwEstimate: 0,
      loading: { start: 0, first: 0, end: 0 },
      parsing: { start: 0, end: 0 },
      buffering: { start: 0, first: 0, end: 0 },
    };
    context: { url: string; type?: string } | null = null;
    load(
      context: { url: string; type?: string },
      _config: unknown,
      callbacks: { onSuccess: (r: unknown, s: unknown, c: unknown, n: unknown) => void },
    ) {
      this.context = context;
      const key = Object.keys(playlists).find((k) => context.url.endsWith(k));
      if (!key) return;
      setTimeout(() => {
        const now = performance.now();
        this.stats.loading = { start: now, first: now, end: now };
        callbacks.onSuccess({ url: context.url, data: playlists[key] }, this.stats, context, null);
      }, 0);
    }
    abort() {}
    destroy() {}
  };
}

interface Run {
  calls: number[];
  early: number[];
  inst: Hls;
}

/** Builds the session exactly as the page does (hlsSessionConfig, then
 *  armFirstLoad, then loadSource) and waits until the first playlist is in
 *  and every microtask after it has run. */
async function open(
  startSec: number,
  playlists: Record<string, string>,
  url: string,
  extra?: (h: Hls) => void,
): Promise<Run> {
  const h = new Hls({ ...hlsSessionConfig(startSec), loader: fakeLoader(playlists) as never });
  inst = h;
  const calls: number[] = [];
  const startLoad = h.startLoad.bind(h);
  // hls.js's autostart calls this.hls.startLoad, so the wrapper sees it too.
  h.startLoad = (pos?: number, skip?: boolean) => {
    calls.push(pos ?? -1);
    startLoad(pos, skip);
  };
  const early: number[] = [];
  armFirstLoad(h, Hls, {
    startSec,
    choose: (details) => firstLoadPositionSec(startSec, details, 0),
    current: () => inst === h,
    onEarlyStart: (at) => early.push(at),
  });
  extra?.(h);
  const parsed = new Promise<void>((resolve) => h.once(Hls.Events.MANIFEST_PARSED, () => resolve()));
  h.loadSource(`http://onscreen.test/session/${url}`);
  await parsed;
  // The deferred half (a microtask), and a level playlist's load after it.
  await new Promise((r) => setTimeout(r, 5));
  return { calls, early, inst: h };
}

describe('armFirstLoad (with the real hls.js)', () => {
  it('starts a resume at a one-segment playlist edge at that segment, and keeps it', async () => {
    // Resuming 1917: the first playlist lists only seg0 (0-10.428 s) and the
    // start is 10.43 s, past it. hls.js must load from 0 (one GOP replayed),
    // not from 10.43, and its own autostart must not replace that.
    const r = await open(10.43, { 'playlist.m3u8': eventPlaylist([10.428]) }, 'playlist.m3u8');
    expect(r.calls).toEqual([0]);
    expect(r.inst.startPosition).toBe(0);
    expect(r.early).toEqual([0]);
    // Back on for a media-error recovery's re-attach, at the requested start.
    expect(r.inst.config.autoStartLoad).toBe(true);
    expect(r.inst.config.startPosition).toBe(10.43);
  });

  it('starts exactly where asked when the playlist already lists past it', async () => {
    const r = await open(25, { 'playlist.m3u8': eventPlaylist([10, 10, 10, 10]) }, 'playlist.m3u8');
    expect(r.calls).toEqual([25]);
    expect(r.inst.startPosition).toBe(25);
    expect(r.early).toEqual([]);
    expect(r.inst.config.autoStartLoad).toBe(true);
  });

  it('starts a multivariant playlist once, at the requested start', async () => {
    // Its level has no details at MANIFEST_PARSED: nothing to decide on.
    const r = await open(
      10.43,
      { 'master.m3u8': MULTIVARIANT, 'media.m3u8': eventPlaylist([10.428]) },
      'master.m3u8',
    );
    expect(r.calls).toEqual([10.43]);
    expect(r.early).toEqual([]);
  });

  it('does not move the playhead when something replaced the early start', async () => {
    // A later MANIFEST_LOADED listener restarting the load (as hls.js's own
    // autostart did): the stream loads from 10.43, so the bar stays there.
    const r = await open(10.43, { 'playlist.m3u8': eventPlaylist([10.428]) }, 'playlist.m3u8', (h) => {
      h.on(Hls.Events.MANIFEST_LOADED, () => h.startLoad(10.43));
    });
    expect(r.calls).toEqual([0, 10.43]);
    expect(r.inst.startPosition).toBe(10.43);
    expect(r.early).toEqual([]);
  });

  it('starts nothing for an instance the page has replaced', async () => {
    const h = new Hls({ ...hlsSessionConfig(10.43), loader: fakeLoader({ 'p.m3u8': eventPlaylist([10.428]) }) as never });
    inst = h;
    const calls: number[] = [];
    h.startLoad = (pos?: number) => void calls.push(pos ?? -1);
    const early: number[] = [];
    armFirstLoad(h, Hls, {
      startSec: 10.43,
      choose: (d) => firstLoadPositionSec(10.43, d, 0),
      current: () => false,
      onEarlyStart: (at) => early.push(at),
    });
    const parsed = new Promise<void>((resolve) => h.once(Hls.Events.MANIFEST_PARSED, () => resolve()));
    h.loadSource('http://onscreen.test/session/p.m3u8');
    await parsed;
    await new Promise((r) => setTimeout(r, 5));
    expect(calls).toEqual([]);
    expect(early).toEqual([]);
  });

  it("pins hls.js's listener order: autoStartLoad turned on inside MANIFEST_PARSED loses the early start", async () => {
    // The previous wiring, verbatim. If an hls.js upgrade changes this order
    // the deferral may no longer be needed (or may no longer be enough):
    // re-check armFirstLoad then.
    const h = new Hls({ ...hlsSessionConfig(10.43), loader: fakeLoader({ 'p.m3u8': eventPlaylist([10.428]) }) as never });
    inst = h;
    const calls: number[] = [];
    const startLoad = h.startLoad.bind(h);
    h.startLoad = (pos?: number, skip?: boolean) => {
      calls.push(pos ?? -1);
      startLoad(pos, skip);
    };
    h.once(Hls.Events.MANIFEST_PARSED, () => {
      h.config.autoStartLoad = true;
      h.startLoad(0);
    });
    const parsed = new Promise<void>((resolve) => h.once(Hls.Events.MANIFEST_PARSED, () => resolve()));
    h.loadSource('http://onscreen.test/session/p.m3u8');
    await parsed;
    await new Promise((r) => setTimeout(r, 5));
    expect(calls).toEqual([0, 10.43]);
    expect(h.startPosition).toBe(10.43);
  });
});
