import { describe, expect, it } from 'vitest';
import appinfo from '../appinfo.json';

// What appinfo.json tells webOS about the app's behaviour (LG's appinfo.json
// reference and guides).

const info = appinfo as Record<string, unknown>;

describe('appinfo behaviour', () => {
  // With false, webOS answers Back with history.back() and the app never
  // sees keyCode 461: no back stack, picker or dialog closes, and the first
  // screen can't offer to exit (lib/appExit).
  it('takes the Back key itself (disableBackHistoryAPI)', () => {
    expect(info.disableBackHistoryAPI).toBe(true);
  });

  // Type 2 (LG's screensaver guide): OSD-area dimming and a 30 min timeout,
  // for apps that show UI while media plays, so the OLED screensaver doesn't
  // cut music and audiobooks (the now-playing view) or the photo slideshow.
  // Not type 3, which switches the picture to Gallery mode (wrong for video).
  it('asks for the type 2 screensaver, not Gallery mode', () => {
    expect(info.screenSaverProperties).toEqual({ preferredType: 2 });
    expect(info.useGalleryMode).toBeUndefined();
  });
});
