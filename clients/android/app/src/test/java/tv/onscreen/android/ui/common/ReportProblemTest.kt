package tv.onscreen.android.ui.common

import com.google.common.truth.Truth.assertThat
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.model.MediaIssue
import tv.onscreen.android.i18n.AppLanguage
import tv.onscreen.android.i18n.TestStrings
import tv.onscreen.android.i18n.UiText
import java.time.OffsetDateTime

/** Pure "Report a problem" helpers — wording kept in step with
 *  web/src/lib/reportProblem.ts. The text is resolved from the string
 *  resources (TestStrings), so the English is checked word for word. */
class ReportProblemTest {

    private fun en(text: UiText?): String = text?.let { TestStrings.resolve(it) }.orEmpty()
    private fun es(text: UiText): String = TestStrings.resolve(text, AppLanguage.SPANISH)
    private fun zh(text: UiText): String = TestStrings.resolve(text, AppLanguage.CHINESE)
    private fun label(id: Int): String = TestStrings.string(id)

    private val now = OffsetDateTime.parse("2026-09-28T12:00:00Z").toInstant().toEpochMilli()

    private fun issue(
        kind: String,
        status: String = "open",
        created: String = "2026-09-26T11:00:00Z",
        resolved: String? = null,
        note: String? = null,
    ) = MediaIssue(
        id = "i-$kind-$status", item_id = "item", kind = kind, status = status,
        created_at = created, resolved_at = resolved, resolution_note = note,
    )

    private fun http(code: Int, body: String = "") =
        HttpException(Response.error<Unit>(code, body.toResponseBody(null)))

    @Test
    fun `lists the five kinds in order with friendly labels`() {
        assertThat(ReportProblem.KIND_OPTIONS.map { it.value })
            .containsExactly("video", "audio", "subtitles", "wrong_match", "other").inOrder()
        assertThat(ReportProblem.KIND_OPTIONS.map { label(it.label) }).containsExactly(
            "Video won't play or looks wrong", "Audio problem", "Subtitles problem",
            "Wrong movie/show", "Something else",
        ).inOrder()
        assertThat(label(ReportProblem.kindLabel("nope"))).isEqualTo("Something else")
    }

    @Test
    fun `reportable only on movies, episodes and shows`() {
        assertThat(ReportProblem.isReportable("movie")).isTrue()
        assertThat(ReportProblem.isReportable("episode")).isTrue()
        assertThat(ReportProblem.isReportable("show")).isTrue()
        for (t in listOf("season", "track", "album", "artist", "photo", "audiobook", "", null)) {
            assertThat(ReportProblem.isReportable(t)).isFalse()
        }
    }

    @Test
    fun `open kinds are only the open reports`() {
        val list = listOf(issue("audio"), issue("video", status = "resolved"), issue("other", status = "dismissed"))
        assertThat(ReportProblem.openKinds(list)).containsExactly("audio")
    }

    @Test
    fun `relative ages`() {
        assertThat(en(ReportProblem.relativeAge("2026-09-28T11:59:40Z", now))).isEqualTo("just now")
        assertThat(en(ReportProblem.relativeAge("2026-09-28T11:59:00Z", now))).isEqualTo("1 minute ago")
        assertThat(en(ReportProblem.relativeAge("2026-09-28T11:55:00Z", now))).isEqualTo("5 minutes ago")
        assertThat(en(ReportProblem.relativeAge("2026-09-28T11:00:00Z", now))).isEqualTo("1 hour ago")
        assertThat(en(ReportProblem.relativeAge("2026-09-28T09:00:00Z", now))).isEqualTo("3 hours ago")
        assertThat(en(ReportProblem.relativeAge("2026-09-27T11:00:00Z", now))).isEqualTo("yesterday")
        assertThat(en(ReportProblem.relativeAge("2026-09-26T11:00:00Z", now))).isEqualTo("2 days ago")
        assertThat(en(ReportProblem.relativeAge("2026-09-07T12:00:00Z", now))).isEqualTo("3 weeks ago")
        assertThat(en(ReportProblem.relativeAge("2026-06-28T12:00:00Z", now))).isEqualTo("3 months ago")
        assertThat(en(ReportProblem.relativeAge("2025-09-28T12:00:00Z", now))).isEqualTo("1 year ago")
        assertThat(en(ReportProblem.relativeAge("2023-09-28T12:00:00Z", now))).isEqualTo("3 years ago")
        // Offsets and fractional seconds (Go's RFC 3339 nano) parse too.
        assertThat(en(ReportProblem.relativeAge("2026-09-26T13:00:00.123456+02:00", now))).isEqualTo("2 days ago")
        assertThat(ReportProblem.relativeAge("garbage", now)).isNull()
        assertThat(ReportProblem.relativeAge(null, now)).isNull()
    }

    @Test
    fun `relative ages in Spanish and Chinese use their plural forms`() {
        val oneMinute = ReportProblem.relativeAge("2026-09-28T11:59:00Z", now)!!
        val fiveMinutes = ReportProblem.relativeAge("2026-09-28T11:55:00Z", now)!!
        assertThat(es(oneMinute)).isEqualTo("hace 1 minuto")
        assertThat(es(fiveMinutes)).isEqualTo("hace 5 minutos")
        assertThat(zh(oneMinute)).isEqualTo("1 分钟前")
        assertThat(zh(fiveMinutes)).isEqualTo("5 分钟前")
        assertThat(es(ReportProblem.relativeAge("2026-09-27T11:00:00Z", now)!!)).isEqualTo("ayer")
        assertThat(zh(ReportProblem.relativeAge("2026-09-28T11:59:40Z", now)!!)).isEqualTo("刚刚")
    }

    @Test
    fun `describes open and closed reports`() {
        assertThat(en(ReportProblem.describe(issue("audio"), now)))
            .isEqualTo("You reported “Audio problem” 2 days ago. An admin will take a look.")
        assertThat(en(ReportProblem.describe(issue("audio", created = ""), now)))
            .isEqualTo("You reported “Audio problem”. An admin will take a look.")
        assertThat(
            en(
                ReportProblem.describe(
                    issue("video", status = "resolved", resolved = "2026-09-27T11:00:00Z", note = "Replaced the file"),
                    now,
                ),
            ),
        ).isEqualTo("Resolved yesterday: “Video won't play or looks wrong” — “Replaced the file”")
        assertThat(en(ReportProblem.describe(issue("video", status = "resolved", resolved = "2026-09-27T11:00:00Z"), now)))
            .isEqualTo("Resolved yesterday: “Video won't play or looks wrong”")
        assertThat(en(ReportProblem.describe(issue("other", status = "dismissed", created = ""), now)))
            .isEqualTo("Closed: “Something else”")
        assertThat(en(ReportProblem.describe(issue("other", status = "dismissed", created = "", note = "Dupe"), now)))
            .isEqualTo("Closed: “Something else” — “Dupe”")
        assertThat(en(ReportProblem.describe(issue("other", status = "dismissed", note = "Dupe"), now)))
            .isEqualTo("Closed 2 days ago: “Something else” — “Dupe”")
    }

    @Test
    fun `describes a report in Spanish and Chinese`() {
        val open = ReportProblem.describe(issue("audio"), now)
        assertThat(es(open)).isEqualTo("Informaste de “Problema de audio” hace 2 días. Un administrador lo revisará.")
        assertThat(zh(open)).isEqualTo("你报告了“音频问题”（2 天前）。管理员会查看。")
        val resolved = ReportProblem.describe(
            issue("video", status = "resolved", resolved = "2026-09-27T11:00:00Z", note = "Cambiado"),
            now,
        )
        assertThat(es(resolved)).isEqualTo("Resuelto ayer: “El vídeo no se reproduce o se ve mal” (“Cambiado”)")
    }

    @Test
    fun `history keeps open reports and closed ones from the last 30 days`() {
        val open = issue("audio", created = "2025-01-01T00:00:00Z")
        val recent = issue("video", status = "resolved", resolved = "2026-09-20T00:00:00Z")
        val old = issue("other", status = "dismissed", resolved = "2026-07-01T00:00:00Z")
        assertThat(ReportProblem.visibleHistory(listOf(open, recent, old), now))
            .containsExactly(open, recent).inOrder()
    }

    @Test
    fun `409 and 429 map to friendly messages`() {
        assertThat(
            en(
                ReportProblem.errorMessage(
                    http(409, """{"error":{"code":"ALREADY_REPORTED","message":"you already have an open report"}}"""),
                ),
            ),
        ).isEqualTo("You already reported this problem — an admin will take a look.")
        assertThat(
            en(
                ReportProblem.errorMessage(
                    http(429, """{"error":{"code":"TOO_MANY_OPEN_ISSUES","message":"you have 10 open reports"}}"""),
                ),
            ),
        ).isEqualTo("You have several reports waiting for an admin. Try again once they have been looked at.")
        assertThat(
            en(ReportProblem.errorMessage(http(429, """{"error":{"code":"RATE_LIMITED","message":"rate limit exceeded"}}"""))),
        ).isEqualTo("Too many reports in a short time. Try again in a minute.")
        assertThat(en(ReportProblem.errorMessage(http(429)))).isEqualTo("Too many reports in a short time. Try again in a minute.")
        assertThat(en(ReportProblem.errorMessage(http(404)))).isEqualTo("This title is no longer available.")
        assertThat(en(ReportProblem.errorMessage(http(500)))).isEqualTo("Couldn't send the report. Please try again.")
        assertThat(en(ReportProblem.errorMessage(java.io.IOException("offline"))))
            .isEqualTo("Couldn't send the report. Check your connection and try again.")
    }

    @Test
    fun `409 is recognised as already reported`() {
        assertThat(ReportProblem.isAlreadyReported(http(409))).isTrue()
        assertThat(ReportProblem.isAlreadyReported(http(429))).isFalse()
        assertThat(ReportProblem.isAlreadyReported(RuntimeException())).isFalse()
    }
}
