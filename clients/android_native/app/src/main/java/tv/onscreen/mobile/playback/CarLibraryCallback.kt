package tv.onscreen.mobile.playback

import android.content.Context
import android.content.Intent
import android.os.Bundle
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.session.LibraryResult
import androidx.media3.session.MediaConstants
import androidx.media3.session.MediaLibraryService.LibraryParams
import androidx.media3.session.MediaLibraryService.MediaLibrarySession
import androidx.media3.session.MediaSession
import androidx.media3.session.SessionError
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.guava.future
import tv.onscreen.mobile.playback.CarBrowse.CarEntry
import tv.onscreen.mobile.playback.CarBrowse.CarNode

/**
 * [PlaybackService]'s session callback: every controller gets the full
 * player command set ([FullAccessSessionCallback]), and media browsers this
 * app trusts — Android Auto above all — also get the library ([CarBrowse]):
 * browsing, search, and playing an item by its id or by voice.
 *
 * Who may browse: this app, a controller Media3 trusts (the system, a
 * holder of MEDIA_CONTENT_CONTROL), and the car hosts in [CAR_HOSTS]. The
 * service is exported so a car can bind it; any other app that connects
 * still gets play / pause / skip (Bluetooth, a watch — as before), but not
 * the library's contents or its artwork.
 */
open class CarLibraryCallback(
    private val context: Context,
    private val browse: CarBrowse,
    private val scope: CoroutineScope,
    private val signedIn: suspend () -> Boolean,
) : FullAccessSessionCallback(), MediaLibrarySession.Callback {

    /** Search results by query, for the paging calls after onSearch. */
    private val searchResults = object : LinkedHashMap<String, List<CarNode>>(4, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, List<CarNode>>?) = size > 4
    }

    override fun onConnectAsync(
        session: MediaSession,
        controller: MediaSession.ControllerInfo,
    ): ListenableFuture<MediaSession.ConnectionResult> {
        if (!mayBrowse(controller)) return super<FullAccessSessionCallback>.onConnectAsync(session, controller)
        if (controller.packageName != context.packageName) {
            // The car draws the tiles from CarArtworkProvider uris.
            context.grantUriPermission(
                controller.packageName,
                CarArtworkProvider.rootUri(context),
                Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_PREFIX_URI_PERMISSION,
            )
        }
        return Futures.immediateFuture(
            MediaSession.ConnectionResult.AcceptedResultBuilder(session, controller)
                .setAvailableSessionCommands(MediaSession.ConnectionResult.DEFAULT_SESSION_AND_LIBRARY_COMMANDS)
                .setAvailablePlayerCommands(MediaSession.ConnectionResult.DEFAULT_PLAYER_COMMANDS)
                .build(),
        )
    }

    private fun mayBrowse(controller: MediaSession.ControllerInfo): Boolean =
        controller.packageName == context.packageName ||
            controller.packageName in CAR_HOSTS ||
            controller.isTrusted

    override fun onGetLibraryRoot(
        session: MediaLibrarySession,
        browser: MediaSession.ControllerInfo,
        params: LibraryParams?,
    ): ListenableFuture<LibraryResult<MediaItem>> {
        val extras = Bundle().apply {
            putInt(MediaConstants.EXTRAS_KEY_CONTENT_STYLE_BROWSABLE, MediaConstants.EXTRAS_VALUE_CONTENT_STYLE_LIST_ITEM)
            putInt(MediaConstants.EXTRAS_KEY_CONTENT_STYLE_PLAYABLE, MediaConstants.EXTRAS_VALUE_CONTENT_STYLE_LIST_ITEM)
        }
        val root = MediaItem.Builder()
            .setMediaId(CarIds.ROOT)
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle("OnScreen")
                    .setIsBrowsable(true)
                    .setIsPlayable(false)
                    .setMediaType(MediaMetadata.MEDIA_TYPE_FOLDER_MIXED)
                    .build(),
            )
            .build()
        return Futures.immediateFuture(LibraryResult.ofItem(root, LibraryParams.Builder().setExtras(extras).build()))
    }

    override fun onGetChildren(
        session: MediaLibrarySession,
        browser: MediaSession.ControllerInfo,
        parentId: String,
        page: Int,
        pageSize: Int,
        params: LibraryParams?,
    ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> = scope.future {
        guarded {
            val offset = (page.toLong() * pageSize).coerceIn(0, Int.MAX_VALUE.toLong()).toInt()
            val nodes = browse.children(parentId, offset, pageSize)
                ?: return@guarded LibraryResult.ofError<ImmutableList<MediaItem>>(NOT_FOUND)
            LibraryResult.ofItemList(nodes.map(::browseItem), params)
        }
    }

    override fun onGetItem(
        session: MediaLibrarySession,
        browser: MediaSession.ControllerInfo,
        mediaId: String,
    ): ListenableFuture<LibraryResult<MediaItem>> = scope.future {
        guarded {
            val node = browse.node(mediaId)
                ?: return@guarded LibraryResult.ofError<MediaItem>(NOT_FOUND)
            LibraryResult.ofItem(browseItem(node), null)
        }
    }

    override fun onSearch(
        session: MediaLibrarySession,
        browser: MediaSession.ControllerInfo,
        query: String,
        params: LibraryParams?,
    ): ListenableFuture<LibraryResult<Void>> = scope.future {
        guarded {
            val results = browse.search(query)
            synchronized(searchResults) { searchResults[query] = results }
            session.notifySearchResultChanged(browser, query, results.size, params)
            LibraryResult.ofVoid()
        }
    }

    override fun onGetSearchResult(
        session: MediaLibrarySession,
        browser: MediaSession.ControllerInfo,
        query: String,
        page: Int,
        pageSize: Int,
        params: LibraryParams?,
    ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> = scope.future {
        guarded {
            val results = synchronized(searchResults) { searchResults[query] } ?: browse.search(query)
            val offset = (page.toLong() * pageSize).coerceIn(0, Int.MAX_VALUE.toLong()).toInt()
            LibraryResult.ofItemList(results.drop(offset).take(pageSize).map(::browseItem), params)
        }
    }

    /**
     * A browse id (a tap in the car) or a spoken "play …" from a controller
     * that may browse becomes a queue of server items; anything else — the phone's own screens hand over items
     * that already carry their uri — goes through unchanged.
     */
    override fun onSetMediaItems(
        mediaSession: MediaSession,
        controller: MediaSession.ControllerInfo,
        mediaItems: MutableList<MediaItem>,
        startIndex: Int,
        startPositionMs: Long,
    ): ListenableFuture<MediaSession.MediaItemsWithStartPosition> {
        // Only a controller that may browse gets the library resolved for it
        // (the service is exported; any app can connect for play / pause).
        val only = mediaItems.singleOrNull()?.takeIf { it.localConfiguration == null && mayBrowse(controller) }
        val browseId = only?.mediaId?.takeIf { CarIds.isCarId(it) }
        val spoken = only?.takeIf { it.mediaId.isEmpty() }?.requestMetadata?.searchQuery
        if (browseId == null && spoken == null) {
            return super<MediaLibrarySession.Callback>.onSetMediaItems(mediaSession, controller, mediaItems, startIndex, startPositionMs)
        }
        if (browseId == null && spoken.isNullOrBlank()) {
            // "Play OnScreen" with nothing named: carry on with the queue.
            val player = mediaSession.player
            if (player.mediaItemCount == 0) {
                return Futures.immediateFailedFuture(IllegalStateException("nothing queued to resume"))
            }
            val queue = (0 until player.mediaItemCount).map(player::getMediaItemAt)
            return Futures.immediateFuture(
                MediaSession.MediaItemsWithStartPosition(queue, player.currentMediaItemIndex, player.currentPosition),
            )
        }
        return scope.future {
            if (!signedIn()) throw IllegalStateException("signed out")
            val queue = (if (browseId != null) browse.queueFor(browseId) else browse.queueForVoice(spoken!!))
                ?: throw IllegalStateException("nothing to play for ${browseId ?: spoken}")
            MediaSession.MediaItemsWithStartPosition(queue.entries.map(::queueItem), queue.startIndex, queue.startMs)
        }
    }

    /** Signed out or a failed fetch, as the error a car shows. */
    private suspend fun <T : Any> guarded(block: suspend () -> LibraryResult<T>): LibraryResult<T> {
        if (!signedIn()) {
            return LibraryResult.ofError<T>(
                SessionError(SessionError.ERROR_SESSION_AUTHENTICATION_EXPIRED, "Sign in to OnScreen on your phone"),
            )
        }
        return try {
            block()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            LibraryResult.ofError<T>(SessionError(SessionError.ERROR_IO, "Couldn't reach your OnScreen server"))
        }
    }

    private fun browseItem(node: CarNode): MediaItem {
        val extras = if (node.browsable) {
            val style = if (node.gridChildren) {
                MediaConstants.EXTRAS_VALUE_CONTENT_STYLE_GRID_ITEM
            } else {
                MediaConstants.EXTRAS_VALUE_CONTENT_STYLE_LIST_ITEM
            }
            Bundle().apply {
                putInt(MediaConstants.EXTRAS_KEY_CONTENT_STYLE_BROWSABLE, style)
                putInt(MediaConstants.EXTRAS_KEY_CONTENT_STYLE_PLAYABLE, MediaConstants.EXTRAS_VALUE_CONTENT_STYLE_LIST_ITEM)
            }
        } else {
            null
        }
        return MediaItem.Builder()
            .setMediaId(node.id)
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(node.title)
                    .setSubtitle(node.subtitle)
                    .setIsBrowsable(node.browsable)
                    .setIsPlayable(node.playable)
                    .setMediaType(mediaType(node.type))
                    .setArtworkUri(node.artworkPath?.let { CarArtworkProvider.uri(context, it) })
                    .setExtras(extras)
                    .build(),
            )
            .build()
    }

    /** A queue entry the player resolves when it reaches it (see
     *  [MusicQueue.placeholderUri]), with the extras the service reads to
     *  carry on past it: an album into the artist's next, a book's chapter
     *  into the next chapter, a book into the author's next book. */
    private fun queueItem(entry: CarEntry): MediaItem =
        QueueItems.placeholder(
            id = entry.itemId, title = entry.title, type = entry.type,
            parentId = entry.parentId, index = entry.index,
        ) {
            setArtist(entry.artist)
            setAlbumTitle(entry.album)
            setMediaType(mediaType(entry.type))
            setArtworkUri(entry.artworkPath?.let { CarArtworkProvider.uri(context, it) })
        }

    private fun mediaType(type: String): Int = when (type) {
        CarBrowse.MUSIC -> MediaMetadata.MEDIA_TYPE_FOLDER_MIXED
        CarBrowse.AUDIOBOOK_LIBRARY -> MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS
        CarBrowse.PODCAST_LIBRARY -> MediaMetadata.MEDIA_TYPE_FOLDER_PODCASTS
        CarBrowse.ARTIST -> MediaMetadata.MEDIA_TYPE_ARTIST
        CarBrowse.ALBUM -> MediaMetadata.MEDIA_TYPE_ALBUM
        CarBrowse.TRACK -> MediaMetadata.MEDIA_TYPE_MUSIC
        CarBrowse.BOOK_AUTHOR -> MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS
        CarBrowse.BOOK_SERIES -> MediaMetadata.MEDIA_TYPE_FOLDER_AUDIO_BOOKS
        CarBrowse.AUDIOBOOK -> MediaMetadata.MEDIA_TYPE_AUDIO_BOOK
        CarBrowse.CHAPTER -> MediaMetadata.MEDIA_TYPE_AUDIO_BOOK_CHAPTER
        CarBrowse.PODCAST -> MediaMetadata.MEDIA_TYPE_PODCAST
        CarBrowse.EPISODE -> MediaMetadata.MEDIA_TYPE_PODCAST_EPISODE
        else -> MediaMetadata.MEDIA_TYPE_FOLDER_MIXED
    }

    companion object {
        private val NOT_FOUND = SessionError(SessionError.ERROR_BAD_VALUE, "Not found")

        /**
         * Car hosts that may browse: Android Auto (the phone app, which the
         * Desktop Head Unit also connects through), Android Automotive's
         * media centre, and the in-car Assistant. Media3 names a controller
         * by the package of its calling uid, so another app can't claim
         * these names while the real one is installed.
         */
        val CAR_HOSTS = setOf(
            "com.google.android.projection.gearhead",
            "com.android.car.media",
            "com.google.android.carassistant",
        )
    }
}
