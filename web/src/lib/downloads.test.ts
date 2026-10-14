import { describe, expect, it } from 'vitest';
import { ApiRequestError } from '$lib/api';
import {
  convertedFilename,
  downloadErrorMessage,
  downloadStatusText,
  formatBytes,
  isPreparing,
  progressPercent,
} from './downloads';

describe('downloads helpers', () => {
  it('reads queued and running as preparing', () => {
    expect(isPreparing({ status: 'queued' })).toBe(true);
    expect(isPreparing({ status: 'running' })).toBe(true);
    expect(isPreparing({ status: 'ready' })).toBe(false);
    expect(isPreparing({ status: 'failed' })).toBe(false);
  });

  it('clamps progress to whole percent', () => {
    expect(progressPercent({ progress: 0.426 })).toBe(42);
    expect(progressPercent({ progress: 1.4 })).toBe(100);
    expect(progressPercent({ progress: -1 })).toBe(0);
    expect(progressPercent({ progress: NaN })).toBe(0);
  });

  it('describes each status', () => {
    expect(downloadStatusText({ status: 'queued', progress: 0 })).toBe('Waiting to start');
    expect(downloadStatusText({ status: 'running', progress: 0.5 })).toBe('Preparing · 50%');
    expect(downloadStatusText({ status: 'ready', progress: 1 })).toBe('Ready');
    expect(downloadStatusText({ status: 'failed', progress: 0, error: 'ffmpeg: boom' })).toBe('Failed: ffmpeg: boom');
    expect(downloadStatusText({ status: 'expired', progress: 1 })).toMatch(/^Expired/);
  });

  it('formats sizes', () => {
    expect(formatBytes(0)).toBe('');
    expect(formatBytes(undefined)).toBe('');
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(730 * 1024 * 1024)).toBe('730 MB');
    expect(formatBytes(1.4 * 1024 ** 3)).toBe('1.4 GB');
  });

  it('maps server error codes to plain messages', () => {
    expect(downloadErrorMessage(new ApiRequestError('x', 403, 'QUALITY_ABOVE_LIMIT'))).toMatch(/streaming limit/);
    expect(downloadErrorMessage(new ApiRequestError('x', 429, 'TOO_MANY_DOWNLOADS'))).toMatch(/already have downloads/);
    expect(downloadErrorMessage(new ApiRequestError('x', 403, 'DOWNLOADS_DISABLED'))).toMatch(/turned off/);
    expect(downloadErrorMessage(new ApiRequestError('server says no', 500, 'INTERNAL'))).toBe('server says no');
    expect(downloadErrorMessage('weird')).toBe('Download failed');
  });

  it('builds a safe file name', () => {
    expect(convertedFilename('Alien: Covenant', '720p · 4 Mbps')).toBe('Alien_ Covenant (720p · 4 Mbps).mp4');
    expect(convertedFilename('', '480p · 1 Mbps')).toBe('download (480p · 1 Mbps).mp4');
  });
});
