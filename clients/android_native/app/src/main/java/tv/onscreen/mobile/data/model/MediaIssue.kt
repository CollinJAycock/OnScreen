package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * One "Report a problem" report as its reporter sees it — the server's
 * IssueResponse (internal/api/v1/issues.go). Returned by
 * GET /api/v1/items/{id}/issues (the caller's own reports, newest first) and
 * POST /api/v1/items/{id}/issues (the new report).
 *
 * Times are RFC 3339 strings (Go time.Time); see
 * [tv.onscreen.mobile.ui.item.parseRfc3339Millis].
 */
@JsonClass(generateAdapter = true)
data class MediaIssue(
    val id: String,
    val item_id: String = "",
    val file_id: String? = null,
    /** video | audio | subtitles | wrong_match | other */
    val kind: String,
    val note: String? = null,
    /** open | resolved | dismissed */
    val status: String = "open",
    val created_at: String? = null,
    val resolved_at: String? = null,
    val resolution_note: String? = null,
)

/** Body for POST /api/v1/items/{id}/issues. Null fields are omitted on the
 *  wire (Moshi's default), so an empty note / unknown file is simply absent. */
@JsonClass(generateAdapter = true)
data class CreateIssueRequest(
    val kind: String,
    val note: String? = null,
    val file_id: String? = null,
)
