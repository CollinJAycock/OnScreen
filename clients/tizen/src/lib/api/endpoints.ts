import { api } from './client';
import type { TokenPair } from './client';
import type {
  ChildItem,
  CollectionItem,
  FavoriteItem,
  HistoryItem,
  HubData,
  ItemDetail,
  Library,
  ManagedProfile,
  Marker,
  MediaCollection,
  MediaItem,
  PairCodeResponse,
  SearchResult,
  TranscodeSession
} from './types';

// Response shape for list endpoints that wrap data in { data, meta }.
// The client unwraps `data` already, so these pull the array directly.

export const hub = {
  get: () => api.get<HubData>('/api/v1/hub')
};

export const libraries = {
  list: () => api.get<Library[]>('/api/v1/libraries'),
  listItems: (libraryID: string, sort = 'title', dir: 'asc' | 'desc' = 'asc') =>
    api.get<MediaItem[]>(
      `/api/v1/libraries/${libraryID}/items?sort=${sort}&sort_dir=${dir}&limit=200`
    )
};

export const items = {
  get: (id: string) => api.get<ItemDetail>(`/api/v1/items/${id}`),
  children: (id: string) => api.get<ChildItem[]>(`/api/v1/items/${id}/children`),
  // Intro / credits marker windows for an episode. Empty array
  // for movies + non-episode types — the server returns [] rather
  // than 404 so callers can fire-and-forget without branching.
  markers: (id: string) => api.get<Marker[]>(`/api/v1/items/${id}/markers`),
  // Trickplay WebVTT index. Returns the raw text so the caller
  // can run it through the parser; keeping unwrapping client-side
  // means the same parser works against test fixtures, the
  // browser, and (in principle) any other transport. 404 / 204
  // are normal — items without sprite sheets just don't surface
  // a scrub preview, which is non-fatal.
  trickplayVtt: async (id: string): Promise<string | null> => {
    const origin = api.getOrigin();
    if (!origin) return null;
    const tok = api.getToken();
    if (!tok) return null;
    const resp = await fetch(`${origin}/api/v1/items/${id}/trickplay/index.vtt`, {
      headers: { Authorization: `Bearer ${tok}` },
    });
    if (!resp.ok) return null;
    return await resp.text();
  },
  /** Build a fully-qualified URL to a trickplay sprite. Sprites
   *  are auth-via-query-token so the browser can `<img>`-load
   *  them without an Authorization header. */
  trickplaySpriteUrl: (id: string, spritePath: string): string => {
    const origin = api.getOrigin();
    const tok = api.getToken();
    if (!origin || !tok) return '';
    // Cues sometimes carry a relative path (`sprite_0.jpg`),
    // sometimes a server-rooted one. Detect and route both.
    const base = spritePath.startsWith('/')
      ? `${origin}${spritePath}`
      : `${origin}/api/v1/items/${id}/trickplay/${spritePath}`;
    const sep = base.includes('?') ? '&' : '?';
    return `${base}${sep}token=${encodeURIComponent(tok)}`;
  },
  progress: (
    id: string,
    viewOffsetMs: number,
    durationMs: number,
    state: 'playing' | 'paused' | 'stopped'
  ) =>
    api.put<void>(`/api/v1/items/${id}/progress`, {
      view_offset_ms: viewOffsetMs,
      duration_ms: durationMs,
      state
    }),
  addFavorite: (id: string) => api.post<void>(`/api/v1/items/${id}/favorite`, {}),
  removeFavorite: (id: string) => api.del<void>(`/api/v1/items/${id}/favorite`)
};

export const search = {
  query: (q: string, limit = 30) =>
    api.get<SearchResult[]>(`/api/v1/search?q=${encodeURIComponent(q)}&limit=${limit}`)
};

export const profiles = {
  list: () => api.get<ManagedProfile[]>('/api/v1/profiles')
};

// Auth-provider discovery. The TV pair flow works against any auth
// backend, but a laptop user opening /pair on the server is more
// likely to find the right "Sign in with X" button if we hint them
// at it on the TV. Returns the names of OIDC + SAML providers that
// are configured on this server. LDAP is intentionally omitted —
// the LDAP path uses the same username/password form as local auth,
// so naming it as a separate "provider" is just noise on the TV.
export interface EnabledProvider {
  kind: 'oidc' | 'saml';
  display_name: string;
}
export const auth = {
  providers: async (): Promise<EnabledProvider[]> => {
    const out: EnabledProvider[] = [];
    // The /enabled endpoints are unauthenticated and cheap; fire in
    // parallel and tolerate failures (a misconfigured server might
    // 500 on the OIDC probe but still have SAML working). Empty
    // result on either error path → the Pair screen just doesn't
    // render the hint, which matches the pre-feature behaviour.
    type Probe = { enabled: boolean; display_name: string };
    const safe = async (path: string): Promise<Probe | null> => {
      try {
        return await api.get<Probe>(path);
      } catch {
        return null;
      }
    };
    const [oidc, saml] = await Promise.all([
      safe('/api/v1/auth/oidc/enabled'),
      safe('/api/v1/auth/saml/enabled'),
    ]);
    if (oidc?.enabled) out.push({ kind: 'oidc', display_name: oidc.display_name || 'SSO' });
    if (saml?.enabled) out.push({ kind: 'saml', display_name: saml.display_name || 'SAML' });
    return out;
  },
};

export interface TranscodeStartOpts {
  itemId: string;
  height: number;
  positionMs: number;
  fileId?: string;
  videoCopy?: boolean;
  audioStreamIndex?: number;
  supportsHEVC?: boolean;
}

export const transcode = {
  start: (opts: TranscodeStartOpts) =>
    api.post<TranscodeSession>(`/api/v1/items/${opts.itemId}/transcode`, {
      file_id: opts.fileId ?? null,
      height: opts.height,
      position_ms: opts.positionMs,
      video_copy: opts.videoCopy ?? false,
      audio_stream_index: opts.audioStreamIndex ?? null,
      supports_hevc: opts.supportsHEVC ?? false
    }),
  stop: (sessionId: string, token: string) =>
    api.del<void>(
      `/api/v1/transcode/sessions/${sessionId}?token=${encodeURIComponent(token)}`
    )
};

// ── Device pairing ──────────────────────────────────────────────────────────
//
// Same flow as the Android client's PairingFragment: TV requests a code,
// shows it to the user, polls until the user signs in on a phone /
// laptop. The poll endpoint takes the device_token as a Bearer because
// the server treats it like a one-shot identity for this pairing.

export const pair = {
  start: () => api.post<PairCodeResponse>('/api/v1/auth/pair/code', {}),
  // Custom poll: returns 200 + token pair, 202 (still pending), 410 (expired).
  // Caller distinguishes via status — we throw a sentinel for non-200.
  poll: async (deviceToken: string): Promise<{ status: 'done' | 'pending' | 'expired'; pair?: TokenPair }> => {
    const origin = api.getOrigin();
    if (!origin) throw new Error('API origin not configured');
    const resp = await fetch(`${origin}/api/v1/auth/pair/poll`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${deviceToken}`,
        'Content-Type': 'application/json'
      }
    });
    if (resp.status === 200) {
      const j = await resp.json();
      return { status: 'done', pair: (j?.data ?? j) as TokenPair };
    }
    if (resp.status === 202) return { status: 'pending' };
    if (resp.status === 410) return { status: 'expired' };
    throw new Error(`pair poll: HTTP ${resp.status}`);
  }
};

// ── Collections ─────────────────────────────────────────────────────────────

export const collections = {
  list: () => api.get<MediaCollection[]>('/api/v1/collections'),
  get: (id: string) => api.get<MediaCollection>(`/api/v1/collections/${id}`),
  items: (id: string, limit = 200) =>
    api.get<CollectionItem[]>(`/api/v1/collections/${id}/items?limit=${limit}`)
};

// ── Favorites + History ─────────────────────────────────────────────────────

export const favorites = {
  list: (limit = 50) => api.get<FavoriteItem[]>(`/api/v1/favorites?limit=${limit}`)
};

export const history = {
  list: (limit = 50) => api.get<HistoryItem[]>(`/api/v1/history?limit=${limit}`)
};
