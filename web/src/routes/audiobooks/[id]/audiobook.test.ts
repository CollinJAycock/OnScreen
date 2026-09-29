// The audiobook page's Bookmarks section (list, play from a bookmark, edit a
// note, delete, a bookmark added from the player) and single-file books
// playing in the global player from their chapter-snapped resume point.
import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import { readable, get } from 'svelte/store';
import { audio } from '$lib/stores/audio';
import { bookmarkAdded } from '$lib/stores/bookmarks';

const mockGet = vi.hoisted(() => vi.fn());
const mockChildren = vi.hoisted(() => vi.fn());
const mockListBookmarks = vi.hoisted(() => vi.fn());
const mockUpdateBookmark = vi.hoisted(() => vi.fn());
const mockDeleteBookmark = vi.hoisted(() => vi.fn());
const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn() }));

vi.mock('$app/stores', () => ({
  page: readable({ url: new URL('http://localhost/audiobooks/b1'), params: { id: 'b1' }, state: {} }),
}));
vi.mock('$lib/api', () => ({
  itemApi: {
    get: mockGet,
    children: mockChildren,
    listBookmarks: mockListBookmarks,
    updateBookmark: mockUpdateBookmark,
    deleteBookmark: mockDeleteBookmark,
  },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: mockToast }));

import Page from './+page.svelte';

const embedded = [
  { title: 'One', start_ms: 0, end_ms: 60_000 },
  { title: 'Two', start_ms: 60_000, end_ms: 150_000 },
  { title: 'Three', start_ms: 150_000, end_ms: 300_000 },
];

function detail(over: Record<string, unknown>) {
  return {
    id: 'b1', library_id: 'lib1', title: 'The Book', type: 'audiobook', genres: [],
    view_offset_ms: 0, updated_at: 1, is_favorite: false, files: [], ...over,
  };
}

const chapterRow = (n: number) => ({ id: `c${n}`, title: `Chapter ${n}`, type: 'audiobook_chapter', index: n, duration_ms: 600_000 });

function bm(id: string, item_id: string, item_title: string, position_ms: number, note = '', item_index?: number) {
  return { id, item_id, item_title, item_index, position_ms, note, created_at: '2026-09-28T00:00:00Z' };
}

// A multi-file book: two chapter files.
function multiFileBook() {
  mockGet.mockImplementation(async (id: string) => {
    if (id === 'b1') return detail({});
    return detail({ id, title: `Chapter ${id.slice(1)}`, type: 'audiobook_chapter', files: [{ id: `f-${id}`, chapters: [] }] });
  });
  mockChildren.mockResolvedValue({ items: [chapterRow(1), chapterRow(2)], total: 2 });
}

// A single-file book with embedded chapters, saved 100 s in (chapter Two).
function singleFileBook() {
  mockGet.mockResolvedValue(detail({
    duration_ms: 300_000, view_offset_ms: 100_000,
    files: [{ id: 'fb', duration_ms: 300_000, chapters: embedded }],
  }));
  mockChildren.mockResolvedValue({ items: [], total: 0 });
}

beforeEach(() => {
  vi.clearAllMocks();
  audio.clear();
  bookmarkAdded.set(null);
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: 'u1', username: 'me', is_admin: false }));
  mockUpdateBookmark.mockResolvedValue(undefined);
  mockDeleteBookmark.mockResolvedValue(undefined);
});

async function bookmarksSection() {
  const heading = await screen.findByRole('heading', { name: 'Bookmarks' });
  return within(heading.closest('section')!);
}

describe('Audiobook page — bookmarks', () => {
  it('lists bookmarks with their chapter, time and note', async () => {
    multiFileBook();
    mockListBookmarks.mockResolvedValue([
      bm('bm1', 'c1', 'Chapter 1', 5_000, '', 1),
      bm('bm2', 'c2', 'Chapter 2', 3_723_000, 'The twist', 2),
    ]);
    render(Page);
    const s = await bookmarksSection();

    await waitFor(() => expect(s.getAllByRole('listitem')).toHaveLength(2));
    const [first, second] = s.getAllByRole('listitem');
    expect(within(first).getByText('Chapter 1')).toBeTruthy();
    expect(within(first).getByRole('button', { name: 'Play from Chapter 1, 0:05' })).toBeTruthy();
    expect(within(first).getByRole('button', { name: 'Add note' })).toBeTruthy();
    expect(within(second).getByText('The twist')).toBeTruthy();
    expect(within(second).getByRole('button', { name: 'Play from Chapter 2, 1:02:03' })).toBeTruthy();
    expect(mockListBookmarks).toHaveBeenCalledWith('b1');
  });

  it('plays a bookmark: the right chapter, at the right position, in the book queue', async () => {
    multiFileBook();
    mockListBookmarks.mockResolvedValue([bm('bm2', 'c2', 'Chapter 2', 83_500, '', 2)]);
    render(Page);
    const s = await bookmarksSection();

    await fireEvent.click(await s.findByRole('button', { name: 'Play from Chapter 2, 1:23' }));
    const a = get(audio);
    expect(a.queue.map((t) => t.id)).toEqual(['c1', 'c2']);
    expect(a.queue.every((t) => t.audiobook?.bookId === 'b1')).toBe(true);
    expect(a.index).toBe(1);
    expect(a.positionMS).toBe(83_500);
    expect(a.playing).toBe(true);

    // Already in that chapter: the player just moves there.
    audio.setPosition(200_000);
    const seq = get(audio).seekSeq;
    await fireEvent.click(s.getByRole('button', { name: 'Play from Chapter 2, 1:23' }));
    expect(get(audio).seekSeq).toBe(seq + 1);
    expect(get(audio).positionMS).toBe(83_500);
    expect(get(audio).index).toBe(1);
  });

  it('edits a note and deletes a bookmark', async () => {
    multiFileBook();
    mockListBookmarks.mockResolvedValue([
      bm('bm1', 'c1', 'Chapter 1', 5_000, 'old note', 1),
      bm('bm2', 'c2', 'Chapter 2', 9_000, '', 2),
    ]);
    render(Page);
    const s = await bookmarksSection();

    await fireEvent.click(await s.findByRole('button', { name: 'Edit note' }));
    const input = s.getByRole('textbox', { name: 'Bookmark note' }) as HTMLInputElement;
    expect(input.value).toBe('old note');
    await fireEvent.input(input, { target: { value: '  new note ' } });
    await fireEvent.click(s.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(s.getByText('new note')).toBeTruthy());
    expect(mockUpdateBookmark).toHaveBeenCalledWith('bm1', 'new note');

    await fireEvent.click(s.getByRole('button', { name: 'Delete bookmark at 0:09' }));
    await waitFor(() => expect(s.getAllByRole('listitem')).toHaveLength(1));
    expect(mockDeleteBookmark).toHaveBeenCalledWith('bm2');
    expect(mockToast.success).toHaveBeenCalledWith('Bookmark deleted');
  });

  it('lists a bookmark added from the player in its place', async () => {
    multiFileBook();
    mockListBookmarks.mockResolvedValue([
      bm('bm1', 'c1', 'Chapter 1', 5_000, '', 1),
      bm('bm2', 'c2', 'Chapter 2', 9_000, '', 2),
    ]);
    render(Page);
    const s = await bookmarksSection();
    await waitFor(() => expect(s.getAllByRole('listitem')).toHaveLength(2));

    bookmarkAdded.set({ bookId: 'other', bookmark: bm('x', 'c9', 'Elsewhere', 1_000) });
    bookmarkAdded.set({ bookId: 'b1', bookmark: bm('bm3', 'c1', 'Chapter 1', 30_000, 'fresh', 1) });
    await waitFor(() => expect(s.getAllByRole('listitem')).toHaveLength(3));
    expect(within(s.getAllByRole('listitem')[1]).getByText('fresh')).toBeTruthy();
  });

  it('hides the section on a server without bookmarks', async () => {
    multiFileBook();
    mockListBookmarks.mockRejectedValue(Object.assign(new Error('HTTP 404'), { status: 404 }));
    render(Page);
    await screen.findByText('Chapter 2');
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Bookmarks' })).toBeNull());
    expect(mockListBookmarks).toHaveBeenCalledWith('b1');
    expect(screen.getByText('Chapter 2')).toBeTruthy(); // the page itself is fine
  });

  it('says so quietly when bookmarks fail to load', async () => {
    multiFileBook();
    mockListBookmarks.mockRejectedValue(Object.assign(new Error('boom'), { status: 500 }));
    render(Page);
    const s = await bookmarksSection();
    expect(await s.findByText("Couldn't load bookmarks.")).toBeTruthy();
  });
});

describe('Audiobook page — single-file book', () => {
  it('resumes in the global player at the start of the saved chapter', async () => {
    singleFileBook();
    mockListBookmarks.mockResolvedValue([]);
    render(Page);

    const play = await screen.findByRole('button', { name: /Resume/ });
    expect(play.textContent).toMatch(/ch\s+2\s+·\s+1:00/);
    expect(await screen.findByText(/No bookmarks yet/)).toBeTruthy();
    await fireEvent.click(play);

    const a = get(audio);
    expect(a.queue).toHaveLength(1);
    expect(a.queue[0]).toMatchObject({ id: 'b1', fileId: 'fb', audiobook: { bookId: 'b1', chapters: embedded } });
    expect(a.positionMS).toBe(60_000);
    expect(a.playing).toBe(true);
  });

  it('lists the embedded chapters and plays one from its start', async () => {
    singleFileBook();
    mockListBookmarks.mockResolvedValue([]);
    render(Page);

    await fireEvent.click(await screen.findByTitle('Play Three'));
    expect(get(audio).queue[0].id).toBe('b1');
    expect(get(audio).positionMS).toBe(150_000);

    // The book is playing now: another chapter is a seek, not a reload.
    const seq = get(audio).seekSeq;
    await fireEvent.click(screen.getByTitle('Play One'));
    expect(get(audio).seekSeq).toBe(seq + 1);
    expect(get(audio).positionMS).toBe(0);
  });

  it("names a bookmark's embedded chapter and plays the book from it", async () => {
    singleFileBook();
    mockListBookmarks.mockResolvedValue([bm('bm1', 'b1', 'The Book', 70_000, 'here')]);
    render(Page);
    const s = await bookmarksSection();

    await fireEvent.click(await s.findByRole('button', { name: 'Play from Two, 1:10' }));
    expect(get(audio).queue[0].id).toBe('b1');
    expect(get(audio).positionMS).toBe(70_000);
  });
});
