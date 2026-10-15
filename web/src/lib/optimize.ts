// Optimized versions (admin "Optimize…"): shared helpers for the Optimize
// dialog, the per-item panel and the Settings ▸ Library ▸ Optimized list.
import { ApiRequestError, type OptimizeJob, type OptimizeResult } from '$lib/api';
import { formatBytes } from '$lib/downloads';

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
export function optimizeStatusText(job: OptimizeJob): string {
  switch (job.status) {
    case 'queued':
      return 'Waiting to start';
    case 'running':
      return `Encoding · ${optimizeProgress(job)}%`;
    case 'ready': {
      const parts = ['Ready'];
      if (job.height) parts.push(`${job.height}p`);
      if (job.bitrate_kbps) parts.push(`${(job.bitrate_kbps / 1000).toFixed(1)} Mbps`);
      const size = formatBytes(job.size_bytes);
      if (size) parts.push(size);
      return parts.join(' · ');
    }
    case 'failed':
      return job.error ? `Failed: ${job.error}` : 'Failed';
    default:
      return job.status;
  }
}

/** The title a row shows: "Show · S1E2 · Episode" for an episode. */
export function optimizeJobTitle(job: OptimizeJob): string {
  if (job.item_type === 'episode' && job.show_title) {
    const se =
      job.season_index != null && job.episode_index != null
        ? `S${job.season_index}E${job.episode_index}`
        : '';
    return [job.show_title, se, job.title].filter(Boolean).join(' · ');
  }
  return job.title || 'Untitled';
}

/** The toast after "Optimize…": what was queued and what wasn't. */
export function optimizeResultMessage(r: OptimizeResult): string {
  if (r.items === 0) return 'Nothing to optimize here — no video files.';
  const parts: string[] = [];
  if (r.queued) parts.push(`${r.queued} queued`);
  if (r.existing) parts.push(`${r.existing} already optimized or in progress`);
  if (r.skipped) parts.push(`${r.skipped} can't be optimized`);
  let msg = parts.join(', ');
  if (r.truncated) msg += ' — the selection is larger than one request; run it again for the rest';
  return msg.charAt(0).toUpperCase() + msg.slice(1) + '.';
}

/** A user-facing message for a failed request. */
export function optimizeErrorMessage(err: unknown): string {
  if (err instanceof ApiRequestError) {
    if (err.status === 404) return 'Not found — it may have been deleted.';
    if (err.message) return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong.';
}
