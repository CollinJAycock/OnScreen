import { describe, expect, it } from 'vitest';
import { DECODE_PROBES, MSE_PROBES, collectDiagnostics, type DiagEnv } from './diagnostics';

function env(over: Partial<DiagEnv> = {}): DiagEnv {
  return {
    isTypeSupported: (t) => t.includes('avc1') || t.includes('mp4a'),
    canPlayHls: () => '',
    decodingInfo: async () => ({ supported: true, smooth: true, powerEfficient: false }),
    matches: (q) => q === '(dynamic-range: high)',
    info: { Version: '0.1.0', Shell: 'xbox' },
    capabilityHeader: 'videoDecoder=h264,hdr=1',
    ...over,
  };
}

describe('collectDiagnostics', () => {
  it('reports every probe, grouped, with the header the server gets', async () => {
    const sections = await collectDiagnostics(env());
    expect(sections.map((s) => s.title)).toEqual([
      'This console',
      'Display',
      'MSE (what hls.js can play)',
      'Decoding (MediaCapabilities)',
      'Sent to the server',
    ]);
    const mse = sections[2].rows;
    expect(mse).toHaveLength(MSE_PROBES.length);
    expect(mse.find((r) => r.label === 'H.264 High 4.0 (1080p)')).toMatchObject({ value: 'yes', ok: true });
    expect(mse.find((r) => r.label === 'E-AC-3 (Dolby Digital Plus)')).toMatchObject({ value: 'no', ok: false });
    expect(sections[3].rows).toHaveLength(DECODE_PROBES.length);
    expect(sections[3].rows[0].value).toBe('yes, smooth');
    expect(sections[1].rows[0]).toMatchObject({ value: 'yes', ok: true });
    expect(sections[4].rows[0].value).toBe('videoDecoder=h264,hdr=1');
  });

  it('reads a failing or missing probe as unknown instead of throwing', async () => {
    const sections = await collectDiagnostics(
      env({
        isTypeSupported: () => null,
        decodingInfo: async () => {
          throw new Error('not supported');
        },
        matches: () => null,
      }),
    );
    expect(sections[2].rows.every((r) => r.value === 'unknown')).toBe(true);
    expect(sections[3].rows.every((r) => r.value === 'unknown' && r.ok === null)).toBe(true);
    expect(sections[1].rows[0].value).toBe('unknown');
  });
});
