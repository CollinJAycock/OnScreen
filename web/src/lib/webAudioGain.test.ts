import { describe, it, expect, vi } from 'vitest';
import { MediaGainRouter, isSameOrigin, probeStream, type ProbeVerdict } from './webAudioGain';

// Minimal Web Audio fakes: enough surface for the router, with the calls
// recorded so the tests can assert the graph shape and gain automation.
class FakeParam {
  value: number;
  events: Array<[string, ...number[]]> = [];
  constructor(v: number) {
    this.value = v;
  }
  cancelScheduledValues(t: number) {
    this.events.push(['cancel', t]);
  }
  setValueAtTime(v: number, t: number) {
    this.events.push(['set', v, t]);
    this.value = v;
  }
  setTargetAtTime(v: number, t: number, tau: number) {
    this.events.push(['target', v, t, tau]);
  }
}

class FakeNode {
  connections: unknown[] = [];
  connect(n: unknown) {
    this.connections.push(n);
    return n;
  }
}

class FakeGain extends FakeNode {
  gain = new FakeParam(1);
}

class FakeContext {
  state: 'suspended' | 'running' | 'closed' = 'suspended';
  currentTime = 12.5;
  destination = new FakeNode();
  tapped = new Set<unknown>();
  gains: FakeGain[] = [];
  allowStart = true;
  resume = vi.fn(async () => {
    if (this.allowStart) this.state = 'running';
    else await new Promise(() => {}); // autoplay-blocked: never settles
  });
  createGain() {
    const g = new FakeGain();
    this.gains.push(g);
    return g;
  }
  createMediaElementSource(el: unknown) {
    if (this.tapped.has(el)) throw new DOMException('already connected', 'InvalidStateError');
    this.tapped.add(el);
    return new FakeNode();
  }
}

function setup(over: { probe?: (url: string) => Promise<ProbeVerdict>; allowStart?: boolean; active?: boolean } = {}) {
  const ctx = new FakeContext();
  ctx.allowStart = over.allowStart ?? true;
  const log = vi.fn();
  const probe = vi.fn(over.probe ?? (async () => 'safe' as ProbeVerdict));
  const router = new MediaGainRouter({
    createContext: () => ctx as unknown as AudioContext,
    probe,
    origin: () => 'https://media.example',
    hasBeenActive: () => over.active ?? true,
    startTimeoutMs: 20,
    log,
  });
  return { ctx, router, probe, log };
}

const el = () => document.createElement('audio');

describe('isSameOrigin', () => {
  it('resolves relative paths against the page origin', () => {
    expect(isSameOrigin('/media/stream/f1', 'https://media.example')).toBe(true);
    expect(isSameOrigin('https://media.example/media/stream/f1', 'https://media.example')).toBe(true);
    expect(isSameOrigin('https://other.example/media/stream/f1', 'https://media.example')).toBe(false);
  });
});

describe('MediaGainRouter.canRoute', () => {
  it('routes a same-origin stream once the context runs and the probe passes', async () => {
    const { router, ctx, probe } = setup();
    router.unlock();
    expect(await router.canRoute('/media/stream/f1')).toBe(true);
    expect(ctx.state).toBe('running');
    expect(probe).toHaveBeenCalledWith('/media/stream/f1');
    // The pass is remembered for the session.
    expect(await router.canRoute('/media/stream/f2')).toBe(true);
    expect(probe).toHaveBeenCalledTimes(1);
  });

  it('never routes a cross-origin URL and disables the path (logged once)', async () => {
    const { router, log, probe } = setup();
    expect(await router.canRoute('https://cdn.example/media/stream/f1')).toBe(false);
    expect(await router.canRoute('/media/stream/f1')).toBe(false);
    expect(router.disabled).toBe(true);
    expect(router.disabledReason).toMatch(/another origin/);
    expect(probe).not.toHaveBeenCalled();
    expect(log).toHaveBeenCalledTimes(1);
  });

  it('disables the path when the stream redirects (object storage)', async () => {
    const { router } = setup({ probe: async () => 'redirect' });
    router.unlock();
    expect(await router.canRoute('/media/stream/f1')).toBe(false);
    expect(router.disabledReason).toMatch(/redirected/);
    expect(router.attach(el(), 1)).toBe(false);
  });

  it('retries an inconclusive probe on the next track', async () => {
    let n = 0;
    const { router, probe } = setup({ probe: async () => (++n === 1 ? 'unknown' : 'safe') });
    router.unlock();
    expect(await router.canRoute('/media/stream/f1')).toBe(false);
    expect(router.disabled).toBe(false);
    expect(await router.canRoute('/media/stream/f2')).toBe(true);
    expect(probe).toHaveBeenCalledTimes(2);
  });

  it('gives up (without disabling) when the context cannot start, until the next gesture', async () => {
    const { router, ctx, log } = setup({ allowStart: false });
    router.unlock();
    expect(await router.canRoute('/media/stream/f1')).toBe(false);
    expect(router.disabled).toBe(false);
    expect(log).toHaveBeenCalledWith(expect.stringMatching(/could not start/));
    // No second wait (that would put a gap between gapless tracks)...
    ctx.resume.mockClear();
    expect(await router.canRoute('/media/stream/f2')).toBe(false);
    expect(ctx.resume).not.toHaveBeenCalled();
    // ...until the user interacts again.
    ctx.allowStart = true;
    router.unlock();
    expect(await router.canRoute('/media/stream/f3')).toBe(true);
  });

  it('does not create a context before any user activation', async () => {
    const create = vi.fn();
    const router = new MediaGainRouter({
      createContext: create,
      probe: async () => 'safe',
      origin: () => 'https://media.example',
      hasBeenActive: () => false,
      log: () => {},
    });
    expect(await router.canRoute('/media/stream/f1')).toBe(false);
    expect(create).not.toHaveBeenCalled();
  });
});

describe('MediaGainRouter.attach / setGain', () => {
  it('wires element -> gain -> destination with the initial gain', async () => {
    const { router, ctx } = setup();
    router.unlock();
    await router.canRoute('/media/stream/f1');
    const a = el();
    expect(router.attach(a, 0.4)).toBe(true);
    expect(router.isRouted(a)).toBe(true);
    const g = ctx.gains[0];
    expect(g.gain.value).toBeCloseTo(0.4);
    expect(g.connections).toEqual([ctx.destination]);
    // Idempotent.
    expect(router.attach(a, 1)).toBe(true);
    expect(ctx.gains).toHaveLength(1);
  });

  it('leaves an element alone if something else already tapped it', async () => {
    const { router, ctx, log } = setup();
    router.unlock();
    await router.canRoute('/media/stream/f1');
    const a = el();
    ctx.createMediaElementSource(a); // e.g. an analyser harness
    expect(router.attach(a, 1)).toBe(false);
    expect(router.isRouted(a)).toBe(false);
    expect(log).toHaveBeenCalledWith(expect.stringMatching(/already has a media source/));
  });

  it('sets the gain immediately for a paused element and fades a playing one', async () => {
    const { router, ctx } = setup();
    router.unlock();
    await router.canRoute('/media/stream/f1');
    const a = el();
    router.attach(a, 0);
    const p = ctx.gains[0].gain;

    router.setGain(a, 0.5, false);
    expect(p.events).toEqual([
      ['cancel', 12.5],
      ['set', 0.5, 12.5],
    ]);
    expect(router.gainOf(a)).toBe(0.5);

    p.events = [];
    router.setGain(a, 0.25, true);
    expect(p.events[0]).toEqual(['cancel', 12.5]);
    expect(p.events[1]).toEqual(['set', 0.5, 12.5]);
    expect(p.events[2][0]).toBe('target');
    expect(p.events[2][1]).toBe(0.25);
    // Lands exactly on the target after the fade.
    expect(p.events[3][0]).toBe('set');
    expect(p.events[3][1]).toBe(0.25);
    expect(p.events[3][2]).toBeGreaterThan(12.5);

    // No-op when unchanged.
    p.events = [];
    router.setGain(a, 0.25, true);
    expect(p.events).toEqual([]);
  });

  it('treats non-finite / negative gains as silence rather than throwing', async () => {
    const { router } = setup();
    router.unlock();
    await router.canRoute('/media/stream/f1');
    const a = el();
    router.attach(a, 1);
    router.setGain(a, Number.NaN, false);
    expect(router.gainOf(a)).toBe(0);
    router.setGain(a, -1, false);
    expect(router.gainOf(a)).toBe(0);
  });
});

describe('probeStream', () => {
  it('classifies redirects, successes and failures', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    try {
      fetchMock.mockResolvedValueOnce({ type: 'opaqueredirect', status: 0, ok: false });
      expect(await probeStream('/media/stream/f1')).toBe('redirect');
      fetchMock.mockResolvedValueOnce({ type: 'basic', status: 206, ok: true });
      expect(await probeStream('/media/stream/f1')).toBe('safe');
      fetchMock.mockResolvedValueOnce({ type: 'basic', status: 401, ok: false });
      expect(await probeStream('/media/stream/f1')).toBe('unknown');
      fetchMock.mockRejectedValueOnce(new TypeError('network'));
      expect(await probeStream('/media/stream/f1')).toBe('unknown');

      const init = fetchMock.mock.calls[0][1] as RequestInit;
      expect(init.redirect).toBe('manual');
      expect((init.headers as Record<string, string>).Range).toBe('bytes=0-0');
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
