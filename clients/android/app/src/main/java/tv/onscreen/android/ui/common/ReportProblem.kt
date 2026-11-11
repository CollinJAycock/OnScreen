package tv.onscreen.android.ui.common

import androidx.annotation.StringRes
import retrofit2.HttpException
import tv.onscreen.android.R
import tv.onscreen.android.data.api.apiError
import tv.onscreen.android.data.model.MediaIssue
import tv.onscreen.android.i18n.UiText
import java.time.OffsetDateTime

/**
 * "Report a problem" — pure helpers behind [ReportProblemDialog]. Server
 * contract: POST/GET /api/v1/items/{id}/issues (internal/api/v1/issues.go).
 * Wording mirrors the web client (web/src/lib/reportProblem.ts) so a report
 * reads the same on every client. The words are string resources, handed out
 * as resource ids and [UiText] for the dialog to resolve.
 */
object ReportProblem {

    data class KindOption(val value: String, @param:StringRes val label: Int)

    /** The kinds in the order the dialog lists them, with user-facing wording. */
    val KIND_OPTIONS: List<KindOption> = listOf(
        KindOption("video", R.string.report_kind_video),
        KindOption("audio", R.string.report_kind_audio),
        KindOption("subtitles", R.string.report_kind_subtitles),
        KindOption("wrong_match", R.string.report_kind_wrong_match),
        KindOption("other", R.string.report_kind_other),
    )

    /** Must match issueNoteMaxRunes on the server (media_issues note CHECK). */
    const val NOTE_MAX = 1000

    /** "You already reported this problem — an admin will take a look." */
    val ALREADY_REPORTED: UiText = UiText.Res(R.string.report_already_reported)

    /** Item types the detail screen offers "Report a problem" on. */
    private val REPORTABLE_TYPES = setOf("movie", "episode", "show")

    fun isReportable(itemType: String?): Boolean = itemType in REPORTABLE_TYPES

    @StringRes
    fun kindLabel(kind: String): Int =
        KIND_OPTIONS.firstOrNull { it.value == kind }?.label ?: R.string.report_kind_other

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

    /** "just now", "5 minutes ago", "yesterday", "2 days ago", "3 weeks ago";
     *  null when [iso] is missing or unparseable. */
    fun relativeAge(iso: String?, nowMs: Long): UiText? {
        val t = parseTime(iso) ?: return null
        val mins = maxOf(0L, (nowMs - t) / 60_000L)
        if (mins < 1) return UiText.Res(R.string.age_just_now)
        if (mins < 60) return UiText.Plural(R.plurals.age_minutes, mins.toInt())
        val hrs = mins / 60
        if (hrs < 24) return UiText.Plural(R.plurals.age_hours, hrs.toInt())
        val days = hrs / 24
        if (days == 1L) return UiText.Res(R.string.age_yesterday)
        if (days < 14) return UiText.Plural(R.plurals.age_days, days.toInt())
        if (days < 60) return UiText.Plural(R.plurals.age_weeks, (days / 7).toInt())
        val months = days / 30
        if (months < 12) return UiText.Plural(R.plurals.age_months, months.toInt())
        val years = days / 365
        return UiText.Plural(R.plurals.age_years, years.toInt())
    }

    /** One line describing an earlier report for the dialog's history block,
     *  e.g. "You reported “Audio problem” 2 days ago. An admin will take a
     *  look." Whole sentences per case (with or without an age, a note). */
    fun describe(issue: MediaIssue, nowMs: Long): UiText {
        val what = UiText.Res(kindLabel(issue.kind))
        if (issue.status == "open") {
            val age = relativeAge(issue.created_at, nowMs)
                ?: return UiText.Res(R.string.report_history_open, what)
            return UiText.Res(R.string.report_history_open_age, what, age)
        }
        val resolved = issue.status == "resolved"
        val age = relativeAge(issue.resolved_at ?: issue.created_at, nowMs)
        val note = issue.resolution_note?.takeIf { it.isNotBlank() }
        return when {
            age != null && note != null -> UiText.Res(
                if (resolved) R.string.report_history_resolved_age_note else R.string.report_history_closed_age_note,
                age, what, note,
            )
            age != null -> UiText.Res(
                if (resolved) R.string.report_history_resolved_age else R.string.report_history_closed_age,
                age, what,
            )
            note != null -> UiText.Res(
                if (resolved) R.string.report_history_resolved_note else R.string.report_history_closed_note,
                what, note,
            )
            else -> UiText.Res(if (resolved) R.string.report_history_resolved else R.string.report_history_closed, what)
        }
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
    fun errorMessage(e: Throwable): UiText {
        if (e is HttpException) {
            val code = e.apiError()?.code
            return when {
                code == "ALREADY_REPORTED" || e.code() == 409 -> ALREADY_REPORTED
                code == "TOO_MANY_OPEN_ISSUES" -> UiText.Res(R.string.report_error_too_many_open)
                e.code() == 429 -> UiText.Res(R.string.report_error_rate_limited)
                e.code() == 404 -> UiText.Res(R.string.report_error_gone)
                else -> UiText.Res(R.string.report_error_failed)
            }
        }
        return UiText.Res(R.string.report_error_offline)
    }
}
