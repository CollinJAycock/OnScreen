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
