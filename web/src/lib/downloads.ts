// Converted (lower-quality) downloads: shared helpers for the Download menu
// on a title's page and the Downloads list on the Account page.
import { ApiRequestError, type DownloadJob } from '$lib/api';

/** How often a page polls a job that is still being prepared. */
export const DOWNLOAD_POLL_MS = 2000;

/** queued / running: the server is still preparing the file. */
export function isPreparing(job: Pick<DownloadJob, 'status'>): boolean {
  return job.status === 'queued' || job.status === 'running';
}

/** Whole-percent progress, 0..100. */
export function progressPercent(job: Pick<DownloadJob, 'progress'>): number {
  const p = Number.isFinite(job.progress) ? job.progress : 0;
  return Math.max(0, Math.min(100, Math.floor(p * 100)));
}

/** One-line status for a job. */
export function downloadStatusText(job: Pick<DownloadJob, 'status' | 'progress' | 'error'>): string {
  switch (job.status) {
    case 'queued':
      return 'Waiting to start';
    case 'running':
      return `Preparing · ${progressPercent(job)}%`;
    case 'ready':
      return 'Ready';
    case 'failed':
      return job.error ? `Failed: ${job.error}` : 'Failed';
    case 'expired':
      return 'Expired — ask for it again to re-create it';
    default:
      return job.status;
  }
}

/** "1.4 GB", "730 MB", "12 KB". */
export function formatBytes(n: number | undefined | null): string {
  if (!n || n <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** A user-facing message for a failed create / fetch. */
export function downloadErrorMessage(err: unknown): string {
  if (err instanceof ApiRequestError) {
    switch (err.code) {
      case 'QUALITY_ABOVE_LIMIT':
        return 'That quality is above your streaming limit. Pick a lower one.';
      case 'TOO_MANY_DOWNLOADS':
        return 'You already have downloads being prepared. Wait for one to finish.';
      case 'DOWNLOADS_DISABLED':
        return 'Downloads are turned off on this server.';
      case 'NOT_CONVERTIBLE':
        return err.message || "This file can't be converted.";
    }
    if (err.message) return err.message;
  }
  return err instanceof Error && err.message ? err.message : 'Download failed';
}

/** Suggested file name for a converted download (the server sends the same
 *  name in Content-Disposition; the native client can't read that). */
export function convertedFilename(title: string, qualityLabel: string): string {
  const safe = (title || 'download').replace(/[\\/:*?"<>|]/g, '_');
  return `${safe} (${qualityLabel}).mp4`;
}

/** Start a browser download of url (already through assetUrl) with a plain
 *  link click: the server's Content-Disposition makes it a save-as. */
export function startBrowserDownload(url: string): void {
  const a = document.createElement('a');
  a.href = url;
  a.download = '';
  a.rel = 'noopener';
  a.style.display = 'none';
  document.body.appendChild(a);
  a.click();
  a.remove();
}
