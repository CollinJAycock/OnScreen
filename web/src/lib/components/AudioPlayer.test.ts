import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import AudioPlayer from './AudioPlayer.svelte';
import { audio } from '$lib/stores/audio';

const mockProgress = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));

vi.mock('$lib/api', () => ({
  itemApi: { progress: mockProgress },
  getApiBase: () => '',
  getBearerToken: () => null,
  assetUrl: (p: string) => p,
}));

// happy-dom doesn't implement HTMLMediaElement playback. Stub the methods
// the component calls so reactive blocks don't throw and unhandled rejections
// from .play() don't pause the store.
beforeAll(() => {
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
});

beforeEach(() => {
  audio.clear();
  localStorage.clear();
  vi.clearAllMocks();
});

describe('AudioPlayer — gapless rollover', () => {
  it('applies full volume to the new active element after auto-advance', async () => {
    const { container } = render(AudioPlayer);
    await tick();

    const els = Array.from(container.querySelectorAll('audio')) as HTMLAudioElement[];
    expect(els).toHaveLength(2);
    const [elA, elB] = els;

    // Queue two tracks. Player picks elA as active, elB as silent preload.
    audio.play([
      { id: 't1', fileId: 'f1', title: 'Track 1' },
      { id: 't2', fileId: 'f2', title: 'Track 2' },
    ]);
    await tick();
    await tick();

    expect(elA.volume).toBe(1);
    expect(elB.volume).toBe(0);

    // Track 1 ends → onEnded handler advances the queue. Because elB was
    // preloaded with track 2's URL, the player takes the gapless path:
    // activeIsA flips to point at elB.
    elA.dispatchEvent(new Event('ended'));
    await tick();
    await tick();

    // Regression guard for "next track plays silent." Before the fix,
    // the volume reactive block didn't track activeIsA (it used helper
    // functions that hide the variable from Svelte's dep analysis), so
    // elB stayed at the volume=0 it had as the preload buffer.
    expect(elB.volume).toBe(1);
    expect(elA.volume).toBe(0);
  });

  it('keeps the active element audible across a manual track skip', async () => {
    const { container } = render(AudioPlayer);
    await tick();

    const [elA, elB] = Array.from(
      container.querySelectorAll('audio'),
    ) as HTMLAudioElement[];

    audio.play([
      { id: 't1', fileId: 'f1', title: 'Track 1' },
      { id: 't2', fileId: 'f2', title: 'Track 2' },
    ]);
    await tick();
    await tick();

    // audio.next() before track 1 ends — exercises the same advancement path
    // the rollover uses, but with playback still mid-track.
    audio.next();
    await tick();
    await tick();

    expect(elB.volume).toBe(1);
    expect(elA.volume).toBe(0);
  });

  it('respects mute across a rollover', async () => {
    const { container } = render(AudioPlayer);
    await tick();

    const [elA, elB] = Array.from(
      container.querySelectorAll('audio'),
    ) as HTMLAudioElement[];

    localStorage.setItem('onscreen_audio_muted', '1');
    // Re-render so onMount picks up the muted flag.
    container.innerHTML = '';
    const { container: c2 } = render(AudioPlayer);
    await tick();
    const [a2, b2] = Array.from(c2.querySelectorAll('audio')) as HTMLAudioElement[];

    audio.play([
      { id: 't1', fileId: 'f1', title: 'Track 1' },
      { id: 't2', fileId: 'f2', title: 'Track 2' },
    ]);
    await tick();
    await tick();

    expect(a2.volume).toBe(0);
    expect(b2.volume).toBe(0);

    a2.dispatchEvent(new Event('ended'));
    await tick();
    await tick();

    // Both elements stay muted across the swap — the new active element
    // must not unilaterally restore volume.
    expect(a2.volume).toBe(0);
    expect(b2.volume).toBe(0);
    // Suppress unused-binding lint for the first render's elements.
    void elA; void elB;
  });
});
