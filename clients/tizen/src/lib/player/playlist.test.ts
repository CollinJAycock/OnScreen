import { describe, expect, it } from 'vitest';
import { firstVariantUrl, mediaPlaylistSpan, resolveUrl } from './playlist';

const MASTER = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-STREAM-INF:BANDWIDTH=8000000,RESOLUTION=3840x2160,CODECS="hvc1.2.4.L150"
index.m3u8
`;

const GROWING = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:4
#EXT-X-PLAYLIST-TYPE:EVENT
#EXTINF:4.000000,
seg0.ts
#EXTINF:4.000000,
seg1.ts
#EXTINF:3.5,
seg2.ts
`;

describe('firstVariantUrl', () => {
  it("resolves a master playlist's first variant, keeping the session token", () => {
    expect(firstVariantUrl(MASTER, 'https://tv.example/api/v1/transcode/sessions/s1/master.m3u8?token=abc')).toBe(
      'https://tv.example/api/v1/transcode/sessions/s1/index.m3u8?token=abc',
    );
  });

  it('is null for a media playlist', () => {
    expect(firstVariantUrl(GROWING, 'https://tv.example/x.m3u8')).toBeNull();
  });
});

describe('mediaPlaylistSpan', () => {
  it('adds up what a growing session has written, not yet ended', () => {
    expect(mediaPlaylistSpan(GROWING)).toEqual({ producedEndSec: 11.5, ended: false });
  });

  it('is ended once the playlist carries ENDLIST', () => {
    expect(mediaPlaylistSpan(`${GROWING}#EXT-X-ENDLIST\n`)).toEqual({ producedEndSec: 11.5, ended: true });
  });

  it('is an empty, growing span before the first segment', () => {
    expect(mediaPlaylistSpan('#EXTM3U\n#EXT-X-TARGETDURATION:4\n')).toEqual({ producedEndSec: 0, ended: false });
  });

  it('is null for a master playlist or anything that is not a playlist', () => {
    expect(mediaPlaylistSpan(MASTER)).toBeNull();
    expect(mediaPlaylistSpan('<html>503</html>')).toBeNull();
  });
});

describe('resolveUrl', () => {
  it("keeps a URI's own query over the base's", () => {
    expect(resolveUrl('seg.ts?token=new', 'https://h/a/b.m3u8?token=old')).toBe('https://h/a/seg.ts?token=new');
  });
});
