// Audiobook listening controls in the global player: per-book speed (fetched
// once per book, kept across chapters and the gapless element swap, saved
// fire-and-forget), Add bookmark, the sleep timer (duration and "end of
// chapter" for both book shapes), chapter skips in a single-file book, and
// store-driven seeks.
import { render, screen, fireEvent } from '@testing-library/svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';
import AudioPlayer from './AudioPlayer.svelte';
import { audio, type AudioTrack } from '$lib/stores/audio';
import { bookmarkAdded } from '$lib/stores/bookmarks';

const mockProgress = vi.hoisted(() => vi.fn());
const mockGetRate = vi.hoisted(() => vi.fn());
const mockSetRate = vi.hoisted(() => vi.fn());
const mockAddBookmark = vi.hoisted(() => vi.fn());
const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn() }));

vi.mock('$lib/api', () => ({
  itemApi: {
    progress: mockProgress,
    getPlaybackRate: mockGetRate,
    setPlaybackRate: mockSetRate,
    addBookmark: mockAddBookmark,
  },
  notificationApi: { list: vi.fn().mockResolvedValue([]) },
  getApiBase: () => '',
  getBearerToken: () => null,
  assetUrl: (p: string) => p,
  getClientName: () => 'Web — Test',
}));

vi.mock('$lib/stores/toast', () => ({ toast: mockToast }));

beforeAll(() => {
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
});

beforeEach(() => {
  audio.clear();
  bookmarkAdded.set(null);
  localStorage.clear();
  vi.clearAllMocks();
  mockProgress.mockResolvedValue(undefined);
  mockGetRate.mockResolvedValue({ rate: 1, source: 'default' });
  mockSetRate.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.useRealTimers();
});

const chapters = [
  { title: 'One', start_ms: 0, end_ms: 60_000 },
  { title: 'Two', start_ms: 60_000, end_ms: 150_000 },
  { title: 'Three', start_ms: 150_000, end_ms: 300_000 },
];

const chapterTrack = (n: number): AudioTrack => ({
  id: `c${n}`, fileId: `f${n}`, title: `Chapter ${n}`, durationMS: 600_000, audiobook: { bookId: 'b1' },
});
const singleFileBook: AudioTrack = {
  id: 'b1', fileId: 'fb', title: 'The Book', durationMS: 300_000, audiobook: { bookId: 'b1', chapters },
};
const song: AudioTrack = { id: 't1', fileId: 'ft', title: 'Song' };

async function flush() {
  for (let i = 0; i < 4; i++) {
    await Promise.resolve();
    await tick();
  }
}

// Renders the player and returns its two <audio> elements with a
// controllable currentTime on each (seconds, like the real property).
async function setup() {
  const { container } = render(AudioPlayer);
  await tick();
  const [elA, elB] = Array.from(container.querySelectorAll('audio')) as HTMLAudioElement[];
  const now = { a: 0, b: 0 };
  Object.defineProperty(elA, 'currentTime', { configurable: true, get: () => now.a, set: (v: number) => { now.a = v; } });
  Object.defineProperty(elB, 'currentTime', { configurable: true, get: () => now.b, set: (v: number) => { now.b = v; } });
  return { elA, elB, now };
}

async function playAt(el: HTMLAudioElement, now: { a: number }, seconds: number) {
  now.a = seconds;
  el.dispatchEvent(new Event('timeupdate'));
  await flush();
}

describe('AudioPlayer — listening speed', () => {
  it('offers no speed or bookmark control for music, and leaves it at 1×', async () => {
    const { elA } = await setup();
    audio.play([song]);
    await flush();

    expect(screen.queryByRole('button', { name: /^Playback speed/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Add bookmark' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Sleep timer' })).toBeTruthy();
    expect(mockGetRate).not.toHaveBeenCalled();
    expect(elA.playbackRate).toBe(1);
  });

  it("applies the book's saved speed, once per book, across chapters and the gapless swap", async () => {
    mockGetRate.mockResolvedValue({ rate: 1.5, source: 'book' });
    const { elA, elB } = await setup();
    audio.play([chapterTrack(1), chapterTrack(2)]);
    await flush();

    expect(mockGetRate).toHaveBeenCalledTimes(1);
    expect(mockGetRate).toHaveBeenCalledWith('c1');
    for (const el of [elA, elB]) {
      expect(el.playbackRate).toBe(1.5);
      expect(el.defaultPlaybackRate).toBe(1.5);
      expect(el.preservesPitch).toBe(true);
    }
    expect(screen.getByRole('button', { name: /^Playback speed/ }).textContent).toBe('1.5×');

    // Chapter 1 ends: the preloaded element takes over, same book.
    elA.dispatchEvent(new Event('ended'));
    await flush();
    expect(get(audio).index).toBe(1);
    expect(mockGetRate).toHaveBeenCalledTimes(1);
    expect(elB.playbackRate).toBe(1.5);

    // A load resets playbackRate to the default; metadata re-asserts it.
    elB.playbackRate = 1;
    elB.dispatchEvent(new Event('loadedmetadata'));
    expect(elB.playbackRate).toBe(1.5);
  });

  it('applies a picked speed at once and keeps it when the save fails', async () => {
    mockSetRate.mockRejectedValue(new Error('offline'));
    const { elA } = await setup();
    audio.play([chapterTrack(1)]);
    await flush();

    await fireEvent.click(screen.getByRole('button', { name: /^Playback speed/ }));
    await fireEvent.click(screen.getByRole('radio', { name: '2×' }));
    await flush();

    expect(elA.playbackRate).toBe(2);
    expect(mockSetRate).toHaveBeenCalledWith('c1', 2);
    expect(screen.getByRole('button', { name: /^Playback speed/ }).textContent).toBe('2×');
    expect(screen.queryByRole('radio', { name: '2×' })).toBeNull(); // menu closed
  });

  it('keeps a speed picked before the saved one arrives', async () => {
    let answer: (v: unknown) => void = () => {};
    mockGetRate.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
    const { elA } = await setup();
    audio.play([chapterTrack(1)]);
    await flush();

    await fireEvent.click(screen.getByRole('button', { name: /^Playback speed/ }));
    await fireEvent.click(screen.getByRole('radio', { name: '1.25×' }));
    answer({ rate: 2, source: 'recent' });
    await flush();

    expect(elA.playbackRate).toBe(1.25);
  });

  it('on a server without the routes plays at 1× and hides bookmarks', async () => {
    mockGetRate.mockRejectedValue(Object.assign(new Error('HTTP 404'), { status: 404 }));
    const { elA } = await setup();
    audio.play([chapterTrack(1)]);
    await flush();

    expect(screen.getByRole('button', { name: /^Playback speed/ }).textContent).toBe('1×');
    expect(elA.playbackRate).toBe(1);
    expect(screen.queryByRole('button', { name: 'Add bookmark' })).toBeNull();
  });

  it('goes back to 1× for music after a book', async () => {
    mockGetRate.mockResolvedValue({ rate: 2, source: 'book' });
    const { elA } = await setup();
    audio.play([chapterTrack(1)]);
    await flush();
    expect(elA.playbackRate).toBe(2);

    audio.play([song]);
    await flush();
    expect(elA.playbackRate).toBe(1);
  });
});

describe('AudioPlayer — bookmarks', () => {
  it('bookmarks the position the prompt opened at, with a trimmed note', async () => {
    const bookmark = {
      id: 'bm1', item_id: 'c1', item_title: 'Chapter 1', item_index: 1,
      position_ms: 83_500, note: 'Great line', created_at: '2026-09-28T00:00:00Z',
    };
    mockAddBookmark.mockResolvedValue(bookmark);
    const { elA, now } = await setup();
    audio.play([chapterTrack(1)]);
    await flush();
    await playAt(elA, now, 83.5);

    await fireEvent.click(screen.getByRole('button', { name: 'Add bookmark' }));
    expect(screen.getByText('Bookmark at 1:23')).toBeTruthy();
    // The book plays on while the note is typed.
    await playAt(elA, now, 90);
    await fireEvent.input(screen.getByRole('textbox', { name: 'Bookmark note' }), { target: { value: '  Great line  ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await flush();

    expect(mockAddBookmark).toHaveBeenCalledWith('c1', 83_500, 'Great line');
    expect(mockToast.success).toHaveBeenCalledWith('Bookmark added at 1:23');
    expect(get(bookmarkAdded)).toEqual({ bookId: 'b1', bookmark });
    expect(screen.queryByRole('textbox', { name: 'Bookmark note' })).toBeNull();
  });

  it('says so and keeps the prompt when the book is full', async () => {
    mockAddBookmark.mockRejectedValue(Object.assign(new Error('this book already has the most bookmarks allowed (1000)'), { status: 409 }));
    await setup();
    audio.play([chapterTrack(1)]);
    await flush();

    await fireEvent.click(screen.getByRole('button', { name: 'Add bookmark' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await flush();

    expect(mockToast.error).toHaveBeenCalledWith('this book already has the most bookmarks allowed (1000)');
    expect(screen.getByRole('textbox', { name: 'Bookmark note' })).toBeTruthy();
    expect(get(bookmarkAdded)).toBeNull();
  });
});

describe('AudioPlayer — sleep timer', () => {
  it('pauses after 15 minutes, shows the time left meanwhile, and keeps the player open', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    await setup();
    audio.play([song]);
    await flush();

    await fireEvent.click(screen.getByRole('button', { name: 'Sleep timer' }));
    await fireEvent.click(screen.getByRole('radio', { name: '15 minutes' }));
    await flush();
    expect(screen.getByRole('button', { name: 'Sleep timer' }).textContent).toContain('15:00');

    vi.advanceTimersByTime(60_000);
    await flush();
    expect(screen.getByRole('button', { name: 'Sleep timer' }).textContent).toContain('14:00');
    expect(get(audio).playing).toBe(true);

    vi.advanceTimersByTime(14 * 60_000 + 1_000);
    await flush();
    expect(get(audio).playing).toBe(false);
    expect(get(audio).queue).toHaveLength(1);
    expect(screen.getByRole('complementary', { name: 'Audio player' })).toBeTruthy();
    expect(mockToast.success).toHaveBeenCalledWith('Sleep timer ended — playback paused');
    expect(screen.getByRole('button', { name: 'Sleep timer' }).textContent?.trim()).toBe('');
  });

  it('end of chapter in a single-file book pauses on the next chapter boundary', async () => {
    const { elA, now } = await setup();
    audio.play([singleFileBook]);
    await flush();
    await playAt(elA, now, 10);

    await fireEvent.click(screen.getByRole('button', { name: 'Sleep timer' }));
    await fireEvent.click(screen.getByRole('radio', { name: 'End of chapter' }));
    await flush();
    // 50 s of chapter One left at 1×.
    expect(screen.getByRole('button', { name: 'Sleep timer' }).textContent).toContain('0:50');

    await playAt(elA, now, 59.9);
    expect(get(audio).playing).toBe(true);

    await playAt(elA, now, 60.2);
    expect(get(audio).playing).toBe(false);
    expect(now.a).toBe(60); // parked on the boundary: Play starts chapter Two
    expect(get(audio).positionMS).toBe(60_000);
    expect(mockToast.success).toHaveBeenCalledWith('Sleep timer ended — paused at the end of the chapter');
  });

  it('end of chapter in a multi-file book stops after the chapter file, on the next one', async () => {
    const { elA } = await setup();
    audio.play([chapterTrack(1), chapterTrack(2)]);
    await flush();

    await fireEvent.click(screen.getByRole('button', { name: 'Sleep timer' }));
    await fireEvent.click(screen.getByRole('radio', { name: 'End of chapter' }));
    await flush();

    elA.dispatchEvent(new Event('ended'));
    await flush();

    const s = get(audio);
    expect(s.index).toBe(1);
    expect(s.playing).toBe(false);
    expect(mockProgress).toHaveBeenCalledWith('c1', 600_000, 600_000, 'stopped');
    expect(mockToast.success).toHaveBeenCalledWith('Sleep timer ended — paused at the end of the chapter');
  });

  it('offers "End of track" for music', async () => {
    await setup();
    audio.play([song]);
    await flush();
    await fireEvent.click(screen.getByRole('button', { name: 'Sleep timer' }));
    expect(screen.getByRole('radio', { name: 'End of track' })).toBeTruthy();
  });
});

describe('AudioPlayer — chapters in a single-file book, and seeks', () => {
  it('names the current chapter and skips between chapters', async () => {
    const { elA, now } = await setup();
    audio.play([singleFileBook]);
    await flush();
    await playAt(elA, now, 70);
    expect(screen.getByText(/^\s*Two/)).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await flush();
    expect(now.a).toBe(150);
    expect(get(audio).positionMS).toBe(150_000);

    // Within 3 s of chapter Three's start: back to chapter Two.
    await fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
    await flush();
    expect(now.a).toBe(60);

    // Well into chapter Two: restart it.
    await playAt(elA, now, 100);
    await fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
    await flush();
    expect(now.a).toBe(60);
  });

  it('moves the element on audio.seek() (a bookmark in the playing chapter)', async () => {
    const { elA, now } = await setup();
    audio.play([chapterTrack(1)]);
    await flush();
    await playAt(elA, now, 5);

    audio.seek(42_000);
    await flush();
    expect(now.a).toBe(42);
    expect(mockProgress).toHaveBeenCalledWith('c1', 42_000, expect.any(Number), 'playing');
  });

  it('restarts a track on Previous past 3 s', async () => {
    const { elA, now } = await setup();
    audio.play([song]);
    await flush();
    await playAt(elA, now, 30);

    await fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
    await flush();
    expect(now.a).toBe(0);
    expect(get(audio).index).toBe(0);
  });
});
