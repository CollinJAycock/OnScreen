// Admin "stop this stream" for browser music. The playback.stop SSE event used
// to reach only the watch page, and a refused 'playing' beat or byte range
// was swallowed / treated as an unplayable file (skip to the next track), so
// an admin Stop never stopped the music. Now the AudioPlayer stops, drops the
// queue and shows the admin's message on every one of those paths.
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';
import AudioPlayer from './AudioPlayer.svelte';
import { audio, currentTrack } from '$lib/stores/audio';
import { playbackStops } from '$lib/stores/notifications';
import { toast } from '$lib/stores/toast';

const CLIENT = 'Web — Chrome on Windows';
const STOP_TEXT = 'Playback was stopped by the server admin: bedtime';

const mockProgress = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  itemApi: { progress: mockProgress },
  notificationApi: { list: vi.fn().mockResolvedValue([]) },
  getApiBase: () => '',
  getBearerToken: () => null,
  assetUrl: (p: string) => p,
  getClientName: () => CLIENT,
}));

beforeAll(() => {
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
});

beforeEach(() => {
  audio.clear();
  playbackStops.set(null);
  for (const t of get(toast)) toast.removeToast(t.id);
  localStorage.clear();
  vi.clearAllMocks();
  mockProgress.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function startAlbum() {
  const { container } = render(AudioPlayer);
  await tick();
  audio.play([
    { id: 't1', fileId: 'f1', title: 'Track 1' },
    { id: 't2', fileId: 'f2', title: 'Track 2' },
  ]);
  await tick();
  await tick();
  const [elA] = Array.from(container.querySelectorAll('audio')) as HTMLAudioElement[];
  expect(elA.getAttribute('src')).toBe('/media/stream/f1');
  return elA;
}

function toastTexts(): string[] {
  return get(toast).map((t) => t.message);
}

function reportedStates(itemId: string): string[] {
  return mockProgress.mock.calls.filter((c) => c[0] === itemId).map((c) => c[3] as string);
}

describe('AudioPlayer — admin stop', () => {
  it('stops on a playback.stop event for this track and client, without advancing', async () => {
    const elA = await startAlbum();

    playbackStops.set({ itemId: 't1', clientName: CLIENT, message: 'bedtime' });
    await tick();
    await tick();

    expect(get(currentTrack)).toBeNull(); // queue dropped, not advanced to t2
    expect(get(audio).playing).toBe(false);
    expect(elA.getAttribute('src')).toBeNull(); // element halted
    expect(toastTexts()).toEqual([STOP_TEXT]);
    // The cut-off position is saved as 'stopped' (never refused server-side).
    expect(reportedStates('t1')).toContain('stopped');
    expect(reportedStates('t2')).toEqual([]);
  });

  it('stops on an untargeted stop for this track', async () => {
    await startAlbum();
    playbackStops.set({ itemId: 't1' });
    await tick();
    expect(get(currentTrack)).toBeNull();
    expect(toastTexts()).toEqual(['Playback was stopped by the server admin.']);
  });

  it('ignores a stop for another item or another client', async () => {
    await startAlbum();

    playbackStops.set({ itemId: 't2', clientName: CLIENT });
    await tick();
    playbackStops.set({ itemId: 't1', clientName: 'Living Room TV' });
    await tick();

    expect(get(currentTrack)?.id).toBe('t1');
    expect(get(audio).playing).toBe(true);
    expect(toastTexts()).toEqual([]);
  });

  it('does not act on the stop the store replays at mount', async () => {
    playbackStops.set({ itemId: 't1', clientName: CLIENT, message: 'old' });
    await startAlbum();
    expect(get(currentTrack)?.id).toBe('t1');
    expect(toastTexts()).toEqual([]);
  });

  it('treats a 403 PLAYBACK_STOPPED on the playing beat as a stop', async () => {
    mockProgress.mockImplementation((_id: string, _pos: number, _dur: number, state: string) =>
      state === 'playing'
        ? Promise.reject(Object.assign(new Error(STOP_TEXT), { status: 403, code: 'PLAYBACK_STOPPED' }))
        : Promise.resolve(undefined),
    );
    const elA = await startAlbum();

    elA.dispatchEvent(new Event('timeupdate')); // → report('playing') → 403
    await vi.waitFor(() => expect(get(currentTrack)).toBeNull());
    expect(toastTexts()).toEqual([STOP_TEXT]);
  });

  it('keeps playing when a beat fails for any other reason', async () => {
    mockProgress.mockImplementation((_id: string, _pos: number, _dur: number, state: string) =>
      state === 'playing' ? Promise.reject(new TypeError('Failed to fetch')) : Promise.resolve(undefined),
    );
    const elA = await startAlbum();
    elA.dispatchEvent(new Event('timeupdate'));
    await tick();
    await tick();
    expect(get(currentTrack)?.id).toBe('t1');
    expect(toastTexts()).toEqual([]);
  });

  it('treats a media error whose stream answers 403 PLAYBACK_STOPPED as a stop, not a skip', async () => {
    const fetchMock = vi.fn(async () => ({
      status: 403,
      json: async () => ({ error: { code: 'PLAYBACK_STOPPED', message: STOP_TEXT } }),
    }));
    vi.stubGlobal('fetch', fetchMock);
    const elA = await startAlbum();

    elA.dispatchEvent(new Event('error'));
    await vi.waitFor(() => expect(get(currentTrack)).toBeNull());

    expect(fetchMock).toHaveBeenCalledWith('/media/stream/f1', { headers: { Range: 'bytes=0-0' } });
    expect(toastTexts()).toEqual([STOP_TEXT]);
    expect(reportedStates('t2')).toEqual([]);
  });

  it('still skips an unplayable file whose stream is served normally', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ status: 206, json: async () => null })));
    const elA = await startAlbum();

    elA.dispatchEvent(new Event('error'));
    await vi.waitFor(() => expect(get(currentTrack)?.id).toBe('t2'));
    expect(toastTexts()).toEqual([]);
  });
});
