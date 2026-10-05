import { describe, expect, it } from 'vitest';
import { loadHls } from './hls-loader';

describe('loadHls on Tizen', () => {
  // AVPlay plays the playlist: the page must take its native-HLS path.
  it('never loads hls.js, and says it is unsupported', async () => {
    const Hls = await loadHls();
    expect(Hls.isSupported()).toBe(false);
  });
});
