// The Live TV player: one stream per channel visit, and a channel that keeps
// failing ends at an error instead of retrying forever behind a black player.
import { render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const fake = vi.hoisted(() => {
  type Handler = (event: string, data: unknown) => void;
  class FakeHls {
    static instances: FakeHls[] = [];
    static isSupported() { return true; }
    static Events = { ERROR: 'hlsError', FRAG_BUFFERED: 'hlsFragBuffered' };
    static ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError', OTHER_ERROR: 'otherError' };
    handlers: Record<string, Handler[]> = {};
    startLoad = vi.fn();
    recoverMediaError = vi.fn();
    destroy = vi.fn();
    loadSource = vi.fn();
    attachMedia = vi.fn();
    constructor(public config: unknown) { FakeHls.instances.push(this); }
    on(ev: string, fn: Handler) { (this.handlers[ev] ??= []).push(fn); }
    emit(ev: string, data: unknown) { for (const fn of this.handlers[ev] ?? []) fn(ev, data); }
  }
  return { FakeHls };
});

vi.mock('hls.js', () => ({ default: fake.FakeHls }));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/stores/page.svelte', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL('http://localhost/tv/ch-1'), params: { id: 'ch-1' }, state: {} }).subscribe(fn),
  },
}));
const nowNext = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({
  liveTvApi: {
    channels: vi.fn().mockResolvedValue({ items: [{ id: 'ch-1', number: '2.1', name: 'News', tuner_type: 'hdhomerun' }], total: 1 }),
    nowNext,
    streamUrl: (id: string) => `/api/v1/tv/channels/${id}/stream.m3u8`,
  },
}));

import Page from './+page.svelte';

beforeEach(() => {
  fake.FakeHls.instances = [];
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: 'u1', username: 'viewer' }));
  nowNext.mockResolvedValue({ items: [], total: 0 });
});

describe('Live TV channel page', () => {
  it('attaches the stream once on the first load', async () => {
    render(Page);
    await waitFor(() => expect(fake.FakeHls.instances.length).toBe(1));
    await new Promise((r) => setTimeout(r, 30));
    expect(fake.FakeHls.instances.length).toBe(1);
    expect(nowNext).toHaveBeenCalledTimes(1);
  });

  it('gives up with an error after repeated fatal errors, but rides out ones between fragments', async () => {
    render(Page);
    await waitFor(() => expect(fake.FakeHls.instances.length).toBe(1));
    const hls = fake.FakeHls.instances[0];
    const fatal = { fatal: true, type: 'networkError', details: 'manifestLoadError' };
    // Recovered, then playing again: the budget refills.
    for (let i = 0; i < 4; i++) hls.emit('hlsError', fatal);
    hls.emit('hlsFragBuffered', {});
    for (let i = 0; i < 5; i++) hls.emit('hlsError', fatal);
    expect(hls.startLoad).toHaveBeenCalledTimes(9);
    expect(screen.queryByText('manifestLoadError')).toBeNull();
    // One more in a row: an error, and the stream is torn down.
    hls.emit('hlsError', fatal);
    expect(hls.startLoad).toHaveBeenCalledTimes(9);
    expect(await screen.findByText('manifestLoadError')).toBeTruthy();
    expect(hls.destroy).toHaveBeenCalled();
  });
});
