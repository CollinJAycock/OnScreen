package tv.onscreen.mobile.playback

import kotlinx.coroutines.CancellationException
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.SearchResult

/**
 * The library as Android Auto (or any other media browser the service lets
 * in) browses it: the user's music, audiobook and podcast libraries, drilled
 * down to what can be played. Plain data, so it is testable without Android;
 * [PlaybackService] turns [CarNode]s and [CarQueue]s into Media3 items.
 *
 * Shape (the root's children become the car's tabs, at most four):
 *
 *     root
 *     ├─ Music library      → Artists → artist → album → track
 *     │                     → Albums → album → track
 *     ├─ Audiobook library  → author → (series →) book → chapter
 *     └─ Podcast library    → podcast → episode
 *
 * Ids ([CarIds]) carry what playing a node needs — its type and, for a
 * leaf, its container — so a tap in the car resolves to a queue without
 * first re-fetching the node.
 */
class CarBrowse(private val source: Source) {

    /** The server calls the tree needs (adapted from the repositories). */
    interface Source {
        suspend fun libraries(): List<Library>
        /** One page of a library's listing; [type] null = its top level. */
        suspend fun items(libraryId: String, type: String?, limit: Int, offset: Int): List<MediaItem>
        suspend fun children(id: String): List<ChildItem>
        suspend fun item(id: String): ItemDetail
        suspend fun search(query: String, limit: Int): List<SearchResult>
    }

    /** A node of the tree. [artworkPath] is the server's artwork path. */
    data class CarNode(
        val id: String,
        val title: String,
        val subtitle: String? = null,
        val type: String,
        val browsable: Boolean,
        val playable: Boolean,
        val artworkPath: String? = null,
        /** Show this node's children as a grid (artists, albums, books). */
        val gridChildren: Boolean = false,
    )

    /** One queue entry: a server item the player resolves to its stream. */
    data class CarEntry(
        val itemId: String,
        val title: String,
        val type: String,
        val parentId: String?,
        val index: Int?,
        val artist: String? = null,
        val album: String? = null,
        val artworkPath: String? = null,
    )

    /** What a tap (or a voice request) plays: [entries] from [startIndex],
     *  at [startMs] into it (a book or episode's resume point). */
    data class CarQueue(val entries: List<CarEntry>, val startIndex: Int, val startMs: Long)

    /** The root's children: the audio libraries, music first. */
    suspend fun root(): List<CarNode> =
        source.libraries()
            .filter { it.type in AUDIO_LIBRARY_TYPES }
            .sortedBy { AUDIO_LIBRARY_TYPES.indexOf(it.type) }
            .map {
                CarNode(
                    id = CarIds.library(it.id, it.type), title = it.name, type = it.type,
                    browsable = true, playable = false,
                    gridChildren = it.type != MUSIC,
                )
            }

    /**
     * The children of [id], [offset] / [limit] into the list. Null when the
     * id isn't one of this tree's (the caller reports "not found").
     */
    suspend fun children(id: String, offset: Int, limit: Int): List<CarNode>? {
        if (id == CarIds.ROOT) return root().page(offset, limit)
        CarIds.parseLibrary(id)?.let { (libraryId, type) ->
            return when (type) {
                MUSIC -> listOf(
                    CarNode(CarIds.listing(libraryId, ARTIST), "Artists", type = "folder",
                        browsable = true, playable = false, gridChildren = true),
                    CarNode(CarIds.listing(libraryId, ALBUM), "Albums", type = "folder",
                        browsable = true, playable = false, gridChildren = true),
                ).page(offset, limit)
                // An audiobook library lists its authors, a podcast library
                // its podcasts: the library's own top level.
                else -> listing(libraryId, null, offset, limit)
            }
        }
        CarIds.parseListing(id)?.let { (libraryId, type) -> return listing(libraryId, type, offset, limit) }
        CarIds.parseContainer(id)?.let { (type, itemId) ->
            return containerChildren(type, itemId).page(offset, limit)
        }
        return null
    }

    private suspend fun listing(libraryId: String, type: String?, offset: Int, limit: Int): List<CarNode> {
        // The server pages at most PAGE items; a car asking for "all" gets
        // the first MAX_LISTING (a car list is scrolled, not read through).
        val wanted = limit.coerceAtMost(MAX_LISTING - offset).coerceAtLeast(0)
        val out = ArrayList<CarNode>()
        var at = offset
        while (out.size < wanted) {
            val ask = minOf(PAGE, wanted - out.size)
            val page = source.items(libraryId, type, ask, at)
            page.mapTo(out) { containerNode(it.type, it.id, it.title, it.year?.toString(), it.poster_path) }
            if (page.size < ask) break // the listing ended
            at += page.size
        }
        return out
    }

    private suspend fun containerChildren(type: String, id: String): List<CarNode> {
        val children = source.children(id)
        return when (type) {
            ARTIST -> children.filter { it.type == ALBUM }
                .map { containerNode(it.type, it.id, it.title, it.year?.toString(), it.poster_path) }
            ALBUM -> children.filter { it.type == TRACK }.sortedWith(ChildItem.PLAY_ORDER)
                .map { leafNode(it, id) }
            BOOK_AUTHOR, BOOK_SERIES -> children.filter { it.type == BOOK_SERIES || it.type == AUDIOBOOK }
                .map { containerNode(it.type, it.id, it.title, it.year?.toString(), it.poster_path) }
            AUDIOBOOK -> AudiobookChapters.inOrder(children).map { leafNode(it, id) }
            PODCAST -> children.filter { it.type == EPISODE }.map { leafNode(it, id) }
            else -> emptyList()
        }
    }

    private fun containerNode(type: String, id: String, title: String, subtitle: String?, art: String?) =
        CarNode(
            id = CarIds.container(type, id), title = title, subtitle = subtitle, type = type,
            browsable = true,
            // An album or a book plays from the top (a book from where it
            // was left); an artist, author, series or podcast is a list.
            playable = type == ALBUM || type == AUDIOBOOK,
            artworkPath = art,
            gridChildren = type == ARTIST || type == BOOK_AUTHOR || type == BOOK_SERIES,
        )

    private fun leafNode(child: ChildItem, parentId: String) = CarNode(
        id = CarIds.leaf(child.type, parentId, child.id),
        title = child.title,
        subtitle = child.duration_ms?.let(::clock),
        type = child.type,
        browsable = false, playable = true,
        artworkPath = child.thumb_path ?: child.poster_path,
    )

    /** The node [id] names, fetched (a car asks for single items when it
     *  restores its last screen). Null for an id outside the tree. */
    suspend fun node(id: String): CarNode? {
        if (id == CarIds.ROOT) {
            return CarNode(CarIds.ROOT, "OnScreen", type = "folder", browsable = true, playable = false)
        }
        if (CarIds.parseLibrary(id) != null) return root().firstOrNull { it.id == id }
        CarIds.parseListing(id)?.let { (libraryId, _) ->
            return children(CarIds.library(libraryId, MUSIC), 0, 2)?.firstOrNull { it.id == id }
        }
        CarIds.parseContainer(id)?.let { (type, itemId) ->
            val item = source.item(itemId)
            return containerNode(type, itemId, item.title, null, item.poster_path)
        }
        CarIds.parseLeaf(id)?.let { (type, _, itemId) ->
            val item = source.item(itemId)
            return CarNode(id, item.title, item.duration_ms?.let(::clock), type,
                browsable = false, playable = true, artworkPath = item.poster_path)
        }
        return null
    }

    /**
     * What tapping [id] plays, or null if it isn't playable. A track plays
     * its album from that track (the service then carries on into the
     * artist's next album); a chapter plays its book from that chapter, at
     * the chapter's resume point; a book from its chapter in progress; an
     * episode on its own, from where it was left.
     */
    suspend fun queueFor(id: String): CarQueue? {
        CarIds.parseLeaf(id)?.let { (type, parentId, itemId) ->
            return when (type) {
                TRACK -> albumQueue(parentId, startAt = itemId)
                CHAPTER -> bookQueue(parentId, startAt = itemId)
                EPISODE -> {
                    val ep = source.item(itemId)
                    val podcast = runCatching { source.item(parentId) }.getOrNull()
                    CarQueue(
                        listOf(entry(itemId, ep.title, EPISODE, parentId, ep.index,
                            artist = podcast?.title, art = ep.poster_path ?: podcast?.poster_path)),
                        0, ep.view_offset_ms,
                    )
                }
                else -> null
            }
        }
        CarIds.parseContainer(id)?.let { (type, itemId) ->
            return when (type) {
                ALBUM -> albumQueue(itemId, startAt = null)
                AUDIOBOOK -> bookQueue(itemId, startAt = null)
                else -> null
            }
        }
        return null
    }

    private suspend fun albumQueue(albumId: String, startAt: String?): CarQueue? {
        val album = runCatching { source.item(albumId) }.getOrNull()
        val artist = album?.parent_id?.let { runCatching { source.item(it).title }.getOrNull() }
        val tracks = source.children(albumId).filter { it.type == TRACK }.sortedWith(ChildItem.PLAY_ORDER)
        if (tracks.isEmpty()) return null
        val start = startAt?.let { id -> tracks.indexOfFirst { it.id == id } }?.takeIf { it >= 0 } ?: 0
        return CarQueue(
            tracks.map {
                entry(it.id, it.title, TRACK, albumId, it.index,
                    artist = artist, album = album?.title, art = album?.poster_path ?: it.poster_path)
            },
            start, 0,
        )
    }

    private suspend fun bookQueue(bookId: String, startAt: String?): CarQueue? {
        val book = source.item(bookId)
        val author = book.parent_id?.let { runCatching { source.item(it).title }.getOrNull() }
        val chapters = AudiobookChapters.inOrder(source.children(bookId))
        if (chapters.isEmpty()) {
            // A single-file book plays itself; the service chains to the
            // author's next book at its end, as when it's played on the phone.
            if (book.files.isEmpty()) return null
            return CarQueue(
                listOf(entry(bookId, book.title, AUDIOBOOK, book.parent_id, book.index,
                    artist = author, art = book.poster_path)),
                0, book.view_offset_ms,
            )
        }
        val start = if (startAt != null) {
            chapters.indexOfFirst { it.id == startAt }.takeIf { it >= 0 } ?: 0
        } else {
            resumeChapter(chapters)
        }
        return CarQueue(
            chapters.map {
                entry(it.id, it.title, CHAPTER, bookId, it.index,
                    artist = author, album = book.title, art = book.poster_path)
            },
            start, chapters[start].view_offset_ms.takeUnless { chapters[start].watched } ?: 0,
        )
    }

    /** Where a book picks up: the chapter in progress, else the first one
     *  not finished, else (all finished) the start. */
    private fun resumeChapter(chapters: List<ChildItem>): Int =
        chapters.indexOfFirst { !it.watched && it.view_offset_ms > 0 }.takeIf { it >= 0 }
            ?: chapters.indexOfFirst { !it.watched }.takeIf { it >= 0 }
            ?: 0

    /** The audio results of a search, as browsable / playable nodes. */
    suspend fun search(query: String, limit: Int = SEARCH_LIMIT): List<CarNode> =
        source.search(query, limit).mapNotNull { r ->
            when (r.type) {
                ARTIST, ALBUM, BOOK_AUTHOR, BOOK_SERIES, AUDIOBOOK, PODCAST ->
                    containerNode(r.type, r.id, r.title, r.year?.toString(), r.poster_path)
                TRACK, CHAPTER, EPISODE -> {
                    // A search hit doesn't name its container; the queue
                    // needs it, so fetch it here (a handful of results).
                    val parent = try {
                        source.item(r.id).parent_id
                    } catch (e: CancellationException) {
                        throw e
                    } catch (_: Exception) {
                        null
                    } ?: return@mapNotNull null
                    CarNode(CarIds.leaf(r.type, parent, r.id), r.title, r.year?.toString(), r.type,
                        browsable = false, playable = true, artworkPath = r.thumb_path ?: r.poster_path)
                }
                else -> null // video, photos, books: not for the car
            }
        }

    /**
     * "Play <query>" said to the car: the best audio match, played. An
     * artist, author or podcast plays its first album / book / episode.
     */
    suspend fun queueForVoice(query: String): CarQueue? {
        for (node in search(query, VOICE_LIMIT)) {
            val queue = when {
                node.playable -> queueFor(node.id)
                node.type == ARTIST || node.type == BOOK_AUTHOR || node.type == BOOK_SERIES || node.type == PODCAST ->
                    children(node.id, 0, VOICE_LIMIT)?.firstOrNull { it.playable }?.let { queueFor(it.id) }
                else -> null
            }
            if (queue != null) return queue
        }
        return null
    }

    private fun entry(
        id: String, title: String, type: String, parentId: String?, index: Int?,
        artist: String? = null, album: String? = null, art: String? = null,
    ) = CarEntry(id, title, type, parentId, index, artist, album, art)

    private fun <T> List<T>.page(offset: Int, limit: Int): List<T> = drop(offset).take(limit)

    companion object {
        const val MUSIC = "music"
        const val AUDIOBOOK_LIBRARY = "audiobook"
        const val PODCAST_LIBRARY = "podcast"
        /** Library types the car shows, in tab order. */
        val AUDIO_LIBRARY_TYPES = listOf(MUSIC, AUDIOBOOK_LIBRARY, PODCAST_LIBRARY)

        const val ARTIST = "artist"
        const val ALBUM = "album"
        const val TRACK = "track"
        const val BOOK_AUTHOR = "book_author"
        const val BOOK_SERIES = "book_series"
        const val AUDIOBOOK = "audiobook"
        const val CHAPTER = AudiobookSpeed.CHAPTER
        const val PODCAST = "podcast"
        const val EPISODE = "podcast_episode"

        /** The server's largest page. */
        const val PAGE = 200
        /** Most rows a car listing gets. */
        const val MAX_LISTING = 1000
        const val SEARCH_LIMIT = 20
        const val VOICE_LIMIT = 10

        /** "1:02:03" / "4:05". */
        fun clock(ms: Long): String {
            val s = ms / 1000
            val h = s / 3600
            val m = (s % 3600) / 60
            val sec = s % 60
            return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
        }
    }
}

/**
 * Media ids of [CarBrowse]'s nodes. Server ids are UUIDs and types are
 * snake_case words, so ':' never appears inside a part.
 *
 *     root                              the root
 *     lib:<type>:<libraryId>            a library
 *     list:<type>:<libraryId>           a library's listing of one type
 *     c:<type>:<itemId>                 a container (artist, album, book…)
 *     p:<type>:<parentId>:<itemId>      a playable leaf and its container
 *
 * The player's queue uses the bare server id (progress is reported by it),
 * so these ids never reach the queue.
 */
object CarIds {
    const val ROOT = "root"

    fun library(id: String, type: String) = "lib:$type:$id"
    fun listing(libraryId: String, type: String) = "list:$type:$libraryId"
    fun container(type: String, id: String) = "c:$type:$id"
    fun leaf(type: String, parentId: String, id: String) = "p:$type:$parentId:$id"

    /** (libraryId, library type) */
    fun parseLibrary(id: String): Pair<String, String>? =
        parts(id, "lib", 2)?.let { (type, libId) -> libId to type }

    /** (libraryId, item type) */
    fun parseListing(id: String): Pair<String, String>? =
        parts(id, "list", 2)?.let { (type, libId) -> libId to type }

    /** (type, itemId) */
    fun parseContainer(id: String): Pair<String, String>? =
        parts(id, "c", 2)?.let { (type, itemId) -> type to itemId }

    /** (type, parentId, itemId) */
    fun parseLeaf(id: String): Triple<String, String, String>? =
        parts(id, "p", 3)?.let { (type, parent, itemId) -> Triple(type, parent, itemId) }

    /** Whether [id] is one of this tree's (vs a bare server id or a url). */
    fun isCarId(id: String): Boolean =
        id == ROOT || parseLibrary(id) != null || parseListing(id) != null ||
            parseContainer(id) != null || parseLeaf(id) != null

    private fun parts(id: String, prefix: String, n: Int): List<String>? {
        val p = id.split(':')
        if (p.size != n + 1 || p[0] != prefix || p.drop(1).any { it.isEmpty() }) return null
        return p.drop(1)
    }
}
