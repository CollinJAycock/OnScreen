import { api } from './client';
import type { TokenPair } from './client';
import type {
  Channel,
  ChildItem,
  CollectionItem,
  DiscoverItem,
  ExternalSubtitle,
  FavoriteItem,
  GenreCount,
  HistoryItem,
  HubData,
  HubRowPref,
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
import type { PageResult } from '$lib/paging';

// Response shape for list endpoints that wrap data in { data, meta }.
// The client unwraps `data` already, so these pull the array directly.

export const hub = {
  get: () => api.get<HubData>('/api/v1/hub')
};

export const libraries = {
  list: () => api.get<Library[]>('/api/v1/libraries'),
  /** One page of a library. `watch` narrows by the caller's watch state
   *  (v2.5; older servers ignore it — the library page only offers the
   *  filter to newer ones); `genre` by genre name. The server caps `limit`
   *  at 200 and pages with `offset`. */
  listItems: (
    libraryID: string,
    sort = 'title',
    dir: 'asc' | 'desc' = 'asc',
    watch: WatchFilter | '' = '',
    genre = '',
    limit = 100,
    offset = 0
  ) =>
    api.get<MediaItem[]>(
      `/api/v1/libraries/${libraryID}/items?sort=${sort}&sort_dir=${dir}&limit=${limit}&offset=${offset}` +
        `${watchQuery(watch)}${genre ? `&genre=${encodeURIComponent(genre)}` : ''}`
    ),
  /** The library's genres with item counts (the genre filter's choices). */
  genres: (libraryID: string) => api.get<GenreCount[]>(`/api/v1/libraries/${libraryID}/genres`),
  /** "Surprise me" (v2.5): one random item matching the filters. 404 when
   *  nothing matches. */
  random: (libraryID: string, watch: WatchFilter | '' = '', genre = '') => {
    const params: string[] = [];
    if (watch) params.push(`watch=${watch}`);
    if (genre) params.push(`genre=${encodeURIComponent(genre)}`);
    return api.get<{ id: string; type: string }>(
      `/api/v1/libraries/${libraryID}/random${params.length ? `?${params.join('&')}` : ''}`
    );
  }
};

export const items = {
  get: (id: string) => api.get<ItemDetail>(`/api/v1/items/${id}`),
  children: (id: string) => api.get<ChildItem[]>(`/api/v1/items/${id}/children`),
  // Intro / credits marker windows for an episode. Empty array
  // for movies + non-episode types — the server returns [] rather
  // than 404 so callers can fire-and-forget without branching.
  markers: (id: string) => api.get<Marker[]>(`/api/v1/items/${id}/markers`),
  // Trickplay WebVTT index. Returns the raw text so the caller can
  // run it through the parser; keeping unwrapping client-side means
  // the same parser works against test fixtures and the browser.
  // 404 / 204 are normal — items without sprite sheets just don't
  // surface a scrub preview, which is non-fatal.
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
  /** Build a fully-qualified URL to a trickplay sprite. Sprites are
   *  auth-via-query-token so the browser can `<img>`-load them
   *  without an Authorization header. */
  trickplaySpriteUrl: (id: string, spritePath: string): string => {
    const origin = api.getOrigin();
    const tok = api.getAssetToken();
    if (!origin || !tok) return '';
    const base = spritePath.startsWith('/')
      ? `${origin}${spritePath}`
      : `${origin}/api/v1/items/${id}/trickplay/${spritePath}`;
    const sep = base.includes('?') ? '&' : '?';
    return `${base}${sep}token=${encodeURIComponent(tok)}`;
  },
  /** `extra` (all optional, omitted when empty): client_name names this TV
   *  in the device list / Now Playing / an admin stop; decision feeds the
   *  direct-vs-transcode analytics; file_id picks the version for a
   *  server-filled duration. */
  progress: (
    id: string,
    viewOffsetMs: number,
    durationMs: number,
    state: 'playing' | 'paused' | 'stopped',
    extra: { clientName?: string; decision?: string; fileId?: string } = {}
  ) =>
    api.put<void>(`/api/v1/items/${id}/progress`, {
      view_offset_ms: viewOffsetMs,
      duration_ms: durationMs,
      state,
      ...(extra.clientName ? { client_name: extra.clientName } : {}),
      ...(extra.decision ? { decision: extra.decision } : {}),
      ...(extra.fileId ? { file_id: extra.fileId } : {})
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
  /** `libraryId` narrows the search to one library (the server's
   *  `library_id`; Android's "Search in" chip). */
  query: (q: string, limit = 30, libraryId?: string) =>
    api.get<SearchResult[]>(
      `/api/v1/search?q=${encodeURIComponent(q)}&limit=${limit}` +
        (libraryId ? `&library_id=${encodeURIComponent(libraryId)}` : '')
    )
};

export const profiles = {
  list: () => api.get<ManagedProfile[]>('/api/v1/profiles')
};

// Auth-provider discovery for the Pair screen's SSO hint. The TV
// pair flow works against any auth backend (PIN claim is auth-
// agnostic), but a laptop user is more likely to find the right
// "Sign in with X" button on the web pair page if we name the
// configured providers up front. LDAP is intentionally omitted —
// its UX is the same username/password form local auth uses.
export interface EnabledProvider {
  kind: 'oidc' | 'saml';
  display_name: string;
}
export const auth = {
  providers: async (): Promise<EnabledProvider[]> => {
    const out: EnabledProvider[] = [];
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
  /** The 0-based ORDINAL of the track within file.audio_streams: the server
   *  maps `-map 0:a:N` and refuses N past the last audio track (400). Never
   *  AudioStream.index, the absolute ffprobe index that also counts the
   *  video and subtitle streams. Omitted = the server default (0:a:0). */
  audioStreamIndex?: number;
  supportsHEVC?: boolean;
  /** Sent as an explicit bool on every start (Android parity). The
   *  server's field is tri-state and an absent value defers to the
   *  capability header; an explicit one says the same thing (including a
   *  runtime demotion's false) without depending on the header. */
  supportsAV1?: boolean;
}

export const transcode = {
  start: (opts: TranscodeStartOpts) =>
    api.post<TranscodeSession>(`/api/v1/items/${opts.itemId}/transcode`, {
      file_id: opts.fileId ?? null,
      height: opts.height,
      position_ms: opts.positionMs,
      video_copy: opts.videoCopy ?? false,
      audio_stream_index: opts.audioStreamIndex ?? null,
      supports_hevc: opts.supportsHEVC ?? false,
      supports_av1: opts.supportsAV1 ?? false
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
  /** One page of a collection's items (the collection page pages 100 at a
   *  time, like the library grid) with meta.total: the rows the caller can
   *  see. A playlist pages in SQL and only then drops rows from libraries the
   *  caller can't access, so a page can come back short or empty mid-list;
   *  the page pages on the total instead (lib/paging). */
  items: async (id: string, limit = 100, offset = 0): Promise<PageResult<CollectionItem>> => {
    const env = await api.getEnvelope<CollectionItem[]>(
      `/api/v1/collections/${id}/items?limit=${limit}&offset=${offset}`
    );
    const total = env?.meta?.total;
    return {
      items: Array.isArray(env?.data) ? env.data : [],
      total: typeof total === 'number' && Number.isFinite(total) ? total : null
    };
  }
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
  /** 201 with the attached row (it lands in files[].external_subtitles). */
  download: (itemID: string, fileID: string, candidate: OnlineSubtitle) =>
    api.post<ExternalSubtitle | undefined>(`/api/v1/items/${itemID}/subtitles/download`, {
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
  channels: () => api.get<Channel[]>('/api/v1/tv/channels?enabled_only=true'),
  nowNext: () => api.get<NowNext[]>('/api/v1/tv/channels/now-next'),
  recordings: (status?: string) => {
    const qs = status ? `?status=${encodeURIComponent(status)}&limit=100` : '?limit=100';
    return api.get<Recording[]>(`/api/v1/tv/recordings${qs}`);
  },
};

// ── System (v2.2) ──────────────────────────────────────────────────────────
// Public capabilities feed — no auth needed. TV uses it to gate UI for
// optional features (live_tv, dvr, requests, lyrics) that the operator
// may not have configured. Server promises forward-compat on the shape:
// new fields land in v2.x without breaking older clients.

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
  // server / codecs / limits / discovery exist on the wire but the TV
  // doesn't currently consume them — picking them up is additive.
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
  /** Saved home row order + visibility (set on the web home page); absent
   *  until the user customizes it. The hub applies it via lib/hubLayout. */
  hub_layout?: HubRowPref[] | null;
}

// PUT /users/me/preferences takes the two languages (a null clears one) and
// episode_use_show_poster. It ignores forced_subtitles_only: that one is
// part of the quality profile below.
export interface PreferencesUpdate {
  preferred_audio_lang?: string | null;
  preferred_subtitle_lang?: string | null;
}

// PUT /users/me/quality-profile replaces the whole profile: every field is
// written, a null clears that cap. Changing one field means echoing the
// others from a fresh GET /users/me/preferences (lib/settingsPrefs builds it).
export interface QualityProfileUpdate {
  max_video_bitrate_kbps: number | null;
  max_audio_bitrate_kbps: number | null;
  max_video_height: number | null;
  preferred_video_codec: string | null;
  forced_subtitles_only: boolean;
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
  /** 204, no body: re-read preferences() for the stored values. */
  setPreferences: (body: PreferencesUpdate) =>
    api.put<void>('/api/v1/users/me/preferences', body),
  /** 204, no body. Full-object semantics, see QualityProfileUpdate. */
  setQualityProfile: (body: QualityProfileUpdate) =>
    api.put<void>('/api/v1/users/me/quality-profile', body),
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
