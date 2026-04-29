import { api } from './client';
import type { TokenPair } from './client';
import type {
  ChildItem,
  CollectionItem,
  HubData,
  ItemDetail,
  Library,
  ManagedProfile,
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
