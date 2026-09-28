package tv.onscreen.android.ui.common

import retrofit2.HttpException
import tv.onscreen.android.data.api.apiError
import tv.onscreen.android.data.model.MediaIssue
import java.time.OffsetDateTime

/**
 * "Report a problem" — pure helpers behind [ReportProblemDialog]. Server
 * contract: POST/GET /api/v1/items/{id}/issues (internal/api/v1/issues.go).
 * Wording mirrors the web client (web/src/lib/reportProblem.ts) so a report
 * reads the same on every client.
 */
object ReportProblem {

    data class KindOption(val value: String, val label: String)

    /** The kinds in the order the dialog lists them, with user-facing wording. */
    val KIND_OPTIONS: List<KindOption> = listOf(
        KindOption("video", "Video won't play or looks wrong"),
        KindOption("audio", "Audio problem"),
        KindOption("subtitles", "Subtitles problem"),
        KindOption("wrong_match", "Wrong movie/show"),
        KindOption("other", "Something else"),
    )

    /** Must match issueNoteMaxRunes on the server (media_issues note CHECK). */
    const val NOTE_MAX = 1000

    const val ALREADY_REPORTED_TEXT =
        "You already reported this problem — an admin will take a look."

    /** Item types the detail screen offers "Report a problem" on. */
    private val REPORTABLE_TYPES = setOf("movie", "episode", "show")

    fun isReportable(itemType: String?): Boolean = itemType in REPORTABLE_TYPES

    fun kindLabel(kind: String): String =
        KIND_OPTIONS.firstOrNull { it.value == kind }?.label ?: "Something else"

    /** Kinds the caller already has an OPEN report for — the server refuses a
     *  second one with 409 ALREADY_REPORTED, so the dialog disables them. */
    fun openKinds(issues: List<MediaIssue>): Set<String> =
        issues.filter { it.status == "open" }.map { it.kind }.toSet()

    /** RFC 3339 → epoch ms, or null when missing / unparseable. */
    fun parseTime(iso: String?): Long? {
        if (iso.isNullOrBlank()) return null
        return try {
            OffsetDateTime.parse(iso).toInstant().toEpochMilli()
        } catch (_: Exception) {
            null
        }
    }

    /** "just now", "5 minutes ago", "yesterday", "2 days ago", "3 weeks ago". */
    fun relativeAge(iso: String?, nowMs: Long): String {
        val t = parseTime(iso) ?: return ""
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

    /** One line describing an earlier report for the dialog's history block,
     *  e.g. "You reported “Audio problem” 2 days ago. An admin will take a
     *  look." */
    fun describe(issue: MediaIssue, nowMs: Long): String {
        val what = kindLabel(issue.kind)
        if (issue.status == "open") {
            val age = relativeAge(issue.created_at, nowMs)
            val `when` = if (age.isEmpty()) "" else " $age"
            return "You reported “$what”$`when`. An admin will take a look."
        }
        val verb = if (issue.status == "resolved") "Resolved" else "Closed"
        val age = relativeAge(issue.resolved_at ?: issue.created_at, nowMs)
        val note = issue.resolution_note?.takeIf { it.isNotBlank() }?.let { " — “$it”" } ?: ""
        return if (age.isEmpty()) "$verb: “$what”$note" else "$verb $age: “$what”$note"
    }

    /** The earlier reports worth showing: every open one plus closed ones from
     *  the last 30 days, in the server's order (newest first). */
    fun visibleHistory(issues: List<MediaIssue>, nowMs: Long): List<MediaIssue> {
        val cutoff = nowMs - 30L * 86_400_000L
        return issues.filter { i ->
            if (i.status == "open") return@filter true
            val t = parseTime(i.resolved_at ?: i.created_at)
            t != null && t >= cutoff
        }
    }

    /** Is [e] the server saying this kind is already reported (409)? */
    fun isAlreadyReported(e: Throwable): Boolean =
        e is HttpException && e.code() == 409

    /**
     * Map a failed report onto a friendly sentence for a toast. Reads the
     * error envelope's code (single-read body — call once per exception).
     *  - 409 ALREADY_REPORTED — an open report of this kind exists.
     *  - 429 TOO_MANY_OPEN_ISSUES — the per-user open-report cap.
     *  - 429 RATE_LIMITED (or any other 429) — the route's burst limiter.
     */
    fun errorMessage(e: Throwable): String {
        if (e is HttpException) {
            val code = e.apiError()?.code
            return when {
                code == "ALREADY_REPORTED" || e.code() == 409 -> ALREADY_REPORTED_TEXT
                code == "TOO_MANY_OPEN_ISSUES" ->
                    "You have several reports waiting for an admin. Try again once they have been looked at."
                e.code() == 429 ->
                    "Too many reports in a short time. Try again in a minute."
                e.code() == 404 ->
                    "This title is no longer available."
                else -> "Couldn't send the report. Please try again."
            }
        }
        return "Couldn't send the report. Check your connection and try again."
    }
}
