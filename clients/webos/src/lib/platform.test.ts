import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  CAPS_CACHE_KEY,
  CONFIG_NAMES,
  CONFIG_URI,
  FHD_PANEL,
  UHD_PANEL,
  panelSizeFromDeviceInfo,
  parseConfigsResponse,
  probePanelCaps,
  readCachedCaps,
  readPanelSize,
  resolvePanelSize,
  uhdFromPanelResolution,
  writeCachedCaps,
  type KeyValueStore,
  type ServiceBridge,
} from './platform';

// PalmSystem.deviceInfo as webOS hands it over: a JSON string.
const FHD_DEVICE_INFO = JSON.stringify({
  modelName: '43LM6300PLA',
  platformVersion: '6.0.0',
  platformVersionMajor: '6',
  screenWidth: 1920,
  screenHeight: 1080,
});
const UHD_DEVICE_INFO = JSON.stringify({
  modelName: 'OLED55C1PUB',
  platformVersion: '6.3.0',
  platformVersionMajor: '6',
  screenWidth: 3840,
  screenHeight: 2160,
});

function memoryStore(init: Record<string, string> = {}): KeyValueStore & { data: Record<string, string> } {
  const data = { ...init };
  return {
    data,
    getItem: (k) => (k in data ? data[k] : null),
    setItem: (k, v) => {
      data[k] = v;
    },
  };
}

describe('panelSizeFromDeviceInfo', () => {
  it('reads an FHD panel', () => {
    expect(panelSizeFromDeviceInfo(FHD_DEVICE_INFO)).toEqual(FHD_PANEL);
  });

  it('reads a UHD panel', () => {
    expect(panelSizeFromDeviceInfo(UHD_DEVICE_INFO)).toEqual(UHD_PANEL);
  });

  it('accepts an already-parsed object', () => {
    expect(panelSizeFromDeviceInfo({ screenWidth: 1920, screenHeight: 1080 })).toEqual(FHD_PANEL);
  });

  it('caps an 8K panel at 3840x2160', () => {
    expect(panelSizeFromDeviceInfo({ screenWidth: 7680, screenHeight: 4320 })).toEqual(UHD_PANEL);
  });

  it('floors an HD panel at 1080p (every webOS TV decodes it)', () => {
    expect(panelSizeFromDeviceInfo({ screenWidth: 1366, screenHeight: 768 })).toEqual(FHD_PANEL);
  });

  it('takes a UHD flag over a smaller reported plane', () => {
    expect(panelSizeFromDeviceInfo({ screenWidth: 1920, screenHeight: 1080, UHD: 'true' })).toEqual(UHD_PANEL);
    expect(panelSizeFromDeviceInfo({ screenWidth: 1920, screenHeight: 1080, uhd8K: true })).toEqual(UHD_PANEL);
  });

  it('is null without a usable size', () => {
    expect(panelSizeFromDeviceInfo(undefined)).toBeNull();
    expect(panelSizeFromDeviceInfo('not json')).toBeNull();
    expect(panelSizeFromDeviceInfo(JSON.stringify({ modelName: 'x' }))).toBeNull();
    expect(panelSizeFromDeviceInfo({ screenWidth: 0, screenHeight: 1080 })).toBeNull();
    expect(panelSizeFromDeviceInfo('[]')).toBeNull();
  });
});

describe('getConfigs reply', () => {
  it('reads HDR and the panel class', () => {
    const reply = JSON.stringify({
      returnValue: true,
      configs: { 'tv.model.supportHDR': true, 'tv.hw.panelResolution': 'UD' },
    });
    expect(parseConfigsResponse(reply)).toEqual({ hdr: true, uhd: true });
  });

  it('reads string values and an FHD panel', () => {
    const reply = { returnValue: true, configs: { 'tv.model.supportHDR': 'false', 'tv.hw.panelResolution': 'FHD' } };
    expect(parseConfigsResponse(reply)).toEqual({ hdr: false, uhd: false });
  });

  it('leaves a missing key unknown', () => {
    const reply = { returnValue: true, configs: {}, missingConfigs: ['tv.model.supportHDR'] };
    expect(parseConfigsResponse(reply)).toEqual({ hdr: null, uhd: null });
  });

  it('is null for a failed or denied call', () => {
    expect(parseConfigsResponse({ returnValue: false, errorCode: -1, errorText: 'Denied method call' })).toBeNull();
    expect(parseConfigsResponse('garbage')).toBeNull();
    expect(parseConfigsResponse({ returnValue: true })).toBeNull();
  });

  it('maps panel resolutions', () => {
    expect(uhdFromPanelResolution('UD')).toBe(true);
    expect(uhdFromPanelResolution('8K')).toBe(true);
    expect(uhdFromPanelResolution('hd')).toBe(false);
    expect(uhdFromPanelResolution('QHD')).toBeNull();
    expect(uhdFromPanelResolution(undefined)).toBeNull();
  });
});

describe('resolvePanelSize', () => {
  it('keeps the long-standing 3840x2160 when nothing is known', () => {
    expect(resolvePanelSize(null, null)).toEqual(UHD_PANEL);
  });

  it("never claims less than 3840x2160 on deviceInfo alone (it may be the graphics plane)", () => {
    expect(resolvePanelSize(FHD_PANEL, null)).toEqual(UHD_PANEL);
    expect(resolvePanelSize(UHD_PANEL, null)).toEqual(UHD_PANEL);
  });

  it("lets Luna's panel class win", () => {
    expect(resolvePanelSize(FHD_PANEL, true)).toEqual(UHD_PANEL);
    expect(resolvePanelSize(UHD_PANEL, false)).toEqual(FHD_PANEL);
    expect(resolvePanelSize(null, false)).toEqual(FHD_PANEL);
  });
});

describe('caps cache', () => {
  it('round-trips an answer', () => {
    const store = memoryStore();
    writeCachedCaps(store, { hdr: true, uhd: null });
    expect(readCachedCaps(store)).toEqual({ hdr: true, uhd: null });
  });

  it('ignores an empty or broken entry', () => {
    expect(readCachedCaps(memoryStore({ [CAPS_CACHE_KEY]: '{' }))).toBeNull();
    expect(readCachedCaps(memoryStore({ [CAPS_CACHE_KEY]: '{"hdr":null,"uhd":null}' }))).toBeNull();
    expect(readCachedCaps(null)).toBeNull();
  });

  it('survives a store that throws', () => {
    const broken: KeyValueStore = {
      getItem: () => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('blocked');
      },
    };
    expect(readCachedCaps(broken)).toBeNull();
    expect(() => writeCachedCaps(broken, { hdr: true, uhd: true })).not.toThrow();
  });
});

describe('probePanelCaps', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function fakeBridge(reply?: unknown) {
    const calls: Array<[string, string]> = [];
    let cancelled = 0;
    const bridge: ServiceBridge = {
      onservicecallback: null,
      call(uri, params) {
        calls.push([uri, params]);
        if (reply !== undefined) {
          const cb = bridge.onservicecallback;
          // The real bridge answers asynchronously.
          setTimeout(() => cb?.(typeof reply === 'string' ? reply : JSON.stringify(reply)), 0);
        }
      },
      cancel() {
        cancelled++;
      },
    };
    return { bridge, calls, cancelled: () => cancelled };
  }

  it('asks getConfigs for HDR and the panel class, then caches the answer', async () => {
    const fake = fakeBridge({ returnValue: true, configs: { 'tv.model.supportHDR': true, 'tv.hw.panelResolution': 'UD' } });
    const store = memoryStore();
    const caps = await new Promise((resolve) => probePanelCaps({ createBridge: () => fake.bridge, store }, resolve));
    expect(caps).toEqual({ hdr: true, uhd: true });
    expect(fake.calls).toEqual([[CONFIG_URI, JSON.stringify({ configNames: CONFIG_NAMES })]]);
    expect(readCachedCaps(store)).toEqual({ hdr: true, uhd: true });
    expect(fake.cancelled()).toBe(1);
  });

  it('caches nothing when the call is denied', async () => {
    const fake = fakeBridge({ returnValue: false, errorCode: -1, errorText: 'Denied method call' });
    const store = memoryStore();
    const caps = await new Promise((resolve) => probePanelCaps({ createBridge: () => fake.bridge, store }, resolve));
    expect(caps).toBeNull();
    expect(store.data).toEqual({});
  });

  it('answers null at once without a bridge', () => {
    const done = vi.fn();
    probePanelCaps({ createBridge: null, store: null }, done);
    expect(done).toHaveBeenCalledWith(null);
  });

  it('answers null when making the bridge throws', () => {
    const done = vi.fn();
    probePanelCaps(
      {
        createBridge: () => {
          throw new Error('no bridge');
        },
        store: null,
      },
      done,
    );
    expect(done).toHaveBeenCalledWith(null);
  });

  it('gives up after the timeout, and ignores a late reply', () => {
    vi.useFakeTimers();
    const fake = fakeBridge(); // never answers on its own
    const done = vi.fn();
    probePanelCaps({ createBridge: () => fake.bridge, store: null, timeoutMs: 1000 }, done);
    vi.advanceTimersByTime(999);
    expect(done).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(done).toHaveBeenCalledWith(null);
    fake.bridge.onservicecallback?.(JSON.stringify({ returnValue: true, configs: { 'tv.model.supportHDR': true } }));
    expect(done).toHaveBeenCalledTimes(1);
  });
});

describe('readPanelSize (runtime)', () => {
  it('falls back to 3840x2160 outside webOS', () => {
    expect(readPanelSize()).toEqual(UHD_PANEL);
  });
});
