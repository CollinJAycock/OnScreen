/**
 * OnScreen API client — wraps fetch with httpOnly cookie auth and
 * standard error handling. Tokens live in httpOnly cookies (set by the
 * server); localStorage holds only non-secret user metadata for UI routing.
 */

const BASE = '/api/v1';

interface ApiResponse<T> {
  data: T;
}

interface ApiListResponse<T> {
  data: T[];
  meta: { total: number; cursor: string };
}

interface ApiError {
  error: {
    code: string;
    message: string;
    request_id: string;
  };
}

export interface UserMeta {
  user_id: string;
  username: string;
  is_admin: boolean;
}

export class ApiClient {
  private refreshPromise: Promise<boolean> | null = null;

  /** Store non-secret user metadata for UI routing. */
  setUser(meta: UserMeta | null) {
    if (meta) {
      localStorage.setItem('onscreen_user', JSON.stringify(meta));
    } else {
      localStorage.removeItem('onscreen_user');
    }
  }

  /** Read stored user metadata, or null if not logged in. */
  getUser(): UserMeta | null {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) return null;
    try { return JSON.parse(raw); } catch { return null; }
  }

  /**
   * Shared 401-retry wrapper.  Calls `doFetch` to get the response, then
   * `parseResponse` to turn it into the caller's desired shape.  On a 401
   * it attempts a single silent token refresh before redirecting to login.
   */
  private async requestWithRetry<T>(
    path: string,
    doFetch: () => Promise<Response>,
    parseResponse: (resp: Response) => Promise<T>,
    retry = true
  ): Promise<T> {
    const resp = await doFetch();

    if (resp.status === 401 && retry) {
      let refreshed: boolean;
      try {
        if (!this.refreshPromise) {
          this.refreshPromise = this.tryRefresh().finally(() => {
            this.refreshPromise = null;
          });
        }
        refreshed = await this.refreshPromise;
      } catch {
        refreshed = false;
      }
      if (refreshed) {
        return this.requestWithRetry(path, doFetch, parseResponse, false);
      }
      this.setUser(null);
      window.location.href = '/login';
      return undefined as T;
    }

    return parseResponse(resp);
  }

  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<T> {
    return this.requestWithRetry(
      path,
      () => fetch(BASE + path, {
        method,
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: body ? JSON.stringify(body) : undefined
      }),
      async (resp) => {
        if (resp.status === 204) return undefined as T;
        const json = (await resp.json()) as ApiResponse<T> | ApiError;
        if (!resp.ok) {
          const err = json as ApiError;
          throw new Error(err.error?.message ?? `HTTP ${resp.status}`);
        }
        return (json as ApiResponse<T>).data;
      }
    );
  }

  async requestList<T>(path: string): Promise<{ items: T[]; total: number }> {
    return this.requestWithRetry(
      path,
      () => fetch(BASE + path, {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin'
      }),
      async (resp) => {
        const json = await resp.json();
        if (!resp.ok) {
          const err = json as ApiError;
          throw new Error(err.error?.message ?? `HTTP ${resp.status}`);
        }
        const envelope = json as ApiListResponse<T>;
        return { items: envelope.data ?? [], total: envelope.meta?.total ?? 0 };
      }
    );
  }

  private async tryRefresh(): Promise<boolean> {
    try {
      // Refresh token is in an httpOnly cookie scoped to /api/v1/auth — sent automatically.
      const resp = await fetch(BASE + '/auth/refresh', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin'
      });
      if (!resp.ok) {
        return false;
      }
      const json = (await resp.json()) as ApiResponse<TokenPair>;
      const pair = json.data;
      // Update stored user metadata (tokens stay in httpOnly cookies).
      this.setUser({ user_id: pair.user_id, username: pair.username, is_admin: pair.is_admin });
      return true;
    } catch {
      return false;
    }
  }

  get = <T>(path: string) => this.request<T>('GET', path);
  post = <T>(path: string, body?: unknown) => this.request<T>('POST', path, body);
  put = <T>(path: string, body?: unknown) => this.request<T>('PUT', path, body);
  patch = <T>(path: string, body?: unknown) => this.request<T>('PATCH', path, body);
  del = (path: string, body?: unknown) => this.request<void>('DELETE', path, body);
  delete = (path: string, body?: unknown) => this.request<void>('DELETE', path, body);
}

export const api = new ApiClient();

// ── Auth ──────────────────────────────────────────────────────────────────────

export interface TokenPair {
  access_token: string;
  refresh_token: string;
  expires_at: string;
  user_id: string;
  username: string;
  is_admin: boolean;
}

export const authApi = {
  setupStatus: () => api.get<{ setup_required: boolean }>('/setup/status'),
  login: (username: string, password: string) =>
    api.post<TokenPair>('/auth/login', { username, password }),
  register: (username: string, password: string, email?: string) =>
    api.post<{ id: string; username: string }>('/auth/register', { username, password, email }),
  logout: () => api.post('/auth/logout'),
  googleEnabled: () => api.get<{ enabled: boolean; client_id?: string }>('/auth/google/enabled'),
  githubEnabled: () => api.get<{ enabled: boolean }>('/auth/github/enabled'),
  discordEnabled: () => api.get<{ enabled: boolean }>('/auth/discord/enabled'),
  forgotPasswordEnabled: () => api.get<{ enabled: boolean }>('/auth/forgot-password/enabled'),
  forgotPassword: (email: string) => api.post<{ message: string }>('/auth/forgot-password', { email }),
  resetPassword: (token: string, password: string) => api.post<{ message: string }>('/auth/reset-password', { token, password })
};

// ── Email (admin) ─────────────────────────────────────────────────────────────

export const emailApi = {
  enabled: () => api.get<{ enabled: boolean }>('/email/enabled'),
  sendTest: (to: string) => api.post<{ message: string }>('/email/test', { to })
};

// ── Invites (admin) ───────────────────────────────────────────────────────────

export const inviteApi = {
  create: (email?: string) => api.post<{ invite_url: string; id: string }>('/invites', { email }),
  list: () => api.get<Array<{ id: string; email: string | null; expires_at: string; used_at: string | null; created_at: string }>>('/invites'),
  del: (id: string) => api.del(`/invites/${id}`),
  accept: (token: string, username: string, password: string) => api.post<{ message: string }>('/invites/accept', { token, username, password })
};

// ── Users (admin) ─────────────────────────────────────────────────────────────

export interface User {
  id: string;
  username: string;
  is_admin: boolean;
  created_at: string;
}

export interface SwitchableUser {
  id: string;
  username: string;
  is_admin: boolean;
  has_pin: boolean;
}

export const userApi = {
  list: () => api.requestList<User>('/users'),
  create: (username: string, password: string, email?: string) =>
    api.post<{ id: string; username: string }>('/auth/register', { username, password, email }),
  del: (id: string) => api.del(`/users/${id}`),
  resetPassword: (id: string, password: string) =>
    api.put<void>(`/users/${id}/password`, { password }),
  setAdmin: (id: string, isAdmin: boolean) =>
    api.patch<void>(`/users/${id}`, { is_admin: isAdmin }),
  setPin: (pin: string, password: string) =>
    api.put<void>('/users/me/pin', { pin, password }),
  clearPin: (password: string) =>
    api.del('/users/me/pin', { password }),
  listSwitchable: () =>
    api.get<SwitchableUser[]>('/users/switchable'),
  pinSwitch: (userId: string, pin: string) =>
    api.post<TokenPair>('/auth/pin-switch', { user_id: userId, pin }),
  getPreferences: () =>
    api.get<UserPreferences>('/users/me/preferences'),
  setPreferences: (prefs: UserPreferences) =>
    api.put<void>('/users/me/preferences', prefs),
  setContentRating: (userId: string, maxContentRating: string | null) =>
    api.put<void>(`/users/${userId}/content-rating`, { max_content_rating: maxContentRating }),
  getLibraries: (userId: string) =>
    api.get<UserLibraryAccess[]>(`/users/${userId}/libraries`),
  setLibraries: (userId: string, libraryIds: string[]) =>
    api.put<void>(`/users/${userId}/libraries`, { library_ids: libraryIds })
};

export interface UserLibraryAccess {
  library_id: string;
  name: string;
  type: string;
  enabled: boolean;
}

export interface UserPreferences {
  preferred_audio_lang: string | null;
  preferred_subtitle_lang: string | null;
  max_content_rating: string | null;
}

// ── Libraries ─────────────────────────────────────────────────────────────────

export interface Library {
  id: string;
  name: string;
  type: 'movie' | 'show' | 'music' | 'photo';
  scan_paths: string[];
  agent: string;
  language: string;
  scan_interval_minutes?: number;
  created_at: string;
  updated_at: string;
}

export const libraryApi = {
  list: () => api.get<Library[]>('/libraries'),
  get: (id: string) => api.get<Library>(`/libraries/${id}`),
  create: (body: Partial<Library>) => api.post<Library>('/libraries', body),
  update: (id: string, body: Partial<Library>) => api.patch<Library>(`/libraries/${id}`, body),
  del: (id: string) => api.del(`/libraries/${id}`),
  scan: (id: string) => api.post(`/libraries/${id}/scan`)
};

// ── Media Items ───────────────────────────────────────────────────────────────

export interface MediaItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  summary?: string;
  rating?: number;
  duration_ms?: number;
  genres?: string[];
  poster_path?: string;
  created_at: string;
  updated_at: string;
}

export type SortField = 'title' | 'year' | 'rating' | 'created_at';

export interface ListItemsParams {
  sort?: SortField;
  sort_dir?: 'asc' | 'desc';
  genre?: string;
  year_min?: number;
  year_max?: number;
  rating_min?: number;
}

// ── Settings ──────────────────────────────────────────────────────────────────

export interface ServerSettings {
  tmdb_api_key: string;
  tvdb_api_key: string;
  arr_api_key: string;
  arr_webhook_url: string;
  arr_path_mappings?: Record<string, string>;
  transcode_encoders: string;
}

export interface EncoderEntry {
  encoder: string;
  label: string;
}

export interface EncoderInfo {
  detected: EncoderEntry[];
  current: string;
}

export interface WorkerInfo {
  id: string;
  addr: string;
  capabilities: string[];
  max_sessions: number;
  active_sessions: number;
  registered_at: string;
}

export interface WorkerSlotConfig {
  addr: string;
  name: string;
  encoder: string;
  max_sessions?: number;
}

export interface FleetConfig {
  embedded_enabled: boolean;
  embedded_encoder: string;
  workers: WorkerSlotConfig[];
}

export interface FleetWorkerStatus {
  id: string;
  addr: string;
  name: string;
  encoder: string;
  online: boolean;
  active_sessions: number;
  max_sessions: number;
  capabilities: string[];
}

export interface FleetStatus {
  embedded_enabled: boolean;
  embedded_disabled_by_env: boolean;
  embedded_encoder: string;
  embedded_online: boolean;
  embedded_active_sessions: number;
  embedded_max_sessions: number;
  embedded_capabilities: string[];
  workers: FleetWorkerStatus[];
}

export interface TranscodeConfig {
  nvenc_preset: string;
  nvenc_tune: string;
  nvenc_rc: string;
  maxrate_ratio: number;
}

export const settingsApi = {
  get: () => api.get<ServerSettings>('/settings'),
  update: (body: Partial<ServerSettings>) => api.patch<void>('/settings', body),
  getEncoders: () => api.get<EncoderInfo>('/settings/encoders'),
  getWorkers: () => api.get<WorkerInfo[]>('/settings/workers'),
  getFleet: () => api.get<FleetStatus>('/settings/fleet'),
  updateFleet: (body: FleetConfig) => api.put<void>('/settings/fleet', body),
  getTranscodeConfig: () => api.get<TranscodeConfig>('/settings/transcode-config'),
  updateTranscodeConfig: (body: TranscodeConfig) => api.put<void>('/settings/transcode-config', body),
};

// ── Filesystem browser ────────────────────────────────────────────────────────

export interface BrowseResult {
  path: string;
  parent: string;
  dirs: string[];
}

export const fsApi = {
  browse: (path = '/') =>
    api.get<BrowseResult>(`/fs/browse?path=${encodeURIComponent(path)}`)
};

// ── Media Items ───────────────────────────────────────────────────────────────

export const mediaApi = {
  listItems: (libraryId: string, limit = 50, offset = 0, params?: ListItemsParams) => {
    const qs = new URLSearchParams();
    qs.set('limit', String(limit));
    qs.set('offset', String(offset));
    if (params?.sort) qs.set('sort', params.sort);
    if (params?.sort_dir) qs.set('sort_dir', params.sort_dir);
    if (params?.genre) qs.set('genre', params.genre);
    if (params?.year_min != null) qs.set('year_min', String(params.year_min));
    if (params?.year_max != null) qs.set('year_max', String(params.year_max));
    if (params?.rating_min != null) qs.set('rating_min', String(params.rating_min));
    return api.requestList<MediaItem>(`/libraries/${libraryId}/items?${qs.toString()}`);
  },
  genres: (libraryId: string) =>
    api.get<string[]>(`/libraries/${libraryId}/genres`),
  enrichItem: (id: string) =>
    api.post<void>(`/items/${id}/enrich`)
};

// ── Item detail (player) ──────────────────────────────────────────────────────

export interface AudioStream {
  index: number;
  codec: string;
  channels: number;
  language: string;
  title: string;
}

export interface SubtitleStream {
  index: number;
  codec: string;
  language: string;
  title: string;
  forced: boolean;
}

export interface Chapter {
  title: string;
  start_ms: number;
  end_ms: number;
}

export interface ItemFile {
  id: string;
  stream_url: string;
  container?: string;
  video_codec?: string;
  audio_codec?: string;
  resolution_w?: number;
  resolution_h?: number;
  bitrate?: number;
  hdr_type?: string;
  duration_ms?: number;
  faststart: boolean;
  audio_streams: AudioStream[];
  subtitle_streams: SubtitleStream[];
  chapters: Chapter[];
}

export interface ItemDetail {
  id: string;
  library_id: string;
  title: string;
  type: string;
  year?: number;
  summary?: string;
  rating?: number;
  duration_ms?: number;
  poster_path?: string;
  fanart_path?: string;
  content_rating?: string;
  genres: string[];
  parent_id?: string;
  index?: number;
  view_offset_ms: number;
  updated_at: number;
  is_favorite: boolean;
  files: ItemFile[];
}

export interface FavoriteItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  duration_ms?: number;
  favorited_at: string;
}

export interface ChildItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  summary?: string;
  rating?: number;
  duration_ms?: number;
  poster_path?: string;
  thumb_path?: string;
  index?: number;
  created_at: string;
  updated_at: number;
}

export interface MatchCandidate {
  tmdb_id: number;
  title: string;
  year?: number;
  summary?: string;
  poster_url?: string;
  rating?: number;
}

// ── Collections & Playlists ──────────────────────────────────────────────────

export interface Collection {
  id: string;
  name: string;
  description?: string;
  type: 'auto_genre' | 'playlist';
  genre?: string;
  poster_path?: string;
  created_at: string;
}

export interface CollectionItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  rating?: number;
  poster_path?: string;
  duration_ms?: number;
  position?: number;
}

export const collectionApi = {
  list: () => api.get<Collection[]>('/collections'),
  get: (id: string) => api.get<Collection>(`/collections/${id}`),
  create: (name: string, description?: string) =>
    api.post<Collection>('/collections', { name, description }),
  update: (id: string, name: string, description?: string) =>
    api.patch<Collection>(`/collections/${id}`, { name, description }),
  delete: (id: string) => api.delete(`/collections/${id}`),
  items: (id: string, limit = 50, offset = 0) =>
    api.requestList<CollectionItem>(`/collections/${id}/items?limit=${limit}&offset=${offset}`),
  addItem: (collectionId: string, mediaItemId: string) =>
    api.post<void>(`/collections/${collectionId}/items`, { media_item_id: mediaItemId }),
  removeItem: (collectionId: string, itemId: string) =>
    api.delete(`/collections/${collectionId}/items/${itemId}`),
};

export interface ManagedProfile {
  id: string;
  username: string;
  avatar_url?: string;
  has_pin: boolean;
  created_at: string;
  max_content_rating?: string | null;
}

export const profileApi = {
  list: () => api.get<ManagedProfile[]>('/profiles'),
  create: (username: string, avatar_url?: string, pin?: string) =>
    api.post<ManagedProfile>('/profiles', { username, avatar_url, pin }),
  update: (id: string, username: string, avatar_url?: string) =>
    api.patch<ManagedProfile>(`/profiles/${id}`, { username, avatar_url }),
  delete: (id: string) => api.delete(`/profiles/${id}`),
};

export const itemApi = {
  get: (id: string) => api.get<ItemDetail>(`/items/${id}`),
  children: (id: string) =>
    api.requestList<ChildItem>(`/items/${id}/children`),
  progress: (id: string, viewOffsetMs: number, durationMs: number, state: 'playing' | 'paused' | 'stopped') =>
    api.put<void>(`/items/${id}/progress`, {
      view_offset_ms: viewOffsetMs,
      duration_ms: durationMs,
      state
    }),
  searchMatch: (id: string, query: string) =>
    api.get<MatchCandidate[]>(`/items/${id}/match/search?query=${encodeURIComponent(query)}`),
  applyMatch: (id: string, tmdbId: number) =>
    api.post<void>(`/items/${id}/match`, { tmdb_id: tmdbId }),
  addFavorite: (id: string) => api.post<void>(`/items/${id}/favorite`, {}),
  removeFavorite: (id: string) => api.delete(`/items/${id}/favorite`)
};

export const favoritesApi = {
  list: (limit = 100, offset = 0) =>
    api.requestList<FavoriteItem>(`/favorites?limit=${limit}&offset=${offset}`)
};

// ── Analytics ─────────────────────────────────────────────────────────────────

export interface AnalyticsOverview {
  total_items: number;
  total_files: number;
  total_size_bytes: number;
  total_plays: number;
  total_watch_time_ms: number;
}

export interface LibraryAnalytics {
  id: string;
  name: string;
  type: string;
  item_count: number;
  total_size_bytes: number;
  res_4k: number;
  res_1080p: number;
  res_720p: number;
  res_sd: number;
}

export interface CodecCount   { codec: string;     count: number; }
export interface ContainerCount { container: string; count: number; }
export interface DayCount     { date: string;       count: number; }
export interface DayBytes     { date: string;       bytes: number; }

export interface TopPlayedItem {
  id: string;
  title: string;
  year?: number;
  type: string;
  poster_path?: string;
  play_count: number;
}

export interface RecentPlay {
  title: string;
  year?: number;
  type: string;
  occurred_at: string;
  client_name?: string;
  duration_ms?: number;
}

export interface AnalyticsData {
  overview: AnalyticsOverview;
  libraries: LibraryAnalytics[];
  video_codecs: CodecCount[];
  containers: ContainerCount[];
  plays_by_day: DayCount[];
  bandwidth_by_day: DayBytes[];
  top_played: TopPlayedItem[];
  recent_plays: RecentPlay[];
}

export const analyticsApi = {
  get: () => api.get<AnalyticsData>('/analytics')
};

// ── Webhooks ──────────────────────────────────────────────────────────────────

export interface WebhookEndpoint {
  id: string;
  url: string;
  events: string[];
  enabled: boolean;
}

export const webhookApi = {
  list: () => api.requestList<WebhookEndpoint>('/webhooks'),
  create: (body: { url: string; secret?: string; events: string[] }) =>
    api.post<WebhookEndpoint>('/webhooks', body),
  update: (id: string, body: { url?: string; secret?: string; events?: string[]; enabled?: boolean }) =>
    api.patch<WebhookEndpoint>(`/webhooks/${id}`, body),
  del: (id: string) => api.del(`/webhooks/${id}`),
  test: (id: string) => api.post<void>(`/webhooks/${id}/test`)
};

// ── Active Sessions ───────────────────────────────────────────────────────────

export interface ActiveSession {
  id: string;
  decision: string;
  position_ms: number;
  client_name?: string;
  started_at: string;
  title: string;
  year?: number;
  type?: string;
  poster_path?: string;
  duration_ms?: number;
  bitrate_kbps?: number;
}

export const sessionsApi = {
  list: () => api.get<ActiveSession[]>('/sessions')
};

// ── Hub (home page) ──────────────────────────────────────────────────────────

export interface HubItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  fanart_path?: string;
  thumb_path?: string;
  view_offset_ms?: number;
  duration_ms?: number;
  updated_at: number;
}

export interface HubData {
  continue_watching: HubItem[];
  recently_added: HubItem[];
}

export const hubApi = {
  get: () => api.get<HubData>('/hub')
};

// ── Search ────────────────────────────────────────────────────────────────────

export interface SearchResult {
  id: string;
  library_id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  thumb_path?: string;
}

export const searchApi = {
  search: (query: string, libraryId?: string, limit = 20) => {
    const params = new URLSearchParams({ q: query, limit: String(limit) });
    if (libraryId) params.set('library_id', libraryId);
    return api.get<SearchResult[]>(`/search?${params}`);
  }
};

// ── Watch History ─────────────────────────────────────────────────────────

export interface WatchHistoryItem {
  id: string;
  media_id: string;
  title: string;
  type: string;
  year?: number;
  thumb_path?: string;
  client_name?: string;
  duration_ms?: number;
  occurred_at: string;
}

export const historyApi = {
  list: (limit = 50, offset = 0) =>
    api.requestList<WatchHistoryItem>(`/history?limit=${limit}&offset=${offset}`)
};

// ── Transcode ─────────────────────────────────────────────────────────────────

export interface TranscodeSession {
  session_id: string;
  playlist_url: string;
  token: string;
}

export const transcodeApi = {
  start: (itemId: string, height: number, positionMs: number, fileId?: string, videoCopy?: boolean, audioStreamIndex?: number, supportsHEVC?: boolean) =>
    api.post<TranscodeSession>(`/items/${itemId}/transcode`, {
      file_id: fileId,
      height,
      position_ms: positionMs,
      video_copy: videoCopy ?? false,
      audio_stream_index: audioStreamIndex ?? null,
      supports_hevc: supportsHEVC ?? false
    }),
  stop: (sessionId: string, token: string) =>
    api.del(`/transcode/sessions/${sessionId}?token=${encodeURIComponent(token)}`)
};

// ── Audit Log ─────────────────────────────────────────────────────────────────

export interface AuditLogEntry {
  id: string;
  user_id: string | null;
  action: string;
  target: string | null;
  detail: any;
  ip_addr: string | null;
  created_at: string;
}

export const auditApi = {
  list: (limit = 50, offset = 0) =>
    api.get<AuditLogEntry[]>(`/audit?limit=${limit}&offset=${offset}`)
};

// ── Notifications ────────────────────────────────────────────────────────────

export interface Notification {
  id: string;
  type: string;
  title: string;
  body: string;
  item_id?: string;
  read: boolean;
  created_at: number;
}

export const notificationApi = {
  list: (limit = 20, offset = 0) =>
    api.get<Notification[]>(`/notifications?limit=${limit}&offset=${offset}`),
  unreadCount: () =>
    api.get<{ count: number }>('/notifications/unread-count'),
  markRead: (id: string) =>
    api.post<void>(`/notifications/${id}/read`, {}),
  markAllRead: () =>
    api.post<void>('/notifications/read-all', {}),
};
