// Browser ReplayGain path of the AudioPlayer: with a (fake) AudioContext
// available and ReplayGain switched on, both <audio> elements are tapped
// into GainNodes, the active one carries volume x ReplayGain, and the
// element's own volume is pinned to 1. With ReplayGain off nothing is
// tapped (AudioPlayer.test.ts covers the untouched element.volume path).
import { render, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import AudioPlayer from './AudioPlayer.svelte';
import { audio } from '$lib/stores/audio';

const mockProgress = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
const mockItemGet = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  itemApi: { progress: mockProgress, get: mockItemGet },
  getApiBase: () => '',
  getBearerToken: () => null,
  assetUrl: (p: string) => p,
}));

class FakeParam {
  value: number;
  constructor(v: number) {
    this.value = v;
  }
  cancelScheduledValues() {}
  setValueAtTime(v: number) {
    this.value = v;
  }
  setTargetAtTime(v: number) {
    this.value = v;
  }
}
class FakeGain {
  gain = new FakeParam(1);
  connect(n: unknown) {
    return n;
  }
}
const contexts: FakeAudioContext[] = [];
class FakeAudioContext {
  state = 'suspended';
  currentTime = 0;
  destination = {};
  sources = new Map<unknown, FakeGain | null>();
  gains: FakeGain[] = [];
  constructor() {
    contexts.push(this);
  }
  async resume() {
    this.state = 'running';
  }
  createGain() {
    const g = new FakeGain();
    this.gains.push(g);
    return g;
  }
  createMediaElementSource(el: HTMLMediaElement) {
    const ctx = this;
    return {
      connect(g: FakeGain) {
        ctx.sources.set(el, g);
        return g;
      },
    };
  }
  gainFor(el: HTMLMediaElement): number | undefined {
    return this.sources.get(el)?.gain.value;
  }
}

const db = (x: number) => Math.pow(10, x / 20);

beforeAll(() => {
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
});

beforeEach(() => {
  audio.clear();
  localStorage.clear();
  vi.clearAllMocks();
  contexts.length = 0;
  vi.stubGlobal('AudioContext', FakeAudioContext);
  // Session probe: /media/stream answers directly (no redirect).
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ type: 'basic', status: 206, ok: true }));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function startAlbum(container: HTMLElement) {
  const [elA, elB] = Array.from(container.querySelectorAll('audio')) as HTMLAudioElement[];
  // The click that starts playback is the gesture that unlocks the graph.
  window.dispatchEvent(new Event('pointerdown'));
  audio.play([
    { id: 't1', fileId: 'f1', title: 'Loud', replayGain: { trackGain: -6, trackPeak: 0.9, albumGain: -7, albumPeak: 0.95 } },
    { id: 't2', fileId: 'f2', title: 'Quiet', replayGain: { trackGain: 2, trackPeak: 0.5, albumGain: -7, albumPeak: 0.95 } },
  ]);
  await tick();
  return { elA, elB };
}

describe('AudioPlayer — browser ReplayGain', () => {
  it('taps both elements and applies track gain on the active one', async () => {
    localStorage.setItem('onscreen_native_rg_mode', 'track');
    const { container } = render(AudioPlayer);
    await tick();
    const { elA, elB } = await startAlbum(container);

    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled());
    const ctx = contexts[0];
    expect(ctx).toBeTruthy();
    expect(ctx.sources.size).toBe(2);
    expect(elA.volume).toBe(1);
    expect(elB.volume).toBe(1);
    expect(ctx.gainFor(elA)).toBeCloseTo(db(-6), 6);
    expect(ctx.gainFor(elB)).toBe(0);
  });

  it('carries user volume in the gain stage and hands the next gain over on a gapless swap', async () => {
    localStorage.setItem('onscreen_native_rg_mode', 'track');
    localStorage.setItem('onscreen_audio_volume', '0.5');
    const { container } = render(AudioPlayer);
    await tick();
    const { elA, elB } = await startAlbum(container);
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled());
    const ctx = contexts[0];
    expect(ctx.gainFor(elA)).toBeCloseTo(0.5 * db(-6), 6);

    elA.dispatchEvent(new Event('ended'));
    await tick();
    await tick();
    // Track 2: +2 dB wanted, peak 0.5 leaves headroom -> full +2 dB.
    expect(ctx.gainFor(elB)).toBeCloseTo(0.5 * db(2), 6);
    expect(ctx.gainFor(elA)).toBe(0);
    expect(elA.volume).toBe(1);
    expect(elB.volume).toBe(1);
  });

  it('uses album gain in album mode with the preamp', async () => {
    localStorage.setItem('onscreen_native_rg_mode', 'album');
    localStorage.setItem('onscreen_native_rg_preamp', '1');
    const { container } = render(AudioPlayer);
    await tick();
    const { elA } = await startAlbum(container);
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled());
    expect(contexts[0].gainFor(elA)).toBeCloseTo(db(-6), 6);
  });

  it('looks up tags for a track queued without them before starting it', async () => {
    localStorage.setItem('onscreen_native_rg_mode', 'track');
    mockItemGet.mockResolvedValue({
      id: 't9',
      files: [{ id: 'f9', replaygain_track_gain: -3, replaygain_track_peak: 0.8 }],
    });
    const { container } = render(AudioPlayer);
    await tick();
    const [elA] = Array.from(container.querySelectorAll('audio')) as HTMLAudioElement[];
    window.dispatchEvent(new Event('pointerdown'));
    audio.play([{ id: 't9', fileId: 'f9', title: 'Transferred' }]);
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled());
    expect(mockItemGet).toHaveBeenCalledWith('t9');
    expect(contexts[0].gainFor(elA)).toBeCloseTo(db(-3), 6);
  });

  it('leaves playback untouched when the stream redirects off-origin', async () => {
    localStorage.setItem('onscreen_native_rg_mode', 'track');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ type: 'opaqueredirect', status: 0, ok: false }));
    const info = vi.spyOn(console, 'info').mockImplementation(() => {});
    const { container } = render(AudioPlayer);
    await tick();
    const { elA, elB } = await startAlbum(container);
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled());
    expect(contexts[0].sources.size).toBe(0);
    expect(elA.volume).toBe(1);
    expect(elB.volume).toBe(0);
    expect(info).toHaveBeenCalledWith(expect.stringMatching(/\[replaygain\]/));
    info.mockRestore();
  });

  it('does not create an AudioContext while ReplayGain is off', async () => {
    const { container } = render(AudioPlayer);
    await tick();
    const { elA, elB } = await startAlbum(container);
    await tick();
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalled();
    expect(contexts).toHaveLength(0);
    expect(elA.volume).toBe(1);
    expect(elB.volume).toBe(0);
  });
});
