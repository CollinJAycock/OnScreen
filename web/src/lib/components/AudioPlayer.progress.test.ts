// The 'playing' progress beat is throttled to one per 10 s of playback. The
// throttle state used to be reset from the currentTrack subscription, which
// Svelte re-runs on every audio-store update (derived object values always
// count as changed) — so each timeupdate reset it and a beat went out ~4×/s.
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import AudioPlayer from './AudioPlayer.svelte';
import { audio } from '$lib/stores/audio';

const mockProgress = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  itemApi: { progress: mockProgress },
  notificationApi: { list: vi.fn().mockResolvedValue([]) },
  getApiBase: () => '',
  getBearerToken: () => null,
  assetUrl: (p: string) => p,
  getClientName: () => 'Web — Test',
}));

beforeAll(() => {
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
});

beforeEach(() => {
  audio.clear();
  localStorage.clear();
  vi.clearAllMocks();
  mockProgress.mockResolvedValue(undefined);
});

function playingBeats(itemId: string): number[] {
  return mockProgress.mock.calls
    .filter((c) => c[0] === itemId && c[3] === 'playing')
    .map((c) => c[1] as number);
}

describe('AudioPlayer — playing progress throttle', () => {
  it('sends one playing beat per 10 s of playback, not one per timeupdate', async () => {
    const { container } = render(AudioPlayer);
    await tick();
    audio.play([{ id: 't1', fileId: 'f1', title: 'Track 1', durationMS: 300_000 }]);
    await tick();
    await tick();
    const [el] = Array.from(container.querySelectorAll('audio')) as HTMLAudioElement[];
    let now = 0;
    Object.defineProperty(el, 'currentTime', { configurable: true, get: () => now, set: (v: number) => { now = v; } });
    el.dispatchEvent(new Event('playing'));
    el.dispatchEvent(new Event('play'));
    await tick();

    // 25 s of playback at 4 timeupdates per second.
    for (let ms = 0; ms <= 25_000; ms += 250) {
      now = ms / 1000;
      el.dispatchEvent(new Event('timeupdate'));
      await Promise.resolve();
      await tick();
    }

    const beats = playingBeats('t1');
    expect(beats.length).toBeGreaterThanOrEqual(1);
    // 0 s, ~10 s, ~20 s — never dozens.
    expect(beats.length).toBeLessThanOrEqual(4);
    for (let i = 1; i < beats.length; i++) {
      expect(beats[i] - beats[i - 1]).toBeGreaterThanOrEqual(10_000);
    }
  });
});
