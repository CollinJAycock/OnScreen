// The photo viewer's neighbours: a window of a photo listing around the
// photo that was opened, extended a page at a time as left / right near its
// edges.
//
// The viewer used to crawl the whole library listing (500 a page, one page
// after another) on every photo opened outside an album, with left / right
// dead until it finished and nothing to stop it on leave: 50 000 photos was
// 100 requests per photo, and a few photos opened in a row crawled at once.
// Now the opener hands over the listing and the photo's place in it when it
// knows them (the library grid: offerPhotoSource); the viewer shows the photo
// at once, reads one page around it, and reads more only as the user goes.
// Opened from anywhere else (a hub row, search, a link), the viewer asks the
// server where the photo sits in its library's listing and reads around that
// (libraryListings).
//
// The window and its paging are here, without the page, so they are tested.

export interface PhotoRef {
  id: string;
  posterPath: string;
}

/** One page of a photo listing: the rows from `offset`, at most `limit`, and
 *  the listing's length when the server says. */
export type PhotoPager = (offset: number, limit: number) => Promise<{ items: PhotoRef[]; total: number | null }>;

/** Where a photo was opened from, when the opener knows: the listing it is
 *  in and its offset there. */
export interface PhotoSource {
  /** The photo opened. */
  id: string;
  /** Its offset in the listing. */
  at: number;
  page: PhotoPager;
}

let offered: PhotoSource | null = null;

/** The opener's listing for the viewer about to open (see PhotoSource). */
export function offerPhotoSource(s: PhotoSource): void {
  offered = s;
}

/** The listing offered for photo `id`, once; null when none was (or it was
 *  for another photo). */
export function takePhotoSource(id: string): PhotoSource | null {
  const s = offered;
  offered = null;
  return s && s.id === id ? s : null;
}

/** Rows per read. */
export const PHOTO_PAGE = 100;
/** How close to the window's edge the current photo gets before the next
 *  page is read. */
export const PHOTO_EDGE = 10;
/** The most /api/v1/photos (a library's newest photos) returns in one read. */
export const PHOTO_LOOKUP = 500;

/** A listing to look for the photo's neighbours in: `page` read around
 *  offset `at`, `rows` rows at first (PHOTO_PAGE when unset). */
export interface PhotoListing {
  page: PhotoPager;
  at: number;
  rows?: number;
}

/** The listings to look in, best first, for a photo opened with no listing
 *  offered and no album around it (a hub row, search, a link):
 *  - its library's listing (`grid`, the library grid's default order) around
 *    the photo's row there, when the server says where that is (`locate`:
 *    GET /libraries/{id}/items/position with the same sort; null when the
 *    photo has no library). A server from before the endpoint answers 404,
 *    as does one whose listing doesn't hold the photo: no row then;
 *  - the library's newest photos (`newest`, one read of PHOTO_LOOKUP), which
 *    give it neighbours only when it is among them. */
export async function libraryListings(
  locate: (() => Promise<{ offset: number }>) | null,
  grid: PhotoPager,
  newest: PhotoPager,
): Promise<PhotoListing[]> {
  const out: PhotoListing[] = [];
  if (locate) {
    try {
      const at = (await locate()).offset;
      if (Number.isInteger(at) && at >= 0) out.push({ page: grid, at });
    } catch {
      /* no row for it: the newest photos */
    }
  }
  out.push({ page: newest, at: 0, rows: PHOTO_LOOKUP });
  return out;
}

export class PhotoWindow {
  /** The listing offset of items[0]. */
  start = 0;
  items: PhotoRef[] = [];
  /** The listing's length, when the server said. */
  total: number | null = null;
  /** Nothing before items[0] / after the last item. */
  atStart = false;
  atEnd = false;
  private stopped = false;
  private ahead: Promise<boolean> | null = null;
  private behind: Promise<boolean> | null = null;

  constructor(private readonly page: PhotoPager) {}

  /** Read `rows` rows around offset `at`; true when photo `id` is in them. */
  async open(id: string, at: number, rows = PHOTO_PAGE): Promise<boolean> {
    const from = Math.max(0, at - Math.floor(rows / 2));
    const res = await this.page(from, rows);
    if (this.stopped) return false;
    this.start = from;
    this.items = res.items;
    this.total = res.total;
    this.atStart = from === 0;
    this.atEnd = this.reachedEnd(from, res.items.length, rows);
    return this.indexOf(id) >= 0;
  }

  indexOf(id: string): number {
    return this.items.findIndex((p) => p.id === id);
  }

  /** The whole listing is in: left / right may wrap around. */
  get complete(): boolean {
    return this.atStart && this.atEnd;
  }

  /** Read the next page if `index` (in items) is near the end; true once
   *  rows were added. One read at a time. */
  extendAfter(index: number): Promise<boolean> {
    if (this.stopped || this.atEnd || index < this.items.length - PHOTO_EDGE) return Promise.resolve(false);
    if (!this.ahead) {
      this.ahead = this.readAfter().finally(() => (this.ahead = null));
    }
    return this.ahead;
  }

  /** Read the page before if `index` is near the start; true once rows were
   *  put in front (every index moves up by their number). */
  extendBefore(index: number): Promise<boolean> {
    if (this.stopped || this.atStart || index >= PHOTO_EDGE) return Promise.resolve(false);
    if (!this.behind) {
      this.behind = this.readBefore().finally(() => (this.behind = null));
    }
    return this.behind;
  }

  /** The viewer is gone: no further reads, and late answers are dropped. */
  stop(): void {
    this.stopped = true;
  }

  private reachedEnd(from: number, got: number, asked: number): boolean {
    return got < asked || (this.total !== null && from + got >= this.total);
  }

  private async readAfter(): Promise<boolean> {
    const from = this.start + this.items.length;
    const res = await this.page(from, PHOTO_PAGE);
    if (this.stopped) return false;
    if (res.total !== null) this.total = res.total;
    const have = new Set(this.items.map((p) => p.id));
    const fresh = res.items.filter((p) => !have.has(p.id));
    this.items = this.items.concat(fresh);
    this.atEnd = this.reachedEnd(from, res.items.length, PHOTO_PAGE);
    return fresh.length > 0;
  }

  private async readBefore(): Promise<boolean> {
    const from = Math.max(0, this.start - PHOTO_PAGE);
    const want = this.start - from;
    const res = await this.page(from, want);
    if (this.stopped) return false;
    if (res.total !== null) this.total = res.total;
    const have = new Set(this.items.map((p) => p.id));
    const fresh = res.items.slice(0, want).filter((p) => !have.has(p.id));
    this.items = fresh.concat(this.items);
    this.start = from;
    this.atStart = from === 0;
    return fresh.length > 0;
  }
}
