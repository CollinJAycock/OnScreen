// Optimized versions (admin "Optimize…"): shared helpers for the Optimize
// dialog, the per-item panel and the Settings ▸ Library ▸ Optimized list.
//
// Wording comes from the i18n catalog. The display helpers take the
// translator as an optional last argument: Svelte callers pass $i18n so the
// text re-renders when the language changes; it defaults to the current one.
import { get } from 'svelte/store';
import { ApiRequestError, type OptimizeJob, type OptimizeResult } from '$lib/api';
import { formatBytes } from '$lib/downloads';
import { i18n, type Translator } from '$lib/i18n/index.svelte';

/** How often a list polls while a job is queued or encoding. */
export const OPTIMIZE_POLL_MS = 3000;

/** Library types whose items can be optimized (video). */
export const OPTIMIZABLE_LIBRARY_TYPES = ['movie', 'show', 'dvr', 'home_video', 'anime', 'cartoons'];

/** queued / running: the server is still encoding it. */
export function isOptimizing(job: Pick<OptimizeJob, 'status'>): boolean {
  return job.status === 'queued' || job.status === 'running';
}

/** Whole-percent progress, 0..100. */
export function optimizeProgress(job: Pick<OptimizeJob, 'progress'>): number {
  const p = Number.isFinite(job.progress) ? job.progress : 0;
  return Math.max(0, Math.min(100, Math.floor(p * 100)));
}

/** One-line status: "Waiting", "Encoding · 42%", "Ready · 720p · 4.2 Mbps · 1.1 GB". */
export function optimizeStatusText(job: OptimizeJob, tx: Translator = get(i18n)): string {
  switch (job.status) {
    case 'queued':
      return tx('ui.downloads.status.queued');
    case 'running':
      return tx('lib.optimize.encoding', { pct: optimizeProgress(job) });
    case 'ready': {
      const parts = [tx('ui.downloads.status.ready')];
      if (job.height) parts.push(`${job.height}p`);
      if (job.bitrate_kbps) parts.push(`${(job.bitrate_kbps / 1000).toFixed(1)} Mbps`);
      const size = formatBytes(job.size_bytes);
      if (size) parts.push(size);
      return parts.join(' · ');
    }
    case 'failed':
      return job.error ? tx('lib.optimize.failedWith', { error: job.error }) : tx('requests.status.failed');
    default:
      return job.status;
  }
}

/** The title a row shows: "Show · S1E2 · Episode" for an episode. */
export function optimizeJobTitle(job: OptimizeJob, tx: Translator = get(i18n)): string {
  if (job.item_type === 'episode' && job.show_title) {
    const se =
      job.season_index != null && job.episode_index != null
        ? `S${job.season_index}E${job.episode_index}`
        : '';
    return [job.show_title, se, job.title].filter(Boolean).join(' · ');
  }
  return job.title || tx('browse.untitled');
}

/** The toast after "Optimize…": what was queued and what wasn't. */
export function optimizeResultMessage(r: OptimizeResult, tx: Translator = get(i18n)): string {
  if (r.items === 0) return tx('lib.optimize.nothing');
  const parts: string[] = [];
  if (r.queued) parts.push(tx('ui.optimize.count.queued', { n: r.queued }));
  if (r.existing) parts.push(tx.n('lib.optimize.existing', r.existing));
  if (r.skipped) parts.push(tx.n('lib.optimize.skipped', r.skipped));
  const list = parts.join(tx('ui.bulkEdit.listSeparator'));
  return r.truncated ? tx('lib.optimize.resultTruncated', { parts: list }) : tx('lib.optimize.result', { parts: list });
}

/** A user-facing message for a failed request. */
export function optimizeErrorMessage(err: unknown, tx: Translator = get(i18n)): string {
  if (err instanceof ApiRequestError) {
    if (err.status === 404) return tx('lib.optimize.notFound');
    if (err.message) return err.message;
  }
  return err instanceof Error ? err.message : tx('forgotPassword.genericError');
}
