import type { MediaRequest, RequestDownload } from '$lib/api';
import {
  POLL_INTERVAL_MS,
  createPoller,
  downloadChip,
  failureReason,
  formatBytes,
  formatEta,
  hasActiveRequests,
  isActiveRequest,
  seasonsLabel,
  showsDownload,
  timeAgo,
} from './requestRows';

const NOW = Date.parse('2026-09-28T12:00:00Z');
const inMs = (ms: number) => new Date(NOW + ms).toISOString();
const MIN = 60_000;
const HR = 60 * MIN;

function dl(over: Partial<RequestDownload> & Pick<RequestDownload, 'state'>): RequestDownload {
  return { updated_at: inMs(-MIN), ...over };
}

describe('seasonsLabel', () => {
  it('reads an empty or missing list as all seasons', () => {
    expect(seasonsLabel(undefined)).toBe('All seasons');
    expect(seasonsLabel(null)).toBe('All seasons');
    expect(seasonsLabel([])).toBe('All seasons');
  });

  it('names a single season', () => {
    expect(seasonsLabel([2])).toBe('Season 2');
  });

  it('compresses runs of three or more into ranges', () => {
    expect(seasonsLabel([1, 2, 3, 5])).toBe('Seasons 1–3, 5');
    expect(seasonsLabel([1, 2, 3, 4, 7, 8, 9])).toBe('Seasons 1–4, 7–9');
    expect(seasonsLabel([1, 2])).toBe('Seasons 1, 2');
    expect(seasonsLabel([1, 3, 5])).toBe('Seasons 1, 3, 5');
  });

  it('sorts, de-duplicates and drops junk', () => {
    expect(seasonsLabel([5, 1, 3, 2, 2])).toBe('Seasons 1–3, 5');
    expect(seasonsLabel([2, -1, 1.5, NaN])).toBe('Season 2');
  });

  it('calls season 0 Specials', () => {
    expect(seasonsLabel([0])).toBe('Specials');
    expect(seasonsLabel([0, 1])).toBe('Season 1 + Specials');
    expect(seasonsLabel([0, 1, 2, 3])).toBe('Seasons 1–3 + Specials');
  });
});

describe('formatEta', () => {
  it('formats the time left', () => {
    expect(formatEta(inMs(30_000), NOW)).toBe('<1 min');
    expect(formatEta(inMs(25 * MIN), NOW)).toBe('~25 min');
    expect(formatEta(inMs(59.6 * MIN), NOW)).toBe('~1 hr');
    expect(formatEta(inMs(2 * HR + 5 * MIN), NOW)).toBe('~2 hr 5 min');
    expect(formatEta(inMs(2 * HR), NOW)).toBe('~2 hr');
    expect(formatEta(inMs(12 * HR + 20 * MIN), NOW)).toBe('~12 hr');
    expect(formatEta(inMs(25 * HR), NOW)).toBe('~1 day');
    expect(formatEta(inMs(3 * 24 * HR), NOW)).toBe('~3 days');
  });

  it('is empty for a missing, unparseable or past ETA', () => {
    expect(formatEta(undefined, NOW)).toBe('');
    expect(formatEta('', NOW)).toBe('');
    expect(formatEta('soon', NOW)).toBe('');
    expect(formatEta(inMs(-MIN), NOW)).toBe('');
    expect(formatEta(inMs(0), NOW)).toBe('');
  });
});

describe('timeAgo', () => {
  it('formats relative to now', () => {
    expect(timeAgo(inMs(-10_000), NOW)).toBe('just now');
    expect(timeAgo(inMs(-5 * MIN), NOW)).toBe('5 min ago');
    expect(timeAgo(inMs(-3 * HR), NOW)).toBe('3 hr ago');
    expect(timeAgo(inMs(-30 * HR), NOW)).toBe('yesterday');
    expect(timeAgo(inMs(-4 * 24 * HR), NOW)).toBe('4 days ago');
  });

  it('reads a future timestamp as just now and a bad one as empty', () => {
    expect(timeAgo(inMs(HR), NOW)).toBe('just now');
    expect(timeAgo('nope', NOW)).toBe('');
    expect(timeAgo(undefined, NOW)).toBe('');
  });

  it('falls back to a date after a month', () => {
    const out = timeAgo(inMs(-60 * 24 * HR), NOW);
    expect(out).not.toMatch(/ago/);
    expect(out).toMatch(/2026/);
  });
});

describe('formatBytes', () => {
  it('uses binary units', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(734 * 1024 * 1024)).toBe('734 MB');
    expect(formatBytes(4.2 * 1024 ** 3)).toBe('4.2 GB');
    expect(formatBytes(1.8 * 1024 ** 4)).toBe('1.8 TB');
  });

  it('is empty for nothing', () => {
    expect(formatBytes(undefined)).toBe('');
    expect(formatBytes(0)).toBe('');
    expect(formatBytes(-1)).toBe('');
  });
});

describe('downloadChip', () => {
  it('labels each state', () => {
    expect(downloadChip(dl({ state: 'searching' }), NOW)).toEqual({ label: 'Searching', tone: 'info', progress: null, message: '' });
    expect(downloadChip(dl({ state: 'queued' }), NOW)).toMatchObject({ label: 'Queued', progress: null });
    expect(downloadChip(dl({ state: 'import_pending' }), NOW)).toMatchObject({ label: 'Importing', tone: 'success', progress: 1 });
  });

  it('shows percentage and ETA while downloading', () => {
    const chip = downloadChip(dl({ state: 'downloading', progress: 0.426, eta: inMs(25 * MIN) }), NOW);
    expect(chip).toEqual({ label: 'Downloading 42% · ~25 min', tone: 'accent', progress: 0.426, message: '' });
  });

  it('never rounds a nearly-done download up to 100%', () => {
    expect(downloadChip(dl({ state: 'downloading', progress: 0.999 }), NOW).label).toBe('Downloading 99%');
  });

  it('degrades without progress or ETA', () => {
    expect(downloadChip(dl({ state: 'downloading', progress: 0.5 }), NOW).label).toBe('Downloading 50%');
    expect(downloadChip(dl({ state: 'downloading', progress: 0.5, eta: inMs(-MIN) }), NOW).label).toBe('Downloading 50%');
    const bare = downloadChip(dl({ state: 'downloading' }), NOW);
    expect(bare.label).toBe('Downloading');
    expect(bare.progress).toBe(0);
  });

  it('clamps progress into 0..1', () => {
    expect(downloadChip(dl({ state: 'downloading', progress: 1.7 }), NOW).progress).toBe(1);
    expect(downloadChip(dl({ state: 'downloading', progress: -2 }), NOW).progress).toBe(0);
    expect(downloadChip(dl({ state: 'stalled', progress: NaN }), NOW).progress).toBeNull();
  });

  it('carries the message for stalled and failed only', () => {
    expect(downloadChip(dl({ state: 'stalled', progress: 0.3, message: ' No seeders ' }), NOW)).toEqual({
      label: 'Stalled', tone: 'warn', progress: 0.3, message: 'No seeders',
    });
    expect(downloadChip(dl({ state: 'failed', message: 'Import failed: sample file' }), NOW)).toEqual({
      label: 'Failed', tone: 'error', progress: null, message: 'Import failed: sample file',
    });
    expect(downloadChip(dl({ state: 'searching', message: 'ignored' }), NOW).message).toBe('');
  });

  it('shows an unknown future state neutrally', () => {
    const chip = downloadChip(dl({ state: 'waiting_for_disk' as RequestDownload['state'] }), NOW);
    expect(chip).toEqual({ label: 'Waiting for disk', tone: 'info', progress: null, message: '' });
  });
});

describe('row predicates', () => {
  type R = Pick<MediaRequest, 'status' | 'download'>;
  const r = (status: MediaRequest['status'], download?: RequestDownload): R => ({ status, download });

  it('showsDownload only while an approved / downloading request has a download', () => {
    expect(showsDownload(r('downloading', dl({ state: 'queued' })))).toBe(true);
    expect(showsDownload(r('approved', dl({ state: 'searching' })))).toBe(true);
    // Not yet flipped to failed by the server: the chip carries it.
    expect(showsDownload(r('downloading', dl({ state: 'failed', message: 'x' })))).toBe(true);
    expect(showsDownload(r('failed', dl({ state: 'failed' })))).toBe(false);
    expect(showsDownload(r('available', dl({ state: 'import_pending' })))).toBe(false);
    expect(showsDownload(r('downloading'))).toBe(false);
  });

  it('failureReason explains every failed request', () => {
    expect(failureReason(r('failed', dl({ state: 'failed', message: ' Disk full ' })))).toBe('Disk full');
    expect(failureReason(r('failed', dl({ state: 'searching', message: 'Gave up after 7 days' })))).toBe('Gave up after 7 days');
    expect(failureReason(r('failed', dl({ state: 'failed' })))).toMatch(/No reason/);
    expect(failureReason(r('failed'))).toMatch(/No reason/);
    // Not failed (yet): the chip shows the message instead.
    expect(failureReason(r('downloading', dl({ state: 'failed', message: 'x' })))).toBe('');
  });

  it('isActiveRequest: still moving on its own', () => {
    expect(isActiveRequest(r('downloading'))).toBe(true); // not synced yet
    expect(isActiveRequest(r('approved'))).toBe(true);
    for (const state of ['searching', 'queued', 'downloading', 'import_pending'] as const) {
      expect(isActiveRequest(r('downloading', dl({ state })))).toBe(true);
    }
    expect(isActiveRequest(r('downloading', dl({ state: 'stalled' })))).toBe(false);
    expect(isActiveRequest(r('downloading', dl({ state: 'failed' })))).toBe(false);
    expect(isActiveRequest(r('failed', dl({ state: 'failed' })))).toBe(false);
    expect(isActiveRequest(r('pending'))).toBe(false);
    expect(isActiveRequest(r('available', dl({ state: 'downloading' })))).toBe(false);
    expect(hasActiveRequests([r('available'), r('pending')])).toBe(false);
    expect(hasActiveRequests([r('available'), r('downloading', dl({ state: 'queued' }))])).toBe(true);
    expect(hasActiveRequests([])).toBe(false);
  });
});

describe('createPoller', () => {
  function fakeDoc() {
    const listeners = new Set<() => void>();
    const doc = {
      visibilityState: 'visible' as DocumentVisibilityState,
      addEventListener: (_t: string, fn: () => void) => void listeners.add(fn),
      removeEventListener: (_t: string, fn: () => void) => void listeners.delete(fn),
      setVisibility(v: DocumentVisibilityState) {
        doc.visibilityState = v;
        listeners.forEach((fn) => fn());
      },
      listenerCount: () => listeners.size,
    };
    return doc;
  }

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  function setup(active = true) {
    const doc = fakeDoc();
    const state = { active };
    const tick = vi.fn().mockResolvedValue(undefined);
    const poller = createPoller({ isActive: () => state.active, tick, doc: doc as unknown as Document });
    return { doc, state, tick, poller };
  }

  it('ticks every interval while active and visible', async () => {
    const { tick, poller } = setup();
    poller.sync();
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS - 1);
    expect(tick).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(tick).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    expect(tick).toHaveBeenCalledTimes(2);
    poller.stop();
  });

  it('does not arm when nothing is active, and stops once nothing is', async () => {
    const { tick, poller, state } = setup(false);
    poller.sync();
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3);
    expect(tick).not.toHaveBeenCalled();

    state.active = true;
    poller.sync();
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    expect(tick).toHaveBeenCalledTimes(1);

    state.active = false; // e.g. the tick's reload found everything done
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3);
    expect(tick).toHaveBeenCalledTimes(1);
    poller.stop();
  });

  it('pauses while hidden and re-reads at once when visible again', async () => {
    const { tick, poller, doc } = setup();
    poller.sync();
    doc.setVisibility('hidden');
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3);
    expect(tick).not.toHaveBeenCalled();

    doc.setVisibility('visible');
    await vi.advanceTimersByTimeAsync(0);
    expect(tick).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    expect(tick).toHaveBeenCalledTimes(2);
    poller.stop();
  });

  it('does not arm while the page is hidden', async () => {
    const { tick, poller, doc } = setup();
    doc.setVisibility('hidden');
    poller.sync();
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 2);
    expect(tick).not.toHaveBeenCalled();
    poller.stop();
  });

  it('keeps going after a failed tick, and never overlaps ticks', async () => {
    const { tick, poller } = setup();
    let release: () => void = () => {};
    tick.mockRejectedValueOnce(new Error('offline'));
    tick.mockImplementationOnce(() => new Promise<void>((res) => (release = res)));
    poller.sync();
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    expect(tick).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    expect(tick).toHaveBeenCalledTimes(2);
    // Second tick still in flight: a sync (the list changed) must not stack a third.
    poller.sync();
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3);
    expect(tick).toHaveBeenCalledTimes(2);
    release();
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    expect(tick).toHaveBeenCalledTimes(3);
    poller.stop();
  });

  it('stop() disarms and drops the visibility listener', async () => {
    const { tick, poller, doc } = setup();
    poller.sync();
    poller.stop();
    expect(doc.listenerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 2);
    doc.setVisibility('visible');
    expect(tick).not.toHaveBeenCalled();
  });
});
