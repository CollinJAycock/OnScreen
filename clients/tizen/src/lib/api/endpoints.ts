import { api } from './client';
import type { TokenPair } from './client';
import type {
  Channel,
  ChildItem,
  CollectionItem,
  DiscoverItem,
  FavoriteItem,
  HistoryItem,
  HubData,
  IssueKind,
  ItemDetail,
  LastFMLinkStart,
  Library,
  ManagedProfile,
  Marker,
  MediaCollection,
  MediaIssue,
  MediaItem,
  MediaRequest,
  NowNext,
  OnlineSubtitle,
  PairCodeResponse,
  PlaybackRate,
  Recording,
  ScrobbleLinkResult,
  ScrobbleStatus,
  SearchResult,
  TraktLinkStart,
  TranscodeSession,
  UpNext,
  WatchFilter
} from './types';
import { watchQuery } from '$lib/watchState';

// Response shape for list endpoints that wrap data in { data, meta }.
// The client unwraps `data` already, so these pull the array directly.

export const hub = {
  get: () => api.get<HubData>('/api/v1/hub')
};

export const libraries = {
  list: () => api.get<Library[]>('/api/v1/libraries'),
  /** `watch` narrows by the caller's watch state (v2.5; older servers
   *  ignore it — the library page only offers the filter to newer ones). */
  listItems: (libraryID: string, sort = 'title', dir: 'asc' | 'desc' = 'asc', watch: WatchFilter | '' = '') =>
    api.get<MediaItem[]>(
      `/api/v1/libraries/${libraryID}/items?sort=${sort}&sort_dir=${dir}&limit=200${watchQuery(watch)}`
    ),
  /** "Surprise me" (v2.5): one random item matching the filter. 404 when
   *  nothing matches. */
  random: (libraryID: string, watch: WatchFilter | '' = '') =>
    api.get<{ id: string; type: string }>(
      `/api/v1/libraries/${libraryID}/random${watch ? `?watch=${watch}` : ''}`
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
      // Never replay the Bearer across a redirect (see client.ts raw()).
      redirect: 'manual',
    });
    if (!resp.ok) return null;
    return await resp.text();
  },
  /** Build a fully-qualified URL to a trickplay sprite. Sprites
   *  are auth-via-query-token so the browser can `<img>`-load
   *  them without an Authorization header. */
  trickplaySpriteUrl: (id: string, spritePath: string): string => {
    const origin = api.getOrigin();
    const tok = api.getAssetToken();
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
  removeFavorite: (id: string) => api.del<void>(`/api/v1/items/${id}/favorite`),

  // ── Watch state (v2.5) ──
  // Played / unplayed without playing. On a show or season the server
  // applies it to every episode underneath the caller can see.
  markWatched: (id: string) => api.post<void>(`/api/v1/items/${id}/watched`, {}),
  markUnwatched: (id: string) => api.del<void>(`/api/v1/items/${id}/watched`),
  /** Hide an item from Continue Watching until it's played again. */
  dismissContinueWatching: (id: string) =>
    api.post<void>(`/api/v1/items/${id}/dismiss-continue-watching`, {}),
  /** Show or season: which episode Play should start. */
  upNext: (id: string) => api.get<UpNext>(`/api/v1/items/${id}/up-next`),

  // ── Audiobook listening speed (v2.5) ──
  // {id} is the book or one of its chapters (it resolves to the book).
  // Non-audiobooks are 422; a server without the routes is 404.
  playbackRate: (id: string) => api.get<PlaybackRate>(`/api/v1/items/${id}/playback-rate`),
  setPlaybackRate: (id: string, rate: number) =>
    api.put<void>(`/api/v1/items/${id}/playback-rate`, { rate })
};

// ── Report a problem (v2.5) ────────────────────────────────────────────────

export const issues = {
  /** The caller's own reports on an item, newest first. */
  listMine: (itemID: string) => api.get<MediaIssue[]>(`/api/v1/items/${itemID}/issues`),
  /** 409 ALREADY_REPORTED for a second open report of the same kind;
   *  429 TOO_MANY_OPEN_ISSUES past the per-user cap. */
  create: (itemID: string, body: { kind: IssueKind; note?: string; file_id?: string }) =>
    api.post<MediaIssue>(`/api/v1/items/${itemID}/issues`, body)
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
  // Server-authoritative play decision (capability profiles). Returns
  // "directPlay" | "directStream" | "transcode". The capability profile rides
  // the X-Client-Capabilities header (client.ts). See docs/capability-profiles.md.
  decide: (itemId: string, fileId?: string) =>
    api.post<{ decision: string; file_id: string }>(
      `/api/v1/items/${itemId}/playback-decision`,
      fileId ? { file_id: fileId } : {}
    ),
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
      },
      // The device token is the pairing secret; never replay it across a
      // redirect (a non-200/202/410 falls through to the throw below).
      redirect: 'manual'
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

// ── Discover (TMDB) + Requests ─────────────────────────────────────────────

export const discover = {
  search: (query: string, limit = 12) =>
    api.get<DiscoverItem[]>(
      `/api/v1/discover/search?q=${encodeURIComponent(query)}&limit=${limit}`,
    ),
  createRequest: (type: 'movie' | 'show', tmdbID: number) =>
    api.post<MediaRequest>('/api/v1/requests', { type, tmdb_id: tmdbID }),
};

// ── Online subtitle search (OpenSubtitles via server) ──────────────────────

export const onlineSubtitles = {
  search: (itemID: string, lang?: string, query?: string) => {
    const params = new URLSearchParams();
    if (lang) params.set('lang', lang);
    if (query) params.set('query', query);
    const qs = params.toString();
    return api.get<OnlineSubtitle[]>(
      `/api/v1/items/${itemID}/subtitles/search${qs ? `?${qs}` : ''}`,
    );
  },
  /** Download a search result onto the named file. Server fetches
   *  the .srt from OpenSubtitles, persists it next to the media
   *  file, and emits a new external_subtitle row that the next
   *  item-fetch surfaces in subtitle_streams. */
  download: (itemID: string, fileID: string, candidate: OnlineSubtitle) =>
    api.post<void>(`/api/v1/items/${itemID}/subtitles/download`, {
      file_id: fileID,
      provider_file_id: candidate.provider_file_id,
      language: candidate.language,
      title: candidate.file_name,
      hearing_impaired: candidate.hearing_impaired ?? false,
      rating: candidate.rating ?? 0,
      download_count: candidate.download_count ?? 0,
    }),
};

// ── Live TV / DVR ──────────────────────────────────────────────────────────

export const livetv = {
  /** Enabled-only by default; the disabled-channel curation lives in
   *  the web settings UI and the TV client wants to match what the
   *  user expects to see. */
  channels: () => api.get<Channel[]>('/api/v1/tv/channels?enabled_only=true'),
  /** Up to two rows per channel (current + next). Channels missing
   *  from the response have no EPG data — caller renders "no guide
   *  data" rather than dropping the row. */
  nowNext: () => api.get<NowNext[]>('/api/v1/tv/channels/now-next'),
  /** Recordings filtered by status. status=undefined = all. */
  recordings: (status?: string) => {
    const qs = status ? `?status=${encodeURIComponent(status)}&limit=100` : '?limit=100';
    return api.get<Recording[]>(`/api/v1/tv/recordings${qs}`);
  },
};

// ── System (v2.2) ──────────────────────────────────────────────────────────
// Public capabilities feed — no auth needed. TV uses it to gate UI for
// optional features (live_tv, dvr, requests, lyrics) that the operator
// may not have configured. Server promises forward-compat: new fields
// land in v2.x without breaking older clients.

export interface CapabilitiesFeatures {
  transcode: boolean;
  trickplay: boolean;
  subtitles_external: boolean;
  subtitles_ocr: boolean;
  oidc: boolean;
  ldap: boolean;
  device_pairing: boolean;
  plugins: boolean;
  backup: boolean;
  people_credits: boolean;
  photos: boolean;
  music: boolean;
  webhooks: boolean;
  notifications: boolean;
  // v2.2 additions — all default false on older servers.
  requests: boolean;
  live_tv: boolean;
  dvr: boolean;
  lyrics: boolean;
  intro_markers: boolean;
  chapters: boolean;
  web_downloads: boolean;
}

export interface CapabilitiesResponse {
  features: CapabilitiesFeatures;
}

export const system = {
  capabilities: () => api.get<CapabilitiesResponse>('/api/v1/system/capabilities'),
};

// ── Watch-status mirror (v2.2) ────────────────────────────────────────────
// Plan to Watch / Watching / Completed / On Hold / Dropped — generic
// across every type. Distinct from playback progress.

export type WatchStatusValue =
  | 'plan_to_watch'
  | 'watching'
  | 'completed'
  | 'on_hold'
  | 'dropped';

export interface WatchStatus {
  status: WatchStatusValue;
  updated_at: string;
}

export const watchStatus = {
  get: (itemID: string) => api.get<WatchStatus | null>(`/api/v1/items/${itemID}/watch-status`),
  set: (itemID: string, status: WatchStatusValue) =>
    api.put<void>(`/api/v1/items/${itemID}/watch-status`, { status }),
  clear: (itemID: string) => api.del<void>(`/api/v1/items/${itemID}/watch-status`),
};

// ── Cross-device playback transfer (v2.2) ─────────────────────────────────
// TVs are typical receivers — phone-to-TV is the canonical use case. The
// transfer event arrives on the user's SSE notifications stream as
// `{ type: 'playback.transfer', data: { item_id, position_ms,
// target_client_name } }`; the TV compares target_client_name against
// its own registered client_name and loads + plays the item when they
// match. The POST below lets the TV initiate the other direction
// ("send this back to my phone") if the UI ever needs it.

export interface PlaybackTransferRequest {
  item_id: string;
  position_ms?: number;
  target_client_name: string;
}

export const playback = {
  transfer: (req: PlaybackTransferRequest) =>
    api.post<void>('/api/v1/playback/transfer', req),
};

// ── User preferences ─────────────────────────────────────────────
// Server-side per-user defaults: audio + subtitle languages, forced-
// subtitles-only, max-quality caps. Settings page surfaces the audio
// + subtitle language fields; the rest are admin-only or future
// surface.
export interface UserPreferences {
  preferred_audio_lang?: string | null;
  preferred_subtitle_lang?: string | null;
  max_content_rating?: string | null;
  max_video_bitrate_kbps?: number | null;
  max_audio_bitrate_kbps?: number | null;
  max_video_height?: number | null;
  preferred_video_codec?: string | null;
  forced_subtitles_only: boolean;
  episode_use_show_poster: boolean;
}

export interface PreferencesUpdate {
  preferred_audio_lang?: string | null;
  preferred_subtitle_lang?: string | null;
  forced_subtitles_only?: boolean;
}

// A user's parental watch policy plus today's usage and whether playback
// is allowed right now. All three limit fields are null when unrestricted.
// remaining_minutes is present only when a daily cap is set; reason is set
// only when allowed is false ('daily_limit_reached' | 'outside_allowed_hours').
export interface WatchLimitInfo {
  daily_limit_minutes: number | null;
  allowed_start_minute: number | null;
  allowed_end_minute: number | null;
  used_minutes_today: number;
  remaining_minutes?: number;
  allowed: boolean;
  reason?: string;
}

export const users = {
  preferences: () => api.get<UserPreferences>('/api/v1/users/me/preferences'),
  setPreferences: (body: PreferencesUpdate) =>
    api.put<UserPreferences>('/api/v1/users/me/preferences', body),
  /** The caller's own watch policy + today's usage + whether playback is
   *  allowed right now. The player uses it to block a restricted user before
   *  a stream starts (the transcode-start / progress 403 catches the rest). */
  watchLimit: () => api.get<WatchLimitInfo>('/api/v1/users/me/watch-limit'),
};

// ── Scrobbling ───────────────────────────────────────────────────
// Per-user external scrobble links. The ListenBrainz token is write-only —
// the server returns only whether one is linked and whether submission is
// enabled; an empty token unlinks. Last.fm and Trakt (v2.5) link through an
// approval on the service's own site, then the TV polls `complete` with the
// sealed pending handle. The scrobbles themselves are sent server-side from
// watch events, so every client's plays count once a service is linked.
// Mirrors the web client's scrobbleApi.
export const scrobble = {
  status: () => api.get<ScrobbleStatus>('/api/v1/users/me/scrobble'),
  /** Link or update the ListenBrainz token. An empty token unlinks. */
  setListenBrainz: (token: string, enabled: boolean) =>
    api.put<void>('/api/v1/users/me/scrobble/listenbrainz', { token, enabled }),
  startLastFM: () => api.post<LastFMLinkStart>('/api/v1/users/me/scrobble/lastfm/link', {}),
  completeLastFM: (pending: string) =>
    api.post<ScrobbleLinkResult>('/api/v1/users/me/scrobble/lastfm/link/complete', { pending }),
  unlinkLastFM: () => api.del<void>('/api/v1/users/me/scrobble/lastfm'),
  startTrakt: () => api.post<TraktLinkStart>('/api/v1/users/me/scrobble/trakt/link', {}),
  completeTrakt: (pending: string) =>
    api.post<ScrobbleLinkResult>('/api/v1/users/me/scrobble/trakt/link/complete', { pending }),
  unlinkTrakt: () => api.del<void>('/api/v1/users/me/scrobble/trakt'),
};
