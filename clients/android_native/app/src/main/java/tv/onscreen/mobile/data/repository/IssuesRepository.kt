package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.CreateIssueRequest
import tv.onscreen.mobile.data.model.MediaIssue
import javax.inject.Inject
import javax.inject.Singleton

/**
 * "Report a problem": a user flags an item whose video / audio / subtitles
 * are broken or whose metadata is the wrong title; admins work the reports
 * on the server's Library health page. Server contract:
 * internal/api/v1/issues.go. Errors propagate as Retrofit HttpExceptions so
 * the caller can branch on the envelope code (ALREADY_REPORTED,
 * TOO_MANY_OPEN_ISSUES, RATE_LIMITED).
 */
@Singleton
open class IssuesRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    /** The caller's own reports on [itemId], newest first. */
    open suspend fun listMine(itemId: String): List<MediaIssue> =
        api.listMyIssues(itemId).data

    /** File a report. [note] is sent trimmed and only when non-blank; [fileId]
     *  names the media file the problem is in, when known. */
    open suspend fun report(
        itemId: String,
        kind: String,
        note: String?,
        fileId: String?,
    ): MediaIssue = api.reportIssue(
        itemId,
        CreateIssueRequest(
            kind = kind,
            note = note?.trim()?.takeIf { it.isNotEmpty() },
            file_id = fileId?.takeIf { it.isNotBlank() },
        ),
    ).data
}
