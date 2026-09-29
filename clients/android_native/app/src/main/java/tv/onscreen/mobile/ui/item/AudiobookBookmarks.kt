package tv.onscreen.mobile.ui.item

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.Bookmark
import tv.onscreen.mobile.data.model.Chapter
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.repository.AudiobookRepository
import tv.onscreen.mobile.playback.AudiobookChapters
import tv.onscreen.mobile.playback.AudiobookSpeed
import tv.onscreen.mobile.ui.player.ChapterNav
import javax.inject.Inject

/*
 * Bookmarks on an audiobook's item page, driven by
 * [AudiobookBookmarksViewModel]: chapter, timestamp and note per bookmark;
 * tap to play from there, edit the note, delete. Bookmarks are added from
 * the player (Add bookmark). ItemDetailScreen places [bookmarkSection] below
 * the book's header.
 */

/** Pure display rules for bookmarks — JVM-tested in BookmarkFormatTest. */
object BookmarkFormat {

    /** Matches the server's note limit (AudiobookRepository.NOTE_MAX). */
    const val NOTE_MAX = AudiobookRepository.NOTE_MAX

    /** `H:MM:SS`, or `M:SS` under an hour — the chapter list's format. */
    fun timestamp(positionMs: Long): String = ChapterNav.formatStart(positionMs.coerceAtLeast(0L))

    /**
     * The chapter a bookmark sits in, as the list names it:
     *  - single-file book (the bookmark is on the book itself, [bookId]):
     *    the embedded chapter at its position, from [chapters];
     *  - multi-file book: the chapter item's title, else "Chapter N" from
     *    its number ([chapterFallback] formats that).
     * Null when there's nothing to name it by (a file without chapter marks,
     * an untitled chapter with no number).
     */
    fun chapterLabel(
        bookmark: Bookmark,
        bookId: String,
        chapters: List<Chapter>,
        chapterFallback: (Int) -> String,
    ): String? {
        if (bookmark.item_id == bookId) {
            val at = ChapterNav.activeIndex(chapters, bookmark.position_ms)
            if (at < 0) return null
            return ChapterNav.displayTitle(chapters[at], at)
        }
        if (bookmark.item_title.isNotBlank()) return bookmark.item_title
        return bookmark.item_index?.let(chapterFallback)
    }

    /**
     * Bookmarks in listening order. The server orders a multi-file book's by
     * chapter number, but chapter files usually carry none, so this orders
     * them by where their chapter sits in [chapterOrder] (the book's chapter
     * ids, in listening order — see AudiobookChapters.inOrder), then by
     * position. Bookmarks on the book itself (single-file) come first;
     * chapters not in the listing keep the server's order, after the rest.
     */
    fun inListeningOrder(bookmarks: List<Bookmark>, bookId: String, chapterOrder: List<String>): List<Bookmark> {
        if (chapterOrder.isEmpty()) return bookmarks
        val rank = chapterOrder.withIndex().associate { (i, id) -> id to i }
        return bookmarks.withIndex()
            .sortedWith(
                compareBy<IndexedValue<Bookmark>>(
                    { if (it.value.item_id == bookId) -1 else rank[it.value.item_id] ?: Int.MAX_VALUE },
                    { if (rank.containsKey(it.value.item_id) || it.value.item_id == bookId) it.value.position_ms else 0L },
                    { it.index },
                ),
            )
            .map { it.value }
    }

    /** Characters in a note as the server counts them (code points, after
     *  trimming). */
    fun noteLength(note: String): Int {
        val t = note.trim()
        return t.codePointCount(0, t.length)
    }

    /** [note] cut to [NOTE_MAX] code points, so the field can't overrun
     *  the limit (and never splits a surrogate pair). */
    fun limitNote(note: String): String {
        if (note.codePointCount(0, note.length) <= NOTE_MAX) return note
        return note.substring(0, note.offsetByCodePoints(0, NOTE_MAX))
    }
}

data class BookmarksUi(
    val bookId: String? = null,
    /** Whether the section shows at all: an audiobook, on a server that
     *  takes bookmarks (false until the first load says so). */
    val available: Boolean = false,
    val loading: Boolean = false,
    val error: Boolean = false,
    val bookmarks: List<Bookmark> = emptyList(),
    /** Embedded chapter marks of a single-file book, for naming bookmarks. */
    val chapters: List<Chapter> = emptyList(),
)

/**
 * The bookmark list on an audiobook's page. The screen calls [bind] with
 * each detail it loads — including the reload on return from the player,
 * which is how a bookmark just added there shows up. Edits and deletes are
 * optimistic and roll back on failure, with a message on [messages].
 */
@HiltViewModel
class AudiobookBookmarksViewModel @Inject constructor(
    private val repo: AudiobookRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(BookmarksUi())
    val state: StateFlow<BookmarksUi> = _state.asStateFlow()

    private val _messages = Channel<Int>(Channel.BUFFERED)

    /** String resources to toast (a failed edit / delete). */
    val messages: Flow<Int> = _messages.receiveAsFlow()

    private var loadJob: Job? = null

    fun bind(detail: ItemDetail) {
        if (detail.type != AudiobookSpeed.AUDIOBOOK) {
            loadJob?.cancel()
            _state.value = BookmarksUi()
            return
        }
        val chapters = detail.files.firstOrNull()?.chapters.orEmpty()
        val current = _state.value
        _state.value = if (current.bookId == detail.id) {
            current.copy(chapters = chapters)
        } else {
            BookmarksUi(bookId = detail.id, chapters = chapters)
        }
        load()
    }

    fun retry() = load()

    private fun load() {
        val bookId = _state.value.bookId ?: return
        loadJob?.cancel()
        _state.value = _state.value.copy(loading = true, error = false)
        loadJob = viewModelScope.launch {
            val result = try {
                Result.success(repo.bookmarks(bookId))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Result.failure(e)
            }
            if (_state.value.bookId != bookId) return@launch
            val list = result.getOrNull()
            _state.value = when {
                result.isFailure -> _state.value.copy(available = true, loading = false, error = true)
                // A server without bookmarks: no section.
                list == null -> _state.value.copy(available = false, loading = false)
                else -> _state.value.copy(available = true, loading = false, bookmarks = list)
            }
        }
    }

    fun updateNote(bookmarkId: String, note: String) {
        val list = _state.value.bookmarks
        val at = list.indexOfFirst { it.id == bookmarkId }
        if (at < 0) return
        val original = list[at]
        val trimmed = note.trim()
        if (trimmed == original.note) return
        replaceLocally(original.copy(note = trimmed))
        viewModelScope.launch {
            try {
                repo.updateNote(bookmarkId, trimmed)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if ((e as? HttpException)?.code() == 404) {
                    // Deleted elsewhere meanwhile: drop it.
                    dropLocally(bookmarkId)
                } else {
                    restore(original, at)
                    _messages.trySend(R.string.bookmark_update_failed)
                }
            }
        }
    }

    fun delete(bookmarkId: String) {
        val list = _state.value.bookmarks
        val at = list.indexOfFirst { it.id == bookmarkId }
        if (at < 0) return
        val original = list[at]
        dropLocally(bookmarkId)
        viewModelScope.launch {
            try {
                repo.deleteBookmark(bookmarkId)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // 404: already gone — which is what was asked for.
                if ((e as? HttpException)?.code() != 404) {
                    restore(original, at)
                    _messages.trySend(R.string.bookmark_delete_failed)
                }
            }
        }
    }

    private fun replaceLocally(bookmark: Bookmark) {
        _state.value = _state.value.copy(
            bookmarks = _state.value.bookmarks.map { if (it.id == bookmark.id) bookmark else it },
        )
    }

    private fun dropLocally(bookmarkId: String) {
        _state.value = _state.value.copy(bookmarks = _state.value.bookmarks.filterNot { it.id == bookmarkId })
    }

    /** Roll a failed edit / delete back: [original] as it was, at [index]
     *  if it had been removed. Other changes made since stay. */
    private fun restore(original: Bookmark, index: Int) {
        val now = _state.value.bookmarks
        _state.value = _state.value.copy(
            bookmarks = if (now.any { it.id == original.id }) {
                now.map { if (it.id == original.id) original else it }
            } else {
                now.toMutableList().apply { add(index.coerceIn(0, size), original) }
            },
        )
    }
}

/**
 * The bookmark list: a header, then one row per bookmark (chapter, time,
 * note) — tap to play from there, the pencil to edit the note, the bin to
 * delete (confirmed). Nothing when [ui] isn't [BookmarksUi.available].
 */
internal fun LazyListScope.bookmarkSection(
    ui: BookmarksUi,
    chapterOrder: List<String>,
    onPlay: (Bookmark) -> Unit,
    onEditNote: (Bookmark, String) -> Unit,
    onDelete: (Bookmark) -> Unit,
    onRetry: () -> Unit,
) {
    val bookId = ui.bookId ?: return
    if (!ui.available) return
    item(key = "bookmarks-header") {
        Column(modifier = Modifier.padding(horizontal = 16.dp)) {
            Spacer(Modifier.height(16.dp))
            Text(stringResource(R.string.bookmarks_title), style = MaterialTheme.typography.titleMedium)
            Spacer(Modifier.height(4.dp))
            when {
                ui.error -> Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        stringResource(R.string.bookmarks_load_failed),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    TextButton(onClick = onRetry) { Text(stringResource(R.string.retry)) }
                }
                ui.bookmarks.isEmpty() && !ui.loading -> Text(
                    stringResource(R.string.bookmarks_empty),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
    val ordered = BookmarkFormat.inListeningOrder(ui.bookmarks, bookId, chapterOrder)
    items(ordered, key = { "bookmark-${it.id}" }) { b ->
        BookmarkRow(
            bookmark = b,
            bookId = bookId,
            chapters = ui.chapters,
            onPlay = { onPlay(b) },
            onEditNote = { note -> onEditNote(b, note) },
            onDelete = { onDelete(b) },
        )
    }
}

@Composable
private fun BookmarkRow(
    bookmark: Bookmark,
    bookId: String,
    chapters: List<Chapter>,
    onPlay: () -> Unit,
    onEditNote: (String) -> Unit,
    onDelete: () -> Unit,
) {
    var editing by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    val numbered = bookmark.item_index?.let { stringResource(R.string.bookmark_chapter_n, it) }
    val chapter = BookmarkFormat.chapterLabel(bookmark, bookId, chapters) { numbered.orEmpty() }
    val time = BookmarkFormat.timestamp(bookmark.position_ms)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onPlay)
            .padding(start = 16.dp, end = 4.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                chapter ?: time,
                style = MaterialTheme.typography.bodyLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (chapter != null) {
                Text(
                    time,
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (bookmark.note.isNotBlank()) {
                Text(
                    bookmark.note,
                    style = MaterialTheme.typography.bodyMedium,
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        IconButton(onClick = { editing = true }) {
            Icon(Icons.Default.Edit, contentDescription = stringResource(R.string.bookmark_edit_note))
        }
        IconButton(onClick = { confirmDelete = true }) {
            Icon(Icons.Default.Delete, contentDescription = stringResource(R.string.bookmark_delete))
        }
    }
    if (editing) {
        EditNoteDialog(
            initial = bookmark.note,
            onSave = { note ->
                editing = false
                onEditNote(note)
            },
            onDismiss = { editing = false },
        )
    }
    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text(stringResource(R.string.bookmark_delete_title)) },
            text = { Text(stringResource(R.string.bookmark_delete_body, chapter?.let { "$it · $time" } ?: time)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmDelete = false
                    onDelete()
                }) { Text(stringResource(R.string.delete)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmDelete = false }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }
}

@Composable
private fun EditNoteDialog(
    initial: String,
    onSave: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    var note by remember { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.bookmark_edit_note)) },
        text = {
            OutlinedTextField(
                value = note,
                onValueChange = { note = BookmarkFormat.limitNote(it) },
                label = { Text(stringResource(R.string.bookmark_note_label)) },
                supportingText = {
                    Text(stringResource(R.string.bookmark_note_count, BookmarkFormat.noteLength(note), BookmarkFormat.NOTE_MAX))
                },
                modifier = Modifier.fillMaxWidth(),
            )
        },
        confirmButton = {
            TextButton(onClick = { onSave(note) }) { Text(stringResource(R.string.save)) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
        },
    )
}

/** The book's chapter ids in listening order, for [bookmarkSection]. */
internal fun chapterOrderOf(children: List<tv.onscreen.mobile.data.model.ChildItem>): List<String> =
    AudiobookChapters.inOrder(children).map { it.id }
