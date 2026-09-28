package tv.onscreen.mobile.ui.item

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
import tv.onscreen.mobile.data.api.apiError
import tv.onscreen.mobile.data.model.MediaIssue
import tv.onscreen.mobile.data.repository.IssuesRepository
import java.util.Calendar
import java.util.TimeZone
import javax.inject.Inject

// "Report a problem" — the phone side of POST/GET /api/v1/items/{id}/issues
// (internal/api/v1/issues.go). Wording and rules mirror the web client's
// web/src/lib/reportProblem.ts so the two read the same.

/** Report kinds in the order the sheet lists them, with user-facing wording. */
enum class IssueKind(val wire: String, val label: String) {
    VIDEO("video", "Video won't play or looks wrong"),
    AUDIO("audio", "Audio problem"),
    SUBTITLES("subtitles", "Subtitles problem"),
    WRONG_MATCH("wrong_match", "Wrong movie/show"),
    OTHER("other", "Something else"),
    ;

    companion object {
        fun fromWire(kind: String): IssueKind? = entries.firstOrNull { it.wire == kind }
    }
}

/** Must match issueNoteMaxRunes on the server (media_issues note CHECK). */
const val ISSUE_NOTE_MAX = 1000

/** Item types the sheet is offered on — the same set the web item page uses. */
val REPORTABLE_TYPES = setOf("movie", "episode", "show", "season")

fun issueKindLabel(kind: String): String = IssueKind.fromWire(kind)?.label ?: IssueKind.OTHER.label

/** Characters left in a note. Counts code points like the server's
 *  char_length, so an emoji costs one, not two. */
fun noteRemaining(note: String): Int {
    val t = note.trim()
    return ISSUE_NOTE_MAX - t.codePointCount(0, t.length)
}

/** Kinds the caller already has an open report for (a second one is refused
 *  with 409 ALREADY_REPORTED). */
fun openKinds(issues: List<MediaIssue>): Set<String> =
    issues.filter { it.status == "open" }.map { it.kind }.toSet()

/** Earlier reports worth showing: every open one plus closed ones from the
 *  last 30 days, in server order (newest first). */
fun visibleHistory(issues: List<MediaIssue>, nowMs: Long): List<MediaIssue> {
    val cutoff = nowMs - 30L * 86_400_000L
    return issues.filter { i ->
        if (i.status == "open") return@filter true
        val t = parseRfc3339Millis(i.resolved_at ?: i.created_at) ?: return@filter false
        t >= cutoff
    }
}

/** One line describing an earlier report, for the sheet's history block. */
fun describeIssue(issue: MediaIssue, nowMs: Long): String {
    val what = issueKindLabel(issue.kind)
    if (issue.status == "open") {
        val age = relativeAge(issue.created_at, nowMs)
        return "You reported “$what”${if (age.isEmpty()) "" else " $age"}. An admin will take a look."
    }
    val verb = if (issue.status == "resolved") "Resolved" else "Closed"
    val age = relativeAge(issue.resolved_at ?: issue.created_at, nowMs)
    val note = issue.resolution_note?.takeIf { it.isNotBlank() }?.let { " — “$it”" }.orEmpty()
    return "$verb${if (age.isEmpty()) "" else " $age"}: “$what”$note"
}

/** "just now", "5 minutes ago", "yesterday", "2 days ago", "3 weeks ago". */
fun relativeAge(iso: String?, nowMs: Long): String {
    val t = parseRfc3339Millis(iso) ?: return ""
    val mins = maxOf(0L, (nowMs - t) / 60_000L)
    if (mins < 1) return "just now"
    if (mins < 60) return "$mins minute${if (mins == 1L) "" else "s"} ago"
    val hrs = mins / 60
    if (hrs < 24) return "$hrs hour${if (hrs == 1L) "" else "s"} ago"
    val days = hrs / 24
    if (days == 1L) return "yesterday"
    if (days < 14) return "$days days ago"
    if (days < 60) return "${days / 7} weeks ago"
    val months = days / 30
    if (months < 12) return "$months months ago"
    val years = days / 365
    return "$years year${if (years == 1L) "" else "s"} ago"
}

private val RFC3339 = Regex(
    """^(\d{4})-(\d{2})-(\d{2})[Tt ](\d{2}):(\d{2}):(\d{2})(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$""",
)

/** Epoch millis of an RFC 3339 timestamp as Go marshals time.Time
 *  ("2026-09-28T12:34:56.123456789Z", "…+02:00"); null when unparseable.
 *  Hand-rolled because java.time needs API 26 and minSdk is 24. */
fun parseRfc3339Millis(iso: String?): Long? {
    val m = RFC3339.matchEntire(iso?.trim() ?: return null) ?: return null
    val g = m.groupValues
    val cal = Calendar.getInstance(TimeZone.getTimeZone("UTC")).apply {
        clear()
        set(g[1].toInt(), g[2].toInt() - 1, g[3].toInt(), g[4].toInt(), g[5].toInt(), g[6].toInt())
    }
    val fracMs = g[7].takeIf { it.isNotEmpty() }?.drop(1)?.padEnd(3, '0')?.take(3)?.toLong() ?: 0L
    val zone = g[8]
    val offsetMs = if (zone.equals("Z", ignoreCase = true)) {
        0L
    } else {
        val sign = if (zone[0] == '-') -1 else 1
        sign * (zone.substring(1, 3).toLong() * 60 + zone.substring(4, 6).toLong()) * 60_000L
    }
    return cal.timeInMillis + fracMs - offsetMs
}

/** Sentence for a failed report, keyed on the server's envelope code. */
fun reportErrorMessage(status: Int?, code: String?, message: String?): String = when (code) {
    "ALREADY_REPORTED" -> "You already reported this problem — an admin will take a look."
    "TOO_MANY_OPEN_ISSUES" ->
        "You have several reports waiting for an admin. Try again once they have been looked at."
    "RATE_LIMITED" -> "Too many reports in a short time. Try again in a minute."
    else -> when {
        status == 404 -> "This title is no longer available."
        !message.isNullOrBlank() -> "Couldn't send the report: $message"
        else -> "Couldn't send the report."
    }
}

data class ReportProblemUi(
    val itemId: String? = null,
    val fileId: String? = null,
    val loadingHistory: Boolean = false,
    val history: List<MediaIssue> = emptyList(),
    val kind: IssueKind? = null,
    val note: String = "",
    val submitting: Boolean = false,
    val sent: Boolean = false,
    /** Wall clock the history ages are rendered against. */
    val nowMs: Long = 0L,
) {
    val takenKinds: Set<String> get() = openKinds(history)
    val shownHistory: List<MediaIssue> get() = visibleHistory(history, nowMs)
    val remaining: Int get() = noteRemaining(note)
    val canSubmit: Boolean
        get() = itemId != null && kind != null && kind.wire !in takenKinds &&
            !submitting && !sent && remaining >= 0
}

/**
 * State for the "Report a problem" bottom sheet. [open] resets it for an
 * item and loads the caller's earlier reports (a courtesy — a failed load
 * leaves the history empty and the report can still be sent); [submit] files
 * the report. Failures the user must act on (409 ALREADY_REPORTED, 429
 * TOO_MANY_OPEN_ISSUES / RATE_LIMITED, …) arrive once on [messages] for a
 * snackbar.
 */
@HiltViewModel
class ReportProblemViewModel @Inject constructor(
    private val repo: IssuesRepository,
) : ViewModel() {

    /** Injectable clock so tests can pin the history ages. */
    internal var clock: () -> Long = System::currentTimeMillis

    private val _state = MutableStateFlow(ReportProblemUi())
    val state: StateFlow<ReportProblemUi> = _state.asStateFlow()

    private val _messages = Channel<String>(Channel.BUFFERED)
    val messages: Flow<String> = _messages.receiveAsFlow()

    private var historyJob: Job? = null

    fun open(itemId: String, fileId: String?) {
        _state.value = ReportProblemUi(itemId = itemId, fileId = fileId, nowMs = clock())
        loadHistory()
    }

    fun selectKind(kind: IssueKind) {
        val s = _state.value
        if (s.sent || kind.wire in s.takenKinds) return
        _state.value = s.copy(kind = kind)
    }

    fun setNote(note: String) {
        _state.value = _state.value.copy(note = note)
    }

    fun submit() {
        val s = _state.value
        if (!s.canSubmit) return
        val itemId = s.itemId ?: return
        val kind = s.kind ?: return
        _state.value = s.copy(submitting = true)
        viewModelScope.launch {
            try {
                val created = repo.report(itemId, kind.wire, s.note, s.fileId)
                val cur = _state.value
                if (cur.itemId != itemId) return@launch
                _state.value = cur.copy(
                    submitting = false,
                    sent = true,
                    history = listOf(created) + cur.history,
                    nowMs = clock(),
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                val http = e as? HttpException
                val err = http?.apiError()
                if (_state.value.itemId != itemId) return@launch
                _state.value = _state.value.copy(submitting = false)
                _messages.trySend(reportErrorMessage(http?.code(), err?.code, err?.message ?: e.message))
                // Filed elsewhere meanwhile (another device): refresh so the
                // kind shows as already reported.
                if (err?.code == "ALREADY_REPORTED") loadHistory()
            }
        }
    }

    private fun loadHistory() {
        val itemId = _state.value.itemId ?: return
        historyJob?.cancel()
        _state.value = _state.value.copy(loadingHistory = true)
        historyJob = viewModelScope.launch {
            val list = try {
                repo.listMine(itemId)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                emptyList()
            }
            val cur = _state.value
            if (cur.itemId != itemId) return@launch
            // A kind the user picked that turns out to be already reported
            // is cleared so the Send button can't fire a certain 409.
            val taken = openKinds(list)
            _state.value = cur.copy(
                loadingHistory = false,
                history = list,
                nowMs = clock(),
                kind = cur.kind?.takeUnless { it.wire in taken },
            )
        }
    }
}
