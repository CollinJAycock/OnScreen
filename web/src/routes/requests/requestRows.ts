// Row formatting for the Requests page: the requested-seasons label, the live
// Radarr / Sonarr download chip, ETA / relative-time text and the "is anything
// still moving" check that drives list polling. Pulled out of +page.svelte so
// the rules can be unit-tested without mounting the page.
//
// Pure module — no DOM, no Svelte store dependencies (createPoller takes the
// document it watches as a parameter).

import type { MediaRequest, RequestDownload } from '$lib/api';

/** How often a list with an in-flight download is re-read while visible. */
export const POLL_INTERVAL_MS = 30_000;

type DownloadState = RequestDownload['state'];

/** Download states that are still expected to change on their own. */
const ACTIVE_STATES: ReadonlySet<string> = new Set<DownloadState>([
  'searching',
  'queued',
  'downloading',
  'import_pending',
]);

/** Compress sorted season numbers into "1–3, 5" (runs of three or more
 *  become ranges; a pair stays "1, 2"). */
function seasonRuns(nums: number[]): string {
  const parts: string[] = [];
  let i = 0;
  while (i < nums.length) {
    let j = i;
    while (j + 1 < nums.length && nums[j + 1] === nums[j] + 1) j++;
    if (j - i >= 2) parts.push(`${nums[i]}–${nums[j]}`);
    else for (let k = i; k <= j; k++) parts.push(String(nums[k]));
    i = j + 1;
  }
  return parts.join(', ');
}

/**
 * The seasons a TV request asked for: "All seasons" (the server's meaning of
 * an empty list), "Season 2", "Seasons 1–3, 5", with season 0 as "Specials".
 */
export function seasonsLabel(seasons: readonly number[] | null | undefined): string {
  const list = [...new Set((seasons ?? []).filter((n) => Number.isInteger(n) && n >= 0))].sort(
    (a, b) => a - b,
  );
  if (list.length === 0) return 'All seasons';
  const specials = list[0] === 0;
  const regular = specials ? list.slice(1) : list;
  if (regular.length === 0) return 'Specials';
  const label = `${regular.length === 1 ? 'Season' : 'Seasons'} ${seasonRuns(regular)}`;
  return specials ? `${label} + Specials` : label;
}

/**
 * Time left until an RFC3339 ETA, for the download chip: "<1 min", "~25 min",
 * "~2 hr 5 min", "~3 days". Empty when the ETA is missing, unparseable or
 * already past (a stale ETA is noise next to the percentage).
 */
export function formatEta(eta: string | null | undefined, now: number = Date.now()): string {
  if (!eta) return '';
  const t = Date.parse(eta);
  if (!Number.isFinite(t)) return '';
  const sec = (t - now) / 1000;
  if (sec <= 0) return '';
  if (sec < 60) return '<1 min';
  const min = Math.round(sec / 60);
  if (min < 60) return `~${min} min`;
  if (min < 24 * 60) {
    const hr = Math.floor(min / 60);
    const rem = min % 60;
    return rem && hr < 10 ? `~${hr} hr ${rem} min` : `~${hr} hr`;
  }
  const days = Math.round(min / (24 * 60));
  return `~${days} ${days === 1 ? 'day' : 'days'}`;
}

/** "just now", "5 min ago", "3 hr ago", "yesterday", "4 days ago", then a
 *  plain date after a month. A timestamp in the future (clock skew) reads as
 *  "just now". Empty for an unparseable value. */
export function timeAgo(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return '';
  const sec = Math.max(0, Math.floor((now - t) / 1000));
  if (sec < 60) return 'just now';
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} min ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} hr ago`;
  const day = Math.floor(hr / 24);
  if (day === 1) return 'yesterday';
  if (day < 30) return `${day} days ago`;
  return new Date(t).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

/** Human size, binary units: "734 MB", "4.2 GB", "1.8 TB". */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let v = bytes;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${v >= 100 || u === 0 ? Math.round(v) : v.toFixed(1)} ${units[u]}`;
}

export type ChipTone = 'info' | 'accent' | 'success' | 'warn' | 'error';

export interface DownloadChip {
  /** "Searching", "Downloading 42% · ~25 min", "Importing", … */
  label: string;
  tone: ChipTone;
  /** 0..1 for the thin progress bar; null hides the bar. */
  progress: number | null;
  /** Why it's stuck — only for stalled / failed. */
  message: string;
}

function clampProgress(p: number | null | undefined): number | null {
  if (p == null || !Number.isFinite(p)) return null;
  return Math.min(1, Math.max(0, p));
}

/**
 * The chip for a request's live download state. Unknown future states fall
 * back to a neutral chip with the raw state name, so a newer server doesn't
 * break an older page.
 */
export function downloadChip(d: RequestDownload, now: number = Date.now()): DownloadChip {
  const progress = clampProgress(d.progress);
  const message = (d.message ?? '').trim();
  switch (d.state) {
    case 'searching':
      return { label: 'Searching', tone: 'info', progress: null, message: '' };
    case 'queued':
      return { label: 'Queued', tone: 'info', progress: progress && progress > 0 ? progress : null, message: '' };
    case 'downloading': {
      const parts = ['Downloading'];
      if (progress != null) parts[0] += ` ${Math.floor(progress * 100)}%`;
      const eta = formatEta(d.eta, now);
      if (eta) parts.push(eta);
      return { label: parts.join(' · '), tone: 'accent', progress: progress ?? 0, message: '' };
    }
    case 'import_pending':
      return { label: 'Importing', tone: 'success', progress: 1, message: '' };
    case 'stalled':
      return { label: 'Stalled', tone: 'warn', progress, message };
    case 'failed':
      return { label: 'Failed', tone: 'error', progress: null, message };
    default: {
      const raw = String(d.state ?? '').replace(/_/g, ' ').trim();
      const label = raw ? raw.charAt(0).toUpperCase() + raw.slice(1) : 'Unknown';
      return { label, tone: 'info', progress: null, message: '' };
    }
  }
}

/** Whether the row shows the live download chip: only while the request is
 *  in flight. A failed request's status pill already says so (its reason is
 *  failureReason), and a download block left on a request that has since
 *  become available is stale. */
export function showsDownload(req: Pick<MediaRequest, 'status' | 'download'>): boolean {
  if (!req.download) return false;
  return req.status === 'approved' || req.status === 'downloading';
}

/**
 * The "why it failed" line under a failed request: the reason the arr sync
 * left in download.message, or a stand-in so the "Failed" pill never stands
 * unexplained. Empty for anything not failed.
 */
export function failureReason(req: Pick<MediaRequest, 'status' | 'download'>): string {
  if (req.status !== 'failed') return '';
  const msg = (req.download?.message ?? '').trim();
  return msg || 'No reason was reported by Radarr / Sonarr.';
}

/**
 * Still moving on its own: a searching / queued / downloading / importing
 * download, or an approved request the arr sync hasn't reported on yet.
 * Stalled and failed downloads need a person, so they don't keep the list
 * polling.
 */
export function isActiveRequest(req: Pick<MediaRequest, 'status' | 'download'>): boolean {
  if (req.status !== 'approved' && req.status !== 'downloading') return false;
  if (!req.download) return true;
  return ACTIVE_STATES.has(req.download.state);
}

export function hasActiveRequests(list: readonly Pick<MediaRequest, 'status' | 'download'>[]): boolean {
  return list.some(isActiveRequest);
}

export interface Poller {
  /** Re-evaluate after the list changed: (re)arm the timer when something is
   *  active and the page is visible, disarm otherwise. */
  sync(): void;
  /** Tear down for good (component destroy). */
  stop(): void;
}

export interface PollerOptions {
  /** Is there anything worth re-reading? Read fresh on every decision. */
  isActive: () => boolean;
  /** Re-read the list. Errors are swallowed; the next tick tries again. */
  tick: () => Promise<void>;
  intervalMs?: number;
  /** The document whose visibility gates polling (a stub in tests). */
  doc?: Pick<Document, 'visibilityState' | 'addEventListener' | 'removeEventListener'>;
}

/**
 * Re-reads a list every intervalMs while isActive() and the page is visible.
 * Hiding the tab disarms it; coming back re-reads at once (the list may be
 * minutes old) and re-arms. One tick at a time — a slow response delays the
 * next tick rather than stacking requests.
 */
export function createPoller(opts: PollerOptions): Poller {
  const intervalMs = opts.intervalMs ?? POLL_INTERVAL_MS;
  const doc = opts.doc ?? (typeof document !== 'undefined' ? document : undefined);
  let timer: ReturnType<typeof setTimeout> | null = null;
  let inflight = false;
  let stopped = false;

  const visible = () => !doc || doc.visibilityState !== 'hidden';

  function clear() {
    if (timer != null) {
      clearTimeout(timer);
      timer = null;
    }
  }

  function schedule() {
    clear();
    if (stopped || inflight || !visible() || !opts.isActive()) return;
    timer = setTimeout(fire, intervalMs);
  }

  async function fire() {
    timer = null;
    if (stopped || inflight || !visible() || !opts.isActive()) return;
    inflight = true;
    try {
      await opts.tick();
    } catch {
      /* keep the last list; try again next tick */
    } finally {
      inflight = false;
    }
    schedule();
  }

  function onVisibility() {
    if (stopped) return;
    if (!visible()) {
      clear();
      return;
    }
    if (timer == null && !inflight && opts.isActive()) void fire();
  }

  doc?.addEventListener('visibilitychange', onVisibility);

  return {
    sync: schedule,
    stop() {
      stopped = true;
      clear();
      doc?.removeEventListener('visibilitychange', onVisibility);
    },
  };
}
