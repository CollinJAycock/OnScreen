// The player page is shared with the webOS app, which plays server sessions
// through hls.js. On a Samsung TV AVPlay plays the HLS playlist itself
// (avplayMedia.ts): hls.js is never loaded, and isSupported() says no, so the
// page takes its native-HLS path (`video.src = url`), which lands on AVPlay.
// hls.js stays a dev dependency for its types and the first-load tests only;
// nothing of it is bundled.

import type Hls from 'hls.js';

const NO_HLS = { isSupported: () => false } as unknown as typeof Hls;

export function loadHls(): Promise<typeof Hls> {
  return Promise.resolve(NO_HLS);
}
