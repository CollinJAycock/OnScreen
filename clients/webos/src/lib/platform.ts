// What the LG TV says about its own panel, for the capability header
// (lib/api/capabilities.ts): the resolution the video plane can show and
// whether it shows HDR. Android reads both off its Display; a web app on
// webOS has two sources:
//
//   - PalmSystem.deviceInfo (webOSSystem.deviceInfo from webOS 5): a JSON
//     string the runtime sets synchronously, no Luna call, no permission.
//     Its screenWidth / screenHeight are meant to be the video plane
//     (3840x2160 on a UHD set, 1920x1080 on an FHD one); Shaka Player sizes
//     its ABR ceiling from the same fields. Not yet confirmed on our TVs, so
//     it only sizes an FHD answer from Luna (see resolvePanelSize).
//   - Luna `com.webos.service.config/getConfigs` through PalmServiceBridge,
//     the call LG's own webOSTV.js deviceInfo() and Shaka make:
//     tv.model.supportHDR (the panel shows HDR10) and tv.hw.panelResolution
//     ('UD' on UHD sets). Asynchronous, so it is asked once and the answer
//     kept in localStorage: a panel never changes.
//
// matchMedia('(dynamic-range: high)') needs Chrome 98, so on webOS 6/22/23
// (Chromium 79/87/94) it is always false; that is why HDR asks Luna.
// Everything here is guarded: without PalmSystem or PalmServiceBridge (a
// desktop browser, the emulator, a future firmware that denies the call) the
// header keeps its long-standing values, 3840x2160 and the media query.

export interface PanelSize {
  width: number;
  height: number;
}

export const UHD_PANEL: PanelSize = { width: 3840, height: 2160 };
export const FHD_PANEL: PanelSize = { width: 1920, height: 1080 };

/** What getConfigs said. null = it didn't say (key missing or unknown). */
export interface PanelCaps {
  /** tv.model.supportHDR */
  hdr: boolean | null;
  /** tv.hw.panelResolution: true for UHD / 8K, false for FHD / HD. */
  uhd: boolean | null;
}

export const CONFIG_URI = 'luna://com.webos.service.config/getConfigs';
export const CONFIG_NAMES = ['tv.model.supportHDR', 'tv.hw.panelResolution'];
export const CAPS_CACHE_KEY = 'onscreen:panel-caps';
/** Give up on a Luna call that never answers. */
export const PROBE_TIMEOUT_MS = 5000;

type Info = Record<string, unknown>;

/** deviceInfo / a Luna reply: a JSON string, or already an object. */
function parseObject(raw: unknown): Info | null {
  let v = raw;
  if (typeof v === 'string') {
    try {
      v = JSON.parse(v);
    } catch {
      return null;
    }
  }
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Info) : null;
}

/** true / false from a boolean or its string form; null for anything else. */
function flag(v: unknown): boolean | null {
  if (v === true || v === 'true') return true;
  if (v === false || v === 'false') return false;
  return null;
}

/**
 * The panel size in a deviceInfo value, or null when it has none. A UHD
 * flag, or a plane of 3840x2160 or more, is UHD. The claim never goes past
 * 3840x2160: 8K over MSE is unverified, and the server only needs to know a
 * 4K stream is fine. Smaller planes are floored at 1920x1080, as Shaka does:
 * every webOS TV decodes 1080p (an HD panel scales it down).
 */
export function panelSizeFromDeviceInfo(raw: unknown): PanelSize | null {
  const info = parseObject(raw);
  if (!info) return null;
  if (flag(info.UHD) || flag(info.uhd) || flag(info.uhd8K)) return UHD_PANEL;
  const w = Number(info.screenWidth);
  const h = Number(info.screenHeight);
  if (!(w > 0) || !(h > 0)) return null;
  return {
    width: Math.min(UHD_PANEL.width, Math.max(FHD_PANEL.width, Math.round(w))),
    height: Math.min(UHD_PANEL.height, Math.max(FHD_PANEL.height, Math.round(h))),
  };
}

/** tv.hw.panelResolution: 'UD' (UHD) and '8K' are UHD, 'FHD' and 'HD' are
 *  not; anything else is unknown. */
export function uhdFromPanelResolution(v: unknown): boolean | null {
  if (typeof v !== 'string') return null;
  const r = v.trim().toUpperCase();
  if (r === 'UD' || r === 'UHD' || r === '4K' || r === '8K') return true;
  if (r === 'FHD' || r === 'HD') return false;
  return null;
}

/** The getConfigs reply, or null when the call failed (returnValue false,
 *  "Denied method call", no configs). */
export function parseConfigsResponse(raw: unknown): PanelCaps | null {
  const res = parseObject(raw);
  if (!res || res.returnValue === false) return null;
  const configs = parseObject(res.configs);
  if (!configs) return null;
  return {
    hdr: flag(configs['tv.model.supportHDR']),
    uhd: uhdFromPanelResolution(configs['tv.hw.panelResolution']),
  };
}

/**
 * The size the header claims: Luna's panel class when it answered (the
 * hardware config), else 3840x2160, the claim this app has always made.
 * deviceInfo alone never lowers it: whether its screenWidth/Height is the
 * video plane or the 1920x1080 graphics plane on every firmware is untested
 * on a TV, and a UHD set wrongly read as FHD would have the server scale
 * every 4K title down to 1080p. Only Luna's explicit FHD/HD does, sized by
 * deviceInfo when that is smaller still.
 */
export function resolvePanelSize(fromDeviceInfo: PanelSize | null, lunaUhd: boolean | null): PanelSize {
  if (lunaUhd === false) return fromDeviceInfo && fromDeviceInfo.width < UHD_PANEL.width ? fromDeviceInfo : FHD_PANEL;
  return UHD_PANEL;
}

export interface KeyValueStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export function readCachedCaps(store: KeyValueStore | null): PanelCaps | null {
  try {
    const v = parseObject(store?.getItem(CAPS_CACHE_KEY) ?? null);
    if (!v) return null;
    const caps = { hdr: flag(v.hdr), uhd: flag(v.uhd) };
    return caps.hdr === null && caps.uhd === null ? null : caps;
  } catch {
    return null; // storage blocked
  }
}

export function writeCachedCaps(store: KeyValueStore | null, caps: PanelCaps): void {
  try {
    store?.setItem(CAPS_CACHE_KEY, JSON.stringify(caps));
  } catch {
    // Storage full or blocked: the answer still holds for this launch.
  }
}

/** The bits of PalmServiceBridge this uses. */
export interface ServiceBridge {
  onservicecallback: ((message: string) => void) | null;
  call(uri: string, params: string): unknown;
  cancel?(): void;
}

export interface ProbeEnv {
  /** Makes a PalmServiceBridge; null when the runtime has none. */
  createBridge: (() => ServiceBridge) | null;
  store: KeyValueStore | null;
  timeoutMs?: number;
}

/**
 * Ask getConfigs once. `done` gets the answer (also cached when it says
 * anything), or null when there is no bridge, the call failed or it timed
 * out. Never throws.
 */
export function probePanelCaps(env: ProbeEnv, done: (caps: PanelCaps | null) => void): void {
  if (!env.createBridge) {
    done(null);
    return;
  }
  let bridge: ServiceBridge | null = null;
  let settled = false;
  // The timer also keeps the bridge reachable until the reply: a bridge
  // collected mid-call never calls back.
  const timer = setTimeout(() => finish(null), env.timeoutMs ?? PROBE_TIMEOUT_MS);
  function finish(caps: PanelCaps | null) {
    if (settled) return;
    settled = true;
    clearTimeout(timer);
    try {
      bridge?.cancel?.();
    } catch {
      /* already closed */
    }
    bridge = null;
    if (caps && (caps.hdr !== null || caps.uhd !== null)) writeCachedCaps(env.store, caps);
    done(caps);
  }
  try {
    bridge = env.createBridge();
    bridge.onservicecallback = (message) => finish(parseConfigsResponse(message));
    bridge.call(CONFIG_URI, JSON.stringify({ configNames: CONFIG_NAMES }));
  } catch {
    finish(null);
  }
}

// ── Runtime (the live TV) ─────────────────────────────────────────────────

interface PalmGlobals {
  PalmSystem?: { deviceInfo?: unknown };
  webOSSystem?: { deviceInfo?: unknown };
  PalmServiceBridge?: new () => ServiceBridge;
  localStorage?: KeyValueStore;
}

function runtimeStore(): KeyValueStore | null {
  try {
    return (globalThis as PalmGlobals).localStorage ?? null;
  } catch {
    return null; // access throws when storage is blocked
  }
}

let deviceInfoSize: PanelSize | null | undefined;
let lunaCaps: PanelCaps | null | undefined;
let probeStarted = false;

function liveDeviceInfoSize(): PanelSize | null {
  if (deviceInfoSize === undefined) {
    let raw: unknown;
    try {
      const g = globalThis as PalmGlobals;
      raw = g.webOSSystem?.deviceInfo ?? g.PalmSystem?.deviceInfo;
    } catch {
      raw = undefined; // not a webOS runtime
    }
    deviceInfoSize = panelSizeFromDeviceInfo(raw);
  }
  return deviceInfoSize;
}

function liveCaps(): PanelCaps | null {
  if (lunaCaps === undefined) lunaCaps = readCachedCaps(runtimeStore());
  return lunaCaps;
}

/** Start the getConfigs probe unless an earlier launch cached its answer.
 *  Idempotent; the capability header calls it, so the first request kicks
 *  it off and the ones after (the transcode start among them) see it. */
export function startPanelProbe(): void {
  if (probeStarted) return;
  probeStarted = true;
  if (liveCaps()) return;
  let Bridge: (new () => ServiceBridge) | null = null;
  try {
    // Not typeof === 'function': an injected host constructor isn't
    // guaranteed to report as one. `new` is guarded in probePanelCaps.
    const B = (globalThis as PalmGlobals).PalmServiceBridge;
    if (B) Bridge = B;
  } catch {
    Bridge = null;
  }
  const Ctor = Bridge;
  probePanelCaps({ createBridge: Ctor ? () => new Ctor() : null, store: runtimeStore() }, (caps) => {
    if (caps) lunaCaps = caps;
  });
}

/** The panel size the capability header claims (see resolvePanelSize). */
export function readPanelSize(): PanelSize {
  return resolvePanelSize(liveDeviceInfoSize(), liveCaps()?.uhd ?? null);
}

/** Luna's HDR answer: true / false, null while unknown. */
export function panelHdr(): boolean | null {
  return liveCaps()?.hdr ?? null;
}
