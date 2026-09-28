package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.CreateIssueBody
import tv.onscreen.android.data.model.MediaIssue
import javax.inject.Inject
import javax.inject.Singleton

/**
 * "Report a problem" — POST/GET /api/v1/items/{id}/issues. Failures are
 * thrown as-is (Retrofit's HttpException carries the 409 / 429 error code);
 * the caller maps them to a message — see reportErrorMessage.
 */
@Singleton
open class IssuesRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    /** The caller's own reports on [itemId], newest first. */
    open suspend fun listMine(itemId: String): List<MediaIssue> =
        api.getMyIssues(itemId).data

    /** File a report. A blank [note] is not sent. */
    open suspend fun report(itemId: String, kind: String, note: String?, fileId: String?): MediaIssue =
        api.reportIssue(
            itemId,
            CreateIssueBody(
                kind = kind,
                note = note?.trim()?.takeIf { it.isNotEmpty() },
                file_id = fileId?.takeIf { it.isNotEmpty() },
            ),
        ).data
}
