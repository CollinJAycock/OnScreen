import { describe, expect, it } from 'vitest';
import { ApiRequestError, type OptimizeJob } from '$lib/api';
import {
  isOptimizing,
  optimizeErrorMessage,
  optimizeJobTitle,
  optimizeProgress,
  optimizeResultMessage,
  optimizeStatusText,
} from './optimize';

function job(over: Partial<OptimizeJob> = {}): OptimizeJob {
  return {
    id: 'j1', item_id: 'i1', file_id: 'f1', profile: 'mobile_720p',
    profile_label: 'Mobile · 720p 4 Mbps', status: 'queued', progress: 0,
    created_at: '2026-10-14T12:00:00Z', ...over,
  };
}

describe('optimize helpers', () => {
  it('reads queued and running as in progress', () => {
    expect(isOptimizing({ status: 'queued' })).toBe(true);
    expect(isOptimizing({ status: 'running' })).toBe(true);
    expect(isOptimizing({ status: 'ready' })).toBe(false);
    expect(isOptimizing({ status: 'failed' })).toBe(false);
  });

  it('clamps progress to whole percent', () => {
    expect(optimizeProgress({ progress: 0.426 })).toBe(42);
    expect(optimizeProgress({ progress: 3 })).toBe(100);
    expect(optimizeProgress({ progress: Number.NaN })).toBe(0);
  });

  it('describes each state', () => {
    expect(optimizeStatusText(job())).toBe('Waiting to start');
    expect(optimizeStatusText(job({ status: 'running', progress: 0.5 }))).toBe('Encoding · 50%');
    expect(optimizeStatusText(job({ status: 'ready', height: 720, bitrate_kbps: 4160, size_bytes: 1536 * 1024 * 1024 })))
      .toBe('Ready · 720p · 4.2 Mbps · 1.5 GB');
    expect(optimizeStatusText(job({ status: 'failed', error: 'the file is damaged' }))).toBe('Failed: the file is damaged');
  });

  it('names episodes by show and number', () => {
    expect(optimizeJobTitle(job({ item_type: 'episode', show_title: 'Show', season_index: 1, episode_index: 2, title: 'Pilot' })))
      .toBe('Show · S1E2 · Pilot');
    expect(optimizeJobTitle(job({ item_type: 'movie', title: 'Film' }))).toBe('Film');
  });

  it('summarises a request', () => {
    expect(optimizeResultMessage({ profile: 'tv_1080p', items: 3, queued: 1, existing: 1, skipped: 1 }))
      .toBe("1 queued, 1 already optimized or in progress, 1 can't be optimized.");
    expect(optimizeResultMessage({ profile: 'tv_1080p', items: 0, queued: 0, existing: 0, skipped: 0 }))
      .toBe('Nothing to optimize here — no video files.');
    expect(optimizeResultMessage({ profile: 'tv_1080p', items: 5000, queued: 5000, existing: 0, skipped: 0, truncated: true }))
      .toContain('run it again');
  });

  it('turns errors into messages', () => {
    expect(optimizeErrorMessage(new ApiRequestError('gone', 404, 'NOT_FOUND'))).toContain('Not found');
    expect(optimizeErrorMessage(new ApiRequestError('profile must be one of …', 422, 'VALIDATION_ERROR'))).toBe('profile must be one of …');
  });
});
