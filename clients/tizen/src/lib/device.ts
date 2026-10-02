// This TV's name in the server's eyes: the `client_name` every progress
// heartbeat carries. The server's device list (GET /playback/devices, the
// "play on…" picker of the user's other apps), Now Playing and History show
// it, and an admin's "stop this stream" names it (playback.stop's
// client_name), so the player can tell a stop aimed at this TV from one aimed
// at another of the user's devices. Mirrors the Android TV client's
// ClientName: "<platform> — <model> #<install tag>".
//
// The model comes from Samsung's ProductInfo API (webapis.productinfo, the
// productinfo privilege in config.xml): getRealModel(), the model as sold
// ("QN75Q80BAFXZA"), else getModel(). The tag is 4 random hex digits kept in
// localStorage: two TVs of the same model in one household (a common setup)
// would otherwise share a name, so a transfer would land on both and the
// picker would list only one. The DUID would be stable across reinstalls but
// is a device identifier the app has no other use for.

const PLATFORM = 'Samsung TV';
const TAG_KEY = 'onscreen.client_tag';
/** The server keeps 64 characters; staying under it keeps the tag (the part
 *  that tells two TVs apart) from being cut off. Same cap as Android. */
export const CLIENT_NAME_MAX = 60;

/** What the name reads of webapis.productinfo. */
export interface ProductInfo {
  getRealModel?(): unknown;
  getModel?(): unknown;
}

/** The model from ProductInfo: getRealModel(), else getModel(); '' when
 *  neither answers with text (outside a Tizen webview, or a refused call). */
export function modelNameFrom(info: ProductInfo | null | undefined): string {
  for (const read of [info?.getRealModel, info?.getModel]) {
    if (typeof read !== 'function') continue;
    try {
      const m = read.call(info);
      if (typeof m === 'string' && m.trim()) return m.trim();
    } catch {
      // The next one, then none.
    }
  }
  return '';
}

/** "Samsung TV — <model> #<tag>", capped at CLIENT_NAME_MAX with the tag
 *  kept whole (the model gives way). Without a model: "Samsung TV #<tag>". */
export function formatClientName(model: string, tag: string): string {
  const suffix = tag ? ` #${tag}` : '';
  const base = model ? `${PLATFORM} — ${model}` : PLATFORM;
  return base.slice(0, Math.max(0, CLIENT_NAME_MAX - suffix.length)).trimEnd() + suffix;
}

/** Four lowercase hex digits from `random` (Math.random by default). */
export function randomTag(random: () => number = Math.random): string {
  const n = Math.floor(random() * 0x10000) & 0xffff;
  return n.toString(16).padStart(4, '0');
}

interface TagStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** This install's tag: the stored one, else a new one (stored when storage
 *  takes it; a TV that refuses keeps a per-launch tag, still unique). */
export function installTag(store: TagStore | null, random?: () => number): string {
  try {
    const saved = store?.getItem(TAG_KEY);
    if (saved && /^[0-9a-f]{4}$/.test(saved)) return saved;
  } catch { /* storage unavailable */ }
  const tag = randomTag(random);
  try {
    store?.setItem(TAG_KEY, tag);
  } catch { /* storage full or blocked */ }
  return tag;
}

export interface ClientNameEnv {
  /** webapis.productinfo. */
  productInfo?: ProductInfo | null;
  store?: TagStore | null;
  random?: () => number;
}

/** Build the name from an explicit environment (tests). */
export function computeClientName(env: ClientNameEnv): string {
  return formatClientName(modelNameFrom(env.productInfo), installTag(env.store ?? null, env.random));
}

let cached: string | null = null;

/** This TV's client name, computed once per launch from the live runtime. */
export function clientName(): string {
  if (cached !== null) return cached;
  const g = globalThis as {
    webapis?: { productinfo?: ProductInfo };
    localStorage?: TagStore;
  };
  let productInfo: ProductInfo | undefined;
  try {
    productInfo = g.webapis?.productinfo;
  } catch { /* not a Tizen runtime (desktop dev) */ }
  let store: TagStore | null = null;
  try {
    store = g.localStorage ?? null;
  } catch { /* storage access throws when blocked */ }
  cached = computeClientName({ productInfo, store });
  return cached;
}
