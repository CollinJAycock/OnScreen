package tv.onscreen.android.playback

import androidx.media3.common.MediaMetadata
import kotlinx.coroutines.CancellationException
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.repository.ItemRepository

/**
 * What the background media session says is playing: the title, plus the
 * artist and album a track sits under (a chapter: the author and the book;
 * a book: its author). The fragment's media items carry only a URL, so
 * without this the session, and the Fire TV / Google TV now-playing UI
 * and voice assistants reading it, had nothing to name.
 */
data class NowPlaying(
    val title: String,
    val artist: String? = null,
    val album: String? = null,
    /** The item's id, as the session's media id. */
    val mediaId: String = "",
) {

    /** [base] (what the player read from the stream, e.g. embedded
     *  artwork) with this item's names on top.
     *
     *  The artist and album also go in as the subtitle and description.
     *  Media3 copies the title into the legacy session metadata as its
     *  display title, and the legacy description (what Fire TV's and
     *  Google TV's now-playing UI read) then takes its three lines from the
     *  display title, subtitle and description only, never the artist and
     *  album: with just the title, it read "title, null, null". */
    fun toMediaMetadata(base: MediaMetadata = MediaMetadata.EMPTY): MediaMetadata =
        base.buildUpon()
            .setTitle(title)
            .setDisplayTitle(title)
            .apply {
                artist?.let {
                    setArtist(it)
                    setSubtitle(it)
                }
                album?.let {
                    setAlbumTitle(it)
                    setDescription(it)
                }
            }
            .build()

    companion object {
        /** From [item] and the items above it: a track's parent is its album
         *  and grandparent its artist; a chapter's are its book and author; a
         *  book's parent is its author. Missing levels fall back to the
         *  item's original title (the scanner's artist / author), else are
         *  left out. */
        fun of(item: ItemDetail, parent: ItemDetail? = null, grandparent: ItemDetail? = null): NowPlaying {
            val own = item.original_title?.takeIf { it.isNotBlank() }
            return when (item.type) {
                AudioItemTypes.TRACK -> NowPlaying(
                    title = item.title,
                    artist = grandparent?.title?.takeIf { it.isNotBlank() } ?: own,
                    album = parent?.title?.takeIf { it.isNotBlank() },
                    mediaId = item.id,
                )
                AudiobookSpeed.CHAPTER -> NowPlaying(
                    title = item.title,
                    artist = grandparent?.title?.takeIf { it.isNotBlank() }
                        ?: parent?.original_title?.takeIf { it.isNotBlank() },
                    album = parent?.title?.takeIf { it.isNotBlank() },
                    mediaId = item.id,
                )
                AudiobookSpeed.AUDIOBOOK -> NowPlaying(
                    title = item.title,
                    artist = parent?.title?.takeIf { it.isNotBlank() } ?: own,
                    mediaId = item.id,
                )
                else -> NowPlaying(title = item.title, mediaId = item.id)
            }
        }

        /** [of], looking the parent (and, for a track or chapter, the
         *  grandparent) up. Best effort: a lookup that fails leaves that
         *  name out rather than holding playback up. */
        suspend fun resolve(itemRepo: ItemRepository, item: ItemDetail): NowPlaying {
            if (!AudioItemTypes.isAudio(item.type)) return of(item)
            val parent = item.parent_id?.let { lookup(itemRepo, it) }
            val grandparent = if (item.type == AudioItemTypes.TRACK || item.type == AudiobookSpeed.CHAPTER) {
                parent?.parent_id?.let { lookup(itemRepo, it) }
            } else {
                null
            }
            return of(item, parent, grandparent)
        }

        private suspend fun lookup(itemRepo: ItemRepository, id: String): ItemDetail? = try {
            itemRepo.getItem(id)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            null
        }
    }
}
