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
  continue_watching_tv?: HubItem[];
  continue_watching_movies?: HubItem[];
  continue_watching_other?: HubItem[];
  recently_added: HubItem[];
}

export interface Library {
  id: string;
  name: string;
  type: 'movie' | 'show' | 'music' | 'photo';
  created_at: string;
  updated_at: string;
}

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
// about. The TV client only consumes progress.updated for resume sync.
export interface NotificationEvent {
  id: string;
  type: string;
  item_id?: string;
  data?: { position_ms?: number; duration_ms?: number; state?: string };
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
//
// `/api/v1/discover/search` is a TMDB proxy that also annotates each
// row with `in_library` + active-request status so the UI can hide
// titles the user already has and surface "Already requested"
// states on titles they've asked for. A user submitting a request
// goes through `POST /api/v1/requests` which an admin then routes
// to the configured Sonarr / Radarr.

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
  /** "pending" | "approved" | "declined" | "downloading" |
   *  "available" | "failed". UI maps each to a chip colour. */
  active_request_status?: string;
}

export interface MediaRequest {
  id: string;
  user_id: string;
  type: string; // "movie" | "show"
  tmdb_id: number;
  title: string;
  year?: number;
  poster_url?: string;
  overview?: string;
  status: string;
  created_at?: string;
  updated_at?: string;
}

// ── Online subtitle search (OpenSubtitles) ─────────────────────────────────
//
// In-player feature: when the file's bundled subtitle tracks miss
// the user's preferred language (or when a sub-quality replacement
// is wanted), search OpenSubtitles via the server and download a
// pick into the file's external_subtitles. The newly-downloaded
// track surfaces in the next `/items/{id}` fetch's
// subtitle_streams list.

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
//
// Channels come from the configured tuner (HDHomeRun, xTeVe, etc.).
// now-next pairs each channel with its current + next program for
// the EPG row. Recordings are the DVR queue + completed history;
// `item_id` is set once the recording lands in a media_item and
// can be played through the standard /watch flow.

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
  starts_at: string; // ISO8601
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
  /** "scheduled" | "recording" | "completed" | "failed" |
   *  "cancelled". When status=completed and item_id is set,
   *  the recording is playable via the standard /watch flow. */
  status: string;
  starts_at: string;
  ends_at: string;
  item_id?: string;
  error?: string;
}
