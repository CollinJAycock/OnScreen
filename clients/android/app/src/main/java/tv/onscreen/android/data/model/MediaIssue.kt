package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * One "Report a problem" report, as its reporter sees it — mirrors the
 * server's IssueResponse (internal/api/v1/issues.go). Returned by
 * GET /api/v1/items/{id}/issues (the caller's own reports on the item,
 * newest first) and POST /api/v1/items/{id}/issues (201, the new report).
 *
 * Every field defaults so an older/newer server shape still parses.
 * Timestamps are RFC 3339 strings.
 */
@JsonClass(generateAdapter = true)
data class MediaIssue(
    val id: String = "",
    val item_id: String = "",
    val file_id: String? = null,
    /** video | audio | subtitles | wrong_match | other */
    val kind: String = "",
    val note: String? = null,
    /** open | resolved | dismissed */
    val status: String = "",
    val created_at: String = "",
    val resolved_at: String? = null,
    val resolution_note: String? = null,
)

/** Body for POST /api/v1/items/{id}/issues. Null fields are omitted by
 *  Moshi, which the server reads as "not given". */
@JsonClass(generateAdapter = true)
data class CreateIssueBody(
    val kind: String,
    val note: String? = null,
    val file_id: String? = null,
)
