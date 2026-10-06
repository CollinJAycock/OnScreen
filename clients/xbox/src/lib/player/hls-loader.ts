// Lazy-load hls.js only when the player mounts. Keeps the library bundle
// out of every other page.

import type Hls from 'hls.js';

let cached: Promise<typeof Hls> | null = null;

export function loadHls(): Promise<typeof Hls> {
  if (!cached) {
    cached = import('hls.js').then((m) => m.default);
  }
  return cached;
}
