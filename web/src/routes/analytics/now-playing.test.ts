import { fireEvent, render, screen } from '@testing-library/svelte';
import type { ActiveSession } from '$lib/api';
import NowPlayingCard from './NowPlayingCard.svelte';
import StopStreamDialog from './StopStreamDialog.svelte';
import {
  canStop, channelsLabel, containerLabel, decisionLabel, decisionTone, formatStream, locationLabel,
  resolutionLabel, sourceOutput, stopMessageError, viewerInitial, viewerLabel, STOP_MESSAGE_MAX,
} from './now-playing';

vi.mock('$lib/api', () => ({ assetUrl: (p: string) => p }));

const base: ActiveSession = {
  id: '10.0.0.5|item|user',
  decision: 'directPlay',
  position_ms: 60_000,
  started_at: '2026-09-28T10:00:00Z',
  title: 'Dune',
  duration_ms: 600_000,
};

describe('now-playing helpers', () => {
  it('labels decisions and tones', () => {
    expect(['directPlay', 'directStream', 'remux', 'transcode'].map(decisionLabel))
      .toEqual(['Direct Play', 'Direct Stream', 'Remux', 'Transcoding']);
    expect(['directPlay', 'remux', 'directStream', 'transcode'].map(decisionTone))
      .toEqual(['direct', 'stream', 'stream', 'transcode']);
  });

  it('names the viewer, owner first for managed profiles', () => {
    expect(viewerLabel({ username: 'collin', profile_name: 'Kids' })).toBe('collin · Kids');
    expect(viewerLabel({ username: 'sam' })).toBe('sam');
    expect(viewerLabel({})).toBe('');
    expect(viewerInitial({ username: 'collin', profile_name: 'kids' })).toBe('K');
    expect(viewerInitial({ username: 'sam' })).toBe('S');
    expect(viewerInitial({})).toBe('?');
  });

  it('labels location, resolution, channels, containers', () => {
    expect([locationLabel('lan'), locationLabel('remote'), locationLabel(undefined)]).toEqual(['LAN', 'Remote', '']);
    expect(resolutionLabel(3840, 2160)).toBe('4K');
    expect(resolutionLabel(1920, 800)).toBe('1080p'); // scope crop: width wins
    expect(resolutionLabel(1280, 720)).toBe('720p');
    expect(resolutionLabel(undefined, undefined)).toBe('');
    expect([channelsLabel(8), channelsLabel(6), channelsLabel(2), channelsLabel(0)]).toEqual(['7.1', '5.1', 'Stereo', '']);
    expect(containerLabel('matroska,webm')).toBe('MKV');
    expect(containerLabel('hls')).toBe('HLS');
  });

  it('formats a source → output line', () => {
    expect(formatStream({ video_codec: 'hevc', width: 3840, height: 2160, hdr: 'hdr10', audio_codec: 'eac3', audio_channels: 6, bitrate_kbps: 25_000 }))
      .toBe('HEVC 4K HDR10 · E-AC-3 5.1 · 25.0 Mbps');
    expect(sourceOutput({ decision: 'directPlay', source: { video_codec: 'h264', height: 1080 } }))
      .toEqual({ source: 'H.264 1080p', output: 'Original file' });
    expect(sourceOutput({ decision: 'transcode', source: { video_codec: 'hevc' }, output: { video_codec: 'h264', height: 720, audio_codec: 'aac', audio_channels: 2 } }))
      .toEqual({ source: 'HEVC', output: 'H.264 720p · AAC Stereo' });
    expect(sourceOutput({ decision: 'transcode' })).toEqual({ source: '', output: '' });
  });

  it('hides Stop only when the server says it cannot act', () => {
    expect(canStop({})).toBe(true); // older server: field absent
    expect(canStop({ can_stop: true })).toBe(true);
    expect(canStop({ can_stop: false })).toBe(false);
  });

  it('caps the stop message in characters', () => {
    expect(stopMessageError('é'.repeat(STOP_MESSAGE_MAX))).toBe('');
    expect(stopMessageError('é'.repeat(STOP_MESSAGE_MAX + 1))).toMatch(/under 200/);
  });
});

describe('NowPlayingCard', () => {
  it('renders who / where / how for a transcode, with reasons', () => {
    render(NowPlayingCard, {
      session: {
        ...base,
        decision: 'transcode',
        username: 'collin',
        profile_name: 'Kids',
        client_name: 'Living Room TV',
        client_ip: '203.0.113.9',
        location: 'remote',
        source: { video_codec: 'hevc', width: 3840, height: 2160, audio_codec: 'truehd', audio_channels: 8 },
        output: { container: 'hls', video_codec: 'h264', height: 720, audio_codec: 'aac', audio_channels: 6, bitrate_kbps: 4000 },
        transcode_reasons: ['HEVC not supported by client', '7.1 audio → client max 5.1'],
        can_stop: true,
      },
    });
    expect(screen.getByText('collin · Kids')).toBeTruthy();
    expect(screen.getByText('K')).toBeTruthy();
    expect(screen.getByText(/Living Room TV/)).toBeTruthy();
    expect(screen.getByText('Remote')).toBeTruthy();
    expect(screen.getByText('203.0.113.9')).toBeTruthy();
    expect(screen.getByText('Transcoding')).toBeTruthy();
    const formats = document.querySelector('.formats')!.textContent!;
    expect(formats).toContain('HEVC 4K · TrueHD 7.1');
    expect(formats).toContain('H.264 720p · AAC 5.1 · 4.0 Mbps');
    const reasons = screen.getByRole('list', { name: /direct play/ });
    expect(reasons.textContent).toContain('HEVC not supported by client');
    expect(reasons.textContent).toContain('7.1 audio → client max 5.1');
    expect(screen.getByRole('button', { name: 'Stop' })).toBeTruthy();
  });

  it('shows no reasons for a direct play and hides Stop when the server cannot act', () => {
    render(NowPlayingCard, {
      session: { ...base, location: 'lan', transcode_reasons: ['stale'], can_stop: false, source: { video_codec: 'h264', height: 1080 } },
    });
    expect(screen.getByText('Direct Play')).toBeTruthy();
    expect(screen.getByText('LAN')).toBeTruthy();
    expect(screen.queryByRole('list')).toBeNull();
    expect(document.querySelector('.formats')!.textContent).toContain('Original file');
    expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();
  });

  it('renders a bare card from an older server', () => {
    render(NowPlayingCard, { session: { ...base, bitrate_kbps: 8000, client_name: 'Chrome' } });
    expect(screen.getByText('Dune')).toBeTruthy();
    expect(screen.getByText(/8\.0 Mbps/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeTruthy();
  });

  it('calls onstop with the session', async () => {
    const onstop = vi.fn();
    render(NowPlayingCard, { session: base, onstop });
    await fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(onstop).toHaveBeenCalledWith(base);
  });
});

describe('StopStreamDialog', () => {
  it('confirms with the trimmed message', async () => {
    const onconfirm = vi.fn();
    render(StopStreamDialog, { session: { ...base, username: 'sam', client_name: 'Pixel 8' }, onconfirm, oncancel: vi.fn() });
    expect(screen.getByRole('dialog').textContent).toContain('sam');
    expect(screen.getByRole('dialog').textContent).toContain('two minutes'); // direct play: refusal window explained
    await fireEvent.input(screen.getByLabelText(/Message to the viewer/), { target: { value: '  Maintenance at 9  ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Stop stream' }));
    expect(onconfirm).toHaveBeenCalledWith('Maintenance at 9');
  });

  it('blocks an over-long message', async () => {
    const onconfirm = vi.fn();
    render(StopStreamDialog, { session: { ...base, decision: 'transcode' }, onconfirm, oncancel: vi.fn() });
    expect(screen.getByRole('dialog').textContent).not.toContain('two minutes'); // transcode: no refusal window
    await fireEvent.input(screen.getByLabelText(/Message to the viewer/), { target: { value: 'x'.repeat(201) } });
    expect((screen.getByRole('button', { name: 'Stop stream' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole('alert').textContent).toMatch(/under 200/);
  });

  it('cancels on Escape and via the Cancel button; shows a server error', async () => {
    const oncancel = vi.fn();
    render(StopStreamDialog, { session: base, error: 'Server said no', onconfirm: vi.fn(), oncancel });
    expect(screen.getByRole('alert').textContent).toBe('Server said no');
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(oncancel).toHaveBeenCalledTimes(2);
  });
});
