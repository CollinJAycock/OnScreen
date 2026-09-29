// Desktop (Tauri) with the native engine on: music plays through the engine,
// but an audiobook goes to <audio> — its speed needs the browser's
// pitch-preserving time-stretch — and the engine is stopped so the two never
// play at once.
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import AudioPlayer from './AudioPlayer.svelte';
import { audio } from '$lib/stores/audio';
import { nativeEngine } from '$lib/stores/nativeEngine';

const native = vi.hoisted(() => ({
  isTauri: () => true,
  audioPlayUrl: vi.fn(),
  audioPreloadUrl: vi.fn(),
  audioPause: vi.fn(),
  audioResume: vi.fn(),
  audioSeek: vi.fn(),
  stopAudio: vi.fn(),
  audioState: vi.fn(),
  onMediaKey: vi.fn(),
  notifyNowPlaying: vi.fn(),
  nowPlayingSetMetadata: vi.fn(),
  nowPlayingSetPlayback: vi.fn(),
  nowPlayingClear: vi.fn(),
  replayGainSetMode: vi.fn(),
  replayGainSetPreamp: vi.fn(),
  audioSetExclusiveMode: vi.fn(),
  audioSetVolume: vi.fn(),
}));
const mockGetRate = vi.hoisted(() => vi.fn());

vi.mock('$lib/native', () => native);
vi.mock('$lib/api', () => ({
  itemApi: { progress: vi.fn().mockResolvedValue(undefined), getPlaybackRate: mockGetRate },
  notificationApi: { list: vi.fn().mockResolvedValue([]) },
  getApiBase: () => '',
  getBearerToken: () => null,
  assetUrl: (p: string) => p,
  getClientName: () => 'Desktop — Test',
}));

beforeAll(() => {
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
});

beforeEach(() => {
  audio.clear();
  vi.clearAllMocks();
  nativeEngine.set(true);
  for (const f of Object.values(native)) {
    if (typeof f === 'function' && 'mockResolvedValue' in f) (f as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
  }
  native.onMediaKey.mockResolvedValue(() => {});
  native.audioState.mockResolvedValue({ playing: true, position_ms: 0, ended: false });
  mockGetRate.mockResolvedValue({ rate: 1.5, source: 'book' });
});

afterEach(() => nativeEngine.set(false));

async function flush() {
  for (let i = 0; i < 4; i++) {
    await Promise.resolve();
    await tick();
  }
}

describe('AudioPlayer — native engine and audiobooks', () => {
  it('stops the engine and plays the audiobook in <audio> at its speed', async () => {
    const { container } = render(AudioPlayer);
    await tick();
    const [elA] = Array.from(container.querySelectorAll('audio')) as HTMLAudioElement[];

    audio.play([{ id: 't1', fileId: 'ft', title: 'Song' }]);
    await flush();
    expect(native.audioPlayUrl).toHaveBeenCalledWith('/media/stream/ft', null, null);
    expect(elA.getAttribute('src')).toBeNull();

    audio.play([{ id: 'c1', fileId: 'f1', title: 'Chapter 1', audiobook: { bookId: 'b1' } }]);
    await flush();
    expect(native.stopAudio).toHaveBeenCalled();
    expect(native.audioPlayUrl).toHaveBeenCalledTimes(1);
    expect(elA.getAttribute('src')).toBe('/media/stream/f1');
    expect(elA.playbackRate).toBe(1.5);
  });
});
