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
  /** Episode tiles (Next Up, recently-added episodes): the show they belong to. */
  show_title?: string;
  show_id?: string;
  /** Next Up tiles: position within the show (0 = unknown). */
  season_number?: number;
  episode_number?: number;
}

export interface HubData {
  continue_watching: HubItem[];
  // Pre-split arrays — newer servers populate these so the UI can
  // render TV / Movies / Other rows. Older servers omit them; the
  // hub page falls back to filtering continue_watching itself.
  continue_watching_tv?: HubItem[];
  continue_watching_movies?: HubItem[];
  continue_watching_other?: HubItem[];
  // v2.5: the next unwatched episode of each show in progress, and the
  // items marked Plan to Watch. Always present (maybe empty) on servers that
  // have them — absent means an older server.
  next_up?: HubItem[];
  plan_to_watch?: HubItem[];
  /** Most-watched titles across the server this week. Absent on older
   *  servers; the hub renders it as the "Trending" row. */
  trending?: HubItem[];
  recently_added: HubItem[];
  // Per-library "Recently added to <Library>" strips. Each entry is
  // one library's slice; the hub page renders one row per entry,
  // titled with library_name. Falls back to the flat recently_added
  // when older servers omit this.
  recently_added_by_library?: HubLibraryRow[];
}

export interface HubLibraryRow {
  library_id: string;
  library_name: string;
  library_type: string;
  items: HubItem[];
}

/** One entry of the saved home layout (users/me/preferences hub_layout):
 *  a row key and whether that row is shown. See lib/hubLayout.ts. */
export interface HubRowPref {
  key: string;
  enabled: boolean;
}

/** One row of GET /libraries/{id}/genres. */
export interface GenreCount {
  name: string;
  count: number;
}

export interface Library {
  id: string;
  name: string;
  /** movie | show | music | photo | anime | cartoons | home_video |
   *  audiobook | podcast | book | … — the server's library type. */
  type: string;
  created_at: string;
  updated_at: string;
}

/** Caller's watch state for a video (v2.5; manual marks included). */
export type WatchStateValue = 'watched' | 'in_progress' | 'unwatched';

/** `?watch=` on the library listing and /random. */
export type WatchFilter = 'unwatched' | 'in_progress' | 'watched';

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
  thumb_path?: string;
  created_at: string;
  updated_at: string;
  // v2.5 listing fields — absent on older servers.
  watch_state?: WatchStateValue;
  view_offset_ms?: number;
  /** Shows / seasons: episodes in total and episodes not yet watched. */
  leaf_count?: number;
  unwatched_count?: number;
}

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
  // SDH = subtitles for the deaf / hard-of-hearing (includes sound
  // descriptions, speaker labels). Surfaced as a "(SDH)" badge in the
  // picker; optional so older servers that omit it just don't show it.
  sdh?: boolean;
  /** The file marks this track default: shown when the user has no
   *  subtitle preference (lib/subtitleSelect). Absent from older servers. */
  default?: boolean;
}

export interface Chapter {
  title: string;
  start_ms: number;
  end_ms: number;
}

/** A subtitle file attached to a media file (downloaded from OpenSubtitles,
 *  or OCR'd from an image track), served as WebVTT at `url`
 *  (/media/external-subtitles/{id}, asset token). */
export interface ExternalSubtitle {
  id: string;
  file_id: string;
  language: string;
  title?: string | null;
  forced: boolean;
  sdh: boolean;
  /** 'opensubtitles' | 'ocr' | … */
  source: string;
  /** The provider's id: the OpenSubtitles file id for a download (the
   *  search result's provider_file_id, as a string), 'stream_N' for OCR. */
  source_id?: string | null;
  url: string;
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
  /** Frame rate as probed (e.g. 23.976). Absent on older servers. */
  frame_rate?: number;
  duration_ms?: number;
  /** A 24 h token scoped to this file's stream / subtitle routes, so a
   *  long play outlives the 1 h access token. Empty or absent on older
   *  servers. The player still signs URLs with the asset token
   *  (api.assetUrl); typed for parity with the other clients. */
  stream_token?: string;
  faststart: boolean;
  audio_streams: AudioStream[];
  subtitle_streams: SubtitleStream[];
  /** Absent when the file has none (the server omits an empty list). */
  external_subtitles?: ExternalSubtitle[];
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
  /** The parent's parent: an episode's show (episode → season → show). */
  grandparent_id?: string;
  index?: number;
  view_offset_ms: number;
  /** Playable videos (v2.5): the caller's watch state. Absent on other
   *  types and on older servers. */
  watch_state?: WatchStateValue;
  updated_at: number;
  is_favorite: boolean;
  files: ItemFile[];
}

export interface ChildItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  summary?: string;
  duration_ms?: number;
  poster_path?: string;
  thumb_path?: string;
  index?: number;
  /** A track's disc within its album (index is its number on that disc).
   *  Absent reads as disc 1: single-disc albums, non-tracks, older servers. */
  disc_number?: number;
  /** The caller's playback state on this child (episode rows). */
  view_offset_ms?: number;
  watched?: boolean;
}

// What Play on a show or season starts (GET /items/{id}/up-next, v2.5):
//   resume  — an episode is part-watched
//   next    — the episode after the last one finished
//   start   — nothing watched yet (first episode)
//   rewatch — everything watched (first episode again)
//   none    — no playable episodes
export interface UpNext {
  mode: 'resume' | 'next' | 'start' | 'rewatch' | 'none';
  episode?: {
    id: string;
    title: string;
    season_id: string;
    season_number: number;
    episode_number: number;
    view_offset_ms?: number;
    duration_ms?: number;
    thumb_path?: string;
  };
}

// ── Report a problem (v2.5) ────────────────────────────────────────────────

export type IssueKind = 'video' | 'audio' | 'subtitles' | 'wrong_match' | 'other';

/** A problem report as its reporter sees it. */
export interface MediaIssue {
  id: string;
  item_id: string;
  file_id?: string;
  kind: IssueKind;
  note?: string;
  status: 'open' | 'resolved' | 'dismissed';
  created_at: string;
  resolved_at?: string;
  resolution_note?: string;
}

// ── Audiobook listening speed (v2.5) ───────────────────────────────────────

/** Per-user speed for a book. source: 'book' (set on this book), 'recent'
 *  (the user's latest speed on another book) or 'default' (1.0). */
export interface PlaybackRate {
  rate: number;
  source: 'book' | 'recent' | 'default';
}

// ── Scrobbling ─────────────────────────────────────────────────────────────

/** Per-user external-scrobble status. Credentials are never returned. The
 *  Last.fm / Trakt fields (v2.5) are absent from older servers;
 *  *_available says whether the admin has set the service up at all. */
export interface ScrobbleStatus {
  listenbrainz_linked: boolean;
  listenbrainz_enabled: boolean;
  lastfm_available?: boolean;
  lastfm_linked?: boolean;
  lastfm_username?: string;
  trakt_available?: boolean;
  trakt_linked?: boolean;
  trakt_username?: string;
}

/** Where a Last.fm / Trakt link stands after a complete call. */
export interface ScrobbleLinkResult {
  status: 'pending' | 'slow_down' | 'linked' | 'expired' | 'denied';
  username?: string;
}

/** Last.fm link: the user approves OnScreen at auth_url, the TV polls
 *  complete with the sealed pending handle. */
export interface LastFMLinkStart {
  auth_url: string;
  pending: string;
}

/** Trakt link (device code): the user enters user_code at verification_url,
 *  the TV polls complete every `interval` seconds for `expires_in`. */
export interface TraktLinkStart {
  user_code: string;
  verification_url: string;
  expires_in: number;
  interval: number;
  pending: string;
}

export interface SearchResult {
  id: string;
  library_id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  thumb_path?: string;
}

export interface ManagedProfile {
  id: string;
  user_id: string;
  username: string;
  avatar_url?: string;
  max_content_rating?: string | null;
}

export interface TranscodeSession {
  session_id: string;
  playlist_url: string;
  token: string;
  /** Content position (seconds) the stream begins at: stream time 0 is this
   *  far into the item. Keyframe-aligned, so a remux (video_copy) can open
   *  several seconds before the requested position; 0 for a stream that
   *  covers the whole file. A real 0 is a value, not "absent" (see
   *  sessionOffsetMs in player/session.ts). */
  start_offset_sec?: number;
  /** How far into the stream (seconds) seg 0's audio starts: an AAC
   *  re-encode after a mid-stream -ss warms up over a moment of silent
   *  video, and playback starts here instead. 0 = no measurable gap. */
  seg0_audio_gap_sec?: number;
}

// ── Device pairing ──────────────────────────────────────────────────────────

// Server response from POST /auth/pair/code. The TV displays the PIN +
// the URL for the user to enter on a phone / laptop, then long-polls
// /auth/pair/poll with the device_token until the server returns the
// signed-in token pair (or the code expires and we recycle).
export interface PairCodeResponse {
  pin: string;
  device_token: string;
  expires_at: string;
  poll_after: number;
}

// ── Favorites + History ─────────────────────────────────────────────────────

export interface FavoriteItem {
  id: string;
  library_id: string;
  type: string;
  title: string;
  year?: number;
  summary?: string;
  poster_path?: string;
  thumb_path?: string;
  duration_ms?: number;
  favorited_at: number;
}

export interface HistoryItem {
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

// ── Markers (intro / credits) ───────────────────────────────────────────────

// Marker windows surfaced on the watch route as "Skip Intro" /
// "Skip Credits" affordances. kind is "intro" | "credits"; source is
// "auto" | "manual" | "chapter" (informational only, the client
// doesn't differentiate).
export interface Marker {
  kind: string;
  start_ms: number;
  end_ms: number;
  source: string;
}

// ── Cross-device sync ──────────────────────────────────────────────────────

// Notification SSE event shape. The server multiplexes user-facing
// notifications with internal sync events (progress.updated) on one
// stream — clients filter by `type` and act on the ones they care
// about. The TV player consumes progress.updated (resume sync) and
// playback.stop (an admin stopped this stream — see playbackStop.ts).
export interface NotificationEvent {
  id: string;
  type: string;
  item_id?: string;
  data?: { position_ms?: number; duration_ms?: number; state?: string; [key: string]: unknown };
}

// ── Collections ─────────────────────────────────────────────────────────────

export interface MediaCollection {
  id: string;
  name: string;
  description?: string;
  // Auto-generated collections (auto_genre) vs manual playlists vs
  // smart-rule lists. The TV detail page renders all three the same
  // way (a grid of items), so the type is informational only.
  type: string;
  genre?: string;
  poster_path?: string;
  created_at: string;
}

export interface CollectionItem {
  id: string;
  title: string;
  type: string;
  year?: number;
  poster_path?: string;
  duration_ms?: number;
  position?: number;
}

// ── Discover (TMDB-backed) + Requests ──────────────────────────────────────

export interface DiscoverItem {
  type: string; // "movie" | "show"
  tmdb_id: number;
  title: string;
  year?: number;
  overview?: string;
  rating?: number;
  poster_url?: string;
  fanart_url?: string;
  in_library?: boolean;
  library_item_id?: string;
  has_active_request?: boolean;
  active_request_id?: string;
  active_request_status?: string;
}

export interface MediaRequest {
  id: string;
  user_id: string;
  type: string;
  tmdb_id: number;
  title: string;
  year?: number;
  poster_url?: string;
  overview?: string;
  status: string;
  created_at?: string;
  updated_at?: string;
}

// ── Online subtitle search ─────────────────────────────────────────────────

export interface OnlineSubtitle {
  provider_file_id: number;
  file_name: string;
  language: string;
  release?: string;
  hearing_impaired?: boolean;
  hd?: boolean;
  from_trusted?: boolean;
  rating?: number;
  download_count?: number;
  uploader_name?: string;
}

// ── Live TV / DVR ──────────────────────────────────────────────────────────

export interface Channel {
  id: string;
  tuner_id: string;
  tuner_name: string;
  tuner_type: string;
  number: string;
  callsign?: string;
  name: string;
  logo_url?: string;
  enabled?: boolean;
  sort_order?: number;
  epg_channel_id?: string;
}

export interface NowNext {
  channel_id: string;
  program_id: string;
  title: string;
  subtitle?: string;
  starts_at: string;
  ends_at: string;
  season_num?: number;
  episode_num?: number;
}

export interface Recording {
  id: string;
  schedule_id?: string;
  channel_id: string;
  channel_number: string;
  channel_name: string;
  channel_logo?: string;
  program_id?: string;
  title: string;
  subtitle?: string;
  season_num?: number;
  episode_num?: number;
  status: string;
  starts_at: string;
  ends_at: string;
  item_id?: string;
  error?: string;
}
