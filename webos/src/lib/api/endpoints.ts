import { api } from './client';
import type {
  ChildItem,
  HubData,
  ItemDetail,
  Library,
  ManagedProfile,
  MediaItem,
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
