import { describe, it, expect } from 'vitest';
import {
  clampPreamp,
  replayGainFromFile,
  replayGainLinear,
  selectReplayGain,
  type ReplayGainInfo,
} from './replaygain';

const db = (x: number) => Math.pow(10, x / 20);

describe('replayGainLinear', () => {
  const tagged: ReplayGainInfo = { trackGain: -6, trackPeak: 0.5, albumGain: -8, albumPeak: 0.6 };

  it('is unity when the mode is off, whatever the tags and preamp', () => {
    expect(replayGainLinear(tagged, 'off', 0)).toBe(1);
    expect(replayGainLinear(tagged, 'off', 12)).toBe(1);
  });

  it('applies the track gain in track mode', () => {
    expect(replayGainLinear(tagged, 'track', 0)).toBeCloseTo(db(-6), 10);
  });

  it('applies the album gain in album mode', () => {
    expect(replayGainLinear(tagged, 'album', 0)).toBeCloseTo(db(-8), 10);
  });

  it('adds the preamp to the tag gain', () => {
    expect(replayGainLinear(tagged, 'track', 3)).toBeCloseTo(db(-3), 10);
    expect(replayGainLinear(tagged, 'track', -4.5)).toBeCloseTo(db(-10.5), 10);
  });

  it('clamps the preamp to ±15 dB like the native engine', () => {
    const quiet: ReplayGainInfo = { trackGain: -30, trackPeak: 0.01 };
    expect(replayGainLinear(quiet, 'track', 40)).toBeCloseTo(db(-15), 10);
    expect(replayGainLinear(quiet, 'track', -40)).toBeCloseTo(db(-45), 10);
    expect(clampPreamp(99)).toBe(15);
    expect(clampPreamp(-99)).toBe(-15);
    expect(clampPreamp(Number.NaN)).toBe(0);
  });

  describe('clipping cap', () => {
    it('caps a boost so peak * gain <= 1', () => {
      // +6 dB (x1.995) on a 0.8 peak would reach 1.6 — capped to 1/0.8.
      const g = replayGainLinear({ trackGain: 6, trackPeak: 0.8 }, 'track', 0);
      expect(g).toBeCloseTo(1.25, 10);
      expect(0.8 * g).toBeLessThanOrEqual(1 + 1e-12);
    });

    it('leaves a boost alone when the peak has headroom', () => {
      // +3 dB (x1.413) on a 0.5 peak lands at 0.707 — no cap.
      expect(replayGainLinear({ trackGain: 3, trackPeak: 0.5 }, 'track', 0)).toBeCloseTo(db(3), 10);
    });

    it('caps the preamp boost too', () => {
      const g = replayGainLinear({ trackGain: -2, trackPeak: 0.9 }, 'track', 6);
      expect(0.9 * g).toBeCloseTo(1, 10);
    });

    it('attenuates a file whose peak is already over full scale', () => {
      // Lossy decodes can report peaks > 1; -1 dB alone would still clip.
      const g = replayGainLinear({ trackGain: -1, trackPeak: 1.2 }, 'track', 0);
      expect(g).toBeCloseTo(1 / 1.2, 10);
    });

    it('uses the album peak in album mode', () => {
      // +6 dB album gain, album peak 0.9 -> capped at 1/0.9 even though
      // this track's own peak (0.5) would have allowed the full boost.
      const g = replayGainLinear({ trackGain: 0, trackPeak: 0.5, albumGain: 6, albumPeak: 0.9 }, 'album', 0);
      expect(g).toBeCloseTo(1 / 0.9, 10);
    });

    it('treats an unknown peak as full scale: never boosts past unity', () => {
      expect(replayGainLinear({ trackGain: 4 }, 'track', 0)).toBe(1);
      expect(replayGainLinear({ trackGain: -3 }, 'track', 0)).toBeCloseTo(db(-3), 10);
      expect(replayGainLinear({ trackGain: -3, trackPeak: 0 }, 'track', 6)).toBe(1);
    });
  });

  describe('mode fallback', () => {
    it('album mode falls back to the track tags when there is no album gain', () => {
      const single: ReplayGainInfo = { trackGain: -5, trackPeak: 0.7 };
      expect(replayGainLinear(single, 'album', 0)).toBeCloseTo(db(-5), 10);
      expect(selectReplayGain(single, 'album')).toEqual({ gainDb: -5, peak: 0.7 });
    });

    it('album gain without an album peak borrows the track peak as the clip guard', () => {
      expect(selectReplayGain({ trackGain: 0, trackPeak: 0.95, albumGain: 2 }, 'album')).toEqual({
        gainDb: 2,
        peak: 0.95,
      });
    });

    it('track mode does not fall back to album tags', () => {
      expect(replayGainLinear({ albumGain: -7, albumPeak: 0.5 }, 'track', 0)).toBe(1);
      expect(selectReplayGain({ albumGain: -7 }, 'track')).toBeNull();
    });
  });

  describe('missing tags', () => {
    it('plays untagged files at unity — the preamp is not applied on its own', () => {
      expect(replayGainLinear({}, 'track', 6)).toBe(1);
      expect(replayGainLinear({}, 'album', -6)).toBe(1);
      expect(replayGainLinear(undefined, 'track', 6)).toBe(1);
      expect(replayGainLinear(null, 'album', 6)).toBe(1);
    });

    it('ignores non-finite tag values', () => {
      expect(replayGainLinear({ trackGain: Number.NaN, trackPeak: 0.5 }, 'track', 0)).toBe(1);
      const g = replayGainLinear({ trackGain: -6, trackPeak: Number.POSITIVE_INFINITY }, 'track', 0);
      expect(g).toBeCloseTo(db(-6), 10);
    });
  });
});

describe('replayGainFromFile', () => {
  it('maps the ItemFile replaygain_* fields', () => {
    expect(
      replayGainFromFile({
        replaygain_track_gain: -7.2,
        replaygain_track_peak: 0.98,
        replaygain_album_gain: -8.1,
        replaygain_album_peak: 0.99,
      }),
    ).toEqual({ trackGain: -7.2, trackPeak: 0.98, albumGain: -8.1, albumPeak: 0.99 });
  });

  it('returns an empty object for a file without tags or no file', () => {
    expect(replayGainFromFile({})).toEqual({});
    expect(replayGainFromFile(undefined)).toEqual({});
    expect(replayGainFromFile(null)).toEqual({});
  });

  it('keeps partial tags', () => {
    expect(replayGainFromFile({ replaygain_track_gain: 1.5 })).toEqual({ trackGain: 1.5 });
  });
});
