package tv.onscreen.mobile.ui.components

import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import tv.onscreen.mobile.R

/**
 * The kind of a library, item or collection ([type] is the server's wire
 * value, e.g. "movie", "smart_playlist") as a string resource, or null for
 * a kind the app doesn't know. The English is the wire value itself, as the
 * app has always shown it; the translations name the kind.
 */
@StringRes
fun wireTypeRes(type: String): Int? = when (type) {
    "movie" -> R.string.library_type_movie
    "show" -> R.string.library_type_show
    "season" -> R.string.library_type_season
    "episode" -> R.string.library_type_episode
    "track" -> R.string.library_type_track
    "album" -> R.string.library_type_album
    "artist" -> R.string.library_type_artist
    "photo" -> R.string.library_type_photo
    "music_video" -> R.string.library_type_music_video
    "audiobook" -> R.string.library_type_audiobook
    "audiobook_chapter" -> R.string.library_type_audiobook_chapter
    "book_author" -> R.string.library_type_book_author
    "book_series" -> R.string.library_type_book_series
    "podcast" -> R.string.library_type_podcast
    "podcast_episode" -> R.string.library_type_podcast_episode
    "home_video" -> R.string.library_type_home_video
    "book" -> R.string.library_type_book
    "music" -> R.string.library_type_music
    "anime" -> R.string.library_type_anime
    "manga" -> R.string.library_type_manga
    "dvr" -> R.string.library_type_dvr
    "mixed" -> R.string.library_type_mixed
    "playlist" -> R.string.library_type_playlist
    "smart_playlist" -> R.string.library_type_smart_playlist
    "auto_genre" -> R.string.library_type_auto_genre
    "event_folder" -> R.string.library_type_event_folder
    "photo_album" -> R.string.library_type_photo_album
    "franchise" -> R.string.library_type_franchise
    else -> null
}

/** [wireTypeRes] in the app's language; an unknown kind shows as sent. */
@Composable
fun wireTypeName(type: String): String = wireTypeRes(type)?.let { stringResource(it) } ?: type
