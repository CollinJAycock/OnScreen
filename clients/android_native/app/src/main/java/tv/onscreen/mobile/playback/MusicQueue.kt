package tv.onscreen.mobile.playback

import tv.onscreen.mobile.data.model.ChildItem

/**
 * Pure planning for the background player's music queue (no Android types,
 * so it's unit-tested on the JVM — see MusicQueueTest).
 *
 * Gapless needs the NEXT track in ExoPlayer's playlist before the current one
 * ends: ExoPlayer then decodes across the boundary, trimming the encoder
 * delay/padding it reads from iTunSMPB / LAME headers (MP3, AAC) — FLAC is
 * gapless by construction. Loading one item at a time and chaining on
 * STATE_ENDED (what the service used to do) always leaves a gap: the next
 * file isn't even requested until the previous has finished playing.
 *
 * So the service turns "play this track" into "play this album from this
 * track": the album's other tracks go in before and after it. Their stream
 * URLs aren't known yet — a /children row carries no file — so each queue
 * entry points at a placeholder uri ([placeholderUri]) that the service's
 * data source resolves (one /items/{id} fetch, which also yields the file's
 * ReplayGain tags) when ExoPlayer first opens it, typically ~50 s before the
 * track is due. One /children request builds the queue, however long the
 * album, and every stream token is fetched fresh at play time.
 */
object MusicQueue {

    /** Scheme of the lazily-resolved queue entries. Carries only the item id —
     *  no credential — so it's safe in platform session metadata. */
    const val PLACEHOLDER_SCHEME = "onscreen-item"
    private const val PLACEHOLDER_PREFIX = "$PLACEHOLDER_SCHEME://"

    fun placeholderUri(itemId: String): String = PLACEHOLDER_PREFIX + itemId

    /** The item id of a [placeholderUri], or null for any other uri. */
    fun itemIdFromPlaceholder(uri: String?): String? {
        if (uri == null || !uri.startsWith(PLACEHOLDER_PREFIX)) return null
        return uri.removePrefix(PLACEHOLDER_PREFIX).trimEnd('/').takeIf { it.isNotEmpty() }
    }

    /** Item types whose siblings are queued around them. Music only:
     *  audiobooks keep chaining book → book on STATE_ENDED (no gap concern,
     *  and a sibling "book" isn't an obvious next listen). */
    fun expandsQueue(type: String?): Boolean = type == TRACK

    /** Tracks around the anchor, in the server's album order. */
    data class Plan(val before: List<ChildItem>, val after: List<ChildItem>) {
        val size: Int get() = before.size + 1 + after.size
    }

    /**
     * Split an album listing around [anchorId]: the tracks before it and the
     * tracks after it. Keeps the server's order (/items/{id}/children sorts
     * by disc, then track number — the album page's order, and
     * [ChildItem.PLAY_ORDER]) and only [type] rows; null when the
     * anchor isn't in the listing (moved / deleted meanwhile), so the caller
     * leaves the single-item queue alone.
     */
    fun planAround(anchorId: String, type: String, children: List<ChildItem>): Plan? {
        val playable = children.filter { it.type == type }.distinctBy { it.id }
        val pos = playable.indexOfFirst { it.id == anchorId }
        if (pos < 0) return null
        return Plan(
            before = playable.subList(0, pos).toList(),
            after = playable.subList(pos + 1, playable.size).toList(),
        )
    }

    /**
     * What to append when playback reaches the end of the queue at
     * [currentId]: the rest of its own album if the queue is missing it (the
     * album expansion failed or hadn't landed), else the whole next album.
     * Anything already queued ([queuedIds]) is dropped, so a queue can never
     * loop back over itself.
     *
     * [nextContainer] (the next album's id + listing) is only consulted when
     * the current album has nothing left, so its fetch is skipped when not
     * needed. Null when there's nothing to add.
     */
    suspend fun continuation(
        currentId: String,
        type: String,
        albumId: String,
        albumTracks: List<ChildItem>,
        queuedIds: Set<String>,
        nextContainer: suspend () -> Pair<String, List<ChildItem>>?,
    ): Continuation? {
        val rest = planAround(currentId, type, albumTracks)?.after.orEmpty()
            .filter { it.id !in queuedIds }
        if (rest.isNotEmpty()) return Continuation(albumId, rest)
        val (nextId, listing) = nextContainer() ?: return null
        val tracks = listing
            .filter { it.type == type && it.id !in queuedIds }
            .distinctBy { it.id }
        return if (tracks.isEmpty()) null else Continuation(nextId, tracks)
    }

    /** Tracks to append, all children of [parentId]. */
    data class Continuation(val parentId: String, val tracks: List<ChildItem>)

    /** An artist's albums in play order: by year, then index, undated last.
     *  The order [NextSiblingResolver.nextContainer] walks when the queue
     *  runs off the end of an album. */
    val ALBUM_ORDER: Comparator<ChildItem> =
        compareBy({ it.year ?: Int.MAX_VALUE }, { it.index ?: Int.MAX_VALUE })

    /** Container pages whose Play starts music through a track ([playStart]). */
    fun startsFromContainer(type: String?): Boolean = type == ALBUM || type == ARTIST

    /**
     * The track Play on an album / artist page hands the player — it then
     * rides the same path as playing a track from its own page, so the
     * service queues the album around it and the next albums after it:
     *  - album: its first track, in the album page's (server) order;
     *  - artist: the first track of its first album in [ALBUM_ORDER], so the
     *    queue carries on through the whole discography in that order.
     * [children] is the page's own listing; [childrenOf] fetches an album's
     * tracks (artist only, stopping at the first album that has any). Null
     * when there's nothing to play.
     */
    suspend fun playStart(
        type: String,
        children: List<ChildItem>,
        childrenOf: suspend (String) -> List<ChildItem>,
    ): String? = when (type) {
        ALBUM -> firstTrackId(children)
        ARTIST -> {
            var start: String? = null
            for (album in children.filter { it.type == ALBUM }.distinctBy { it.id }.sortedWith(ALBUM_ORDER)) {
                start = firstTrackId(childrenOf(album.id))
                if (start != null) break
            }
            start
        }
        else -> null
    }

    private fun firstTrackId(children: List<ChildItem>): String? =
        children.firstOrNull { it.type == TRACK }?.id

    const val TRACK = "track"
    const val ALBUM = "album"
    const val ARTIST = "artist"
}
