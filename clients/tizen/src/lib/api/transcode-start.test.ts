import { describe, expect, it, vi } from 'vitest';

const post = vi.hoisted(() => vi.fn(async () => ({})));
vi.mock('./client', async (orig) => {
  const real = await orig<typeof import('./client')>();
  return { ...real, api: { ...real.api, post } };
});

import { transcode } from './endpoints';

describe('transcode.start', () => {
  it('asks for MPEG-TS segments: AVPlay refuses the fMP4 HLS the server writes for HEVC', async () => {
    await transcode.start({ itemId: 'i1', fileId: 'f1', height: 0, positionMs: 0, videoCopy: true, supportsHEVC: true });
    expect(post).toHaveBeenCalledTimes(1);
    const [url, body] = post.mock.calls[0] as unknown as [string, Record<string, unknown>];
    expect(url).toBe('/api/v1/items/i1/transcode');
    expect(body).toMatchObject({ file_id: 'f1', video_copy: true, supports_hevc: true, segment_container: 'ts' });
  });
});
