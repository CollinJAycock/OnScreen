package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.MediaIssue
import tv.onscreen.mobile.data.repository.IssuesRepository

@OptIn(ExperimentalCoroutinesApi::class)
class ReportProblemViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    // 2026-09-28T12:00:00Z
    private val now = 1_790_596_800_000L

    private fun issue(
        id: String,
        kind: String,
        status: String = "open",
        createdAt: String = "2026-09-26T12:00:00Z",
        resolvedAt: String? = null,
        resolutionNote: String? = null,
    ) = MediaIssue(
        id = id, item_id = "movie-1", kind = kind, status = status,
        created_at = createdAt, resolved_at = resolvedAt, resolution_note = resolutionNote,
    )

    private fun httpError(code: Int, body: String) = retrofit2.HttpException(
        retrofit2.Response.error<Any>(code, body.toResponseBody("application/json".toMediaTypeOrNull())),
    )

    private fun vm(repo: IssuesRepository) = ReportProblemViewModel(repo).also { it.clock = { now } }

    @Test
    fun `open loads the caller's reports and disables kinds already open`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine("movie-1") } returns listOf(
            issue("i1", "audio"),
            issue("i2", "video", status = "resolved", resolvedAt = "2026-09-27T12:00:00Z", resolutionNote = "fixed"),
        )
        val vm = vm(repo)
        vm.open("movie-1", "f1")
        assertThat(vm.state.value.loadingHistory).isTrue()
        advanceUntilIdle()

        val ui = vm.state.value
        assertThat(ui.loadingHistory).isFalse()
        assertThat(ui.takenKinds).containsExactly("audio")
        assertThat(ui.shownHistory.map { describeIssue(it, ui.nowMs) }).containsExactly(
            "You reported “Audio problem” 2 days ago. An admin will take a look.",
            "Resolved yesterday: “Video won't play or looks wrong” — “fixed”",
        ).inOrder()

        // A taken kind can't be picked; a free one can.
        vm.selectKind(IssueKind.AUDIO)
        assertThat(vm.state.value.kind).isNull()
        assertThat(vm.state.value.canSubmit).isFalse()
        vm.selectKind(IssueKind.SUBTITLES)
        assertThat(vm.state.value.canSubmit).isTrue()
    }

    @Test
    fun `a failed history load still lets the user report`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine(any()) } throws java.io.IOException("offline")
        val vm = vm(repo)
        vm.open("movie-1", null)
        advanceUntilIdle()
        vm.selectKind(IssueKind.OTHER)

        assertThat(vm.state.value.history).isEmpty()
        assertThat(vm.state.value.canSubmit).isTrue()
    }

    @Test
    fun `submit sends kind, trimmed note and file id, then shows the sent state`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine(any()) } returns emptyList()
        coEvery { repo.report("movie-1", "subtitles", any(), "f1") } returns issue("new", "subtitles")
        val vm = vm(repo)
        vm.open("movie-1", "f1")
        advanceUntilIdle()
        vm.selectKind(IssueKind.SUBTITLES)
        vm.setNote("  out of sync at 12:00  ")
        vm.submit()
        assertThat(vm.state.value.submitting).isTrue()
        advanceUntilIdle()

        val ui = vm.state.value
        assertThat(ui.sent).isTrue()
        assertThat(ui.submitting).isFalse()
        assertThat(ui.takenKinds).contains("subtitles")
        assertThat(ui.canSubmit).isFalse()
        coVerify(exactly = 1) { repo.report("movie-1", "subtitles", "  out of sync at 12:00  ", "f1") }
    }

    @Test
    fun `409 ALREADY_REPORTED shows a snackbar message and refreshes the history`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine(any()) } returnsMany listOf(emptyList<MediaIssue>(), listOf(issue("x", "video")))
        coEvery { repo.report(any(), any(), any(), any()) } throws httpError(
            409, """{"error":{"code":"ALREADY_REPORTED","message":"you already have an open report"}}""",
        )
        val vm = vm(repo)
        vm.open("movie-1", null)
        advanceUntilIdle()
        vm.selectKind(IssueKind.VIDEO)
        vm.submit()
        advanceUntilIdle()

        assertThat(vm.messages.first())
            .isEqualTo("You already reported this problem — an admin will take a look.")
        val ui = vm.state.value
        assertThat(ui.sent).isFalse()
        assertThat(ui.submitting).isFalse()
        assertThat(ui.takenKinds).containsExactly("video")
        // The refreshed history shows the kind as taken, so the pick is cleared.
        assertThat(ui.kind).isNull()
        coVerify(exactly = 2) { repo.listMine("movie-1") }
    }

    @Test
    fun `429 TOO_MANY_OPEN_ISSUES shows a snackbar message`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine(any()) } returns emptyList()
        coEvery { repo.report(any(), any(), any(), any()) } throws httpError(
            429, """{"error":{"code":"TOO_MANY_OPEN_ISSUES","message":"you have 10 open reports"}}""",
        )
        val vm = vm(repo)
        vm.open("movie-1", null)
        advanceUntilIdle()
        vm.selectKind(IssueKind.AUDIO)
        vm.submit()
        advanceUntilIdle()

        assertThat(vm.messages.first()).isEqualTo(
            "You have several reports waiting for an admin. Try again once they have been looked at.",
        )
        assertThat(vm.state.value.sent).isFalse()
        // Still selectable for a later retry.
        assertThat(vm.state.value.canSubmit).isTrue()
    }

    @Test
    fun `429 RATE_LIMITED from the route limiter shows a snackbar message`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine(any()) } returns emptyList()
        coEvery { repo.report(any(), any(), any(), any()) } throws httpError(
            429, """{"error":{"code":"RATE_LIMITED","message":"rate limit exceeded"}}""",
        )
        val vm = vm(repo)
        vm.open("movie-1", null)
        advanceUntilIdle()
        vm.selectKind(IssueKind.OTHER)
        vm.submit()
        advanceUntilIdle()

        assertThat(vm.messages.first()).isEqualTo("Too many reports in a short time. Try again in a minute.")
    }

    @Test
    fun `a note over the server limit blocks sending`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine(any()) } returns emptyList()
        val vm = vm(repo)
        vm.open("movie-1", null)
        advanceUntilIdle()
        vm.selectKind(IssueKind.OTHER)
        vm.setNote("x".repeat(ISSUE_NOTE_MAX + 1))
        assertThat(vm.state.value.remaining).isEqualTo(-1)
        assertThat(vm.state.value.canSubmit).isFalse()

        vm.setNote("x".repeat(ISSUE_NOTE_MAX))
        assertThat(vm.state.value.canSubmit).isTrue()
    }

    @Test
    fun `reopening for another item resets the sheet`() = runTest(dispatcher) {
        val repo = mockk<IssuesRepository>()
        coEvery { repo.listMine(any()) } returns emptyList()
        coEvery { repo.report(any(), any(), any(), any()) } returns issue("n", "video")
        val vm = vm(repo)
        vm.open("movie-1", null)
        advanceUntilIdle()
        vm.selectKind(IssueKind.VIDEO)
        vm.submit()
        advanceUntilIdle()
        assertThat(vm.state.value.sent).isTrue()

        vm.open("movie-2", null)
        advanceUntilIdle()
        val ui = vm.state.value
        assertThat(ui.itemId).isEqualTo("movie-2")
        assertThat(ui.sent).isFalse()
        assertThat(ui.kind).isNull()
        assertThat(ui.history).isEmpty()
    }

    // ── Pure helpers ────────────────────────────────────────────────────

    @Test
    fun `error messages map the server codes`() {
        assertThat(reportErrorMessage(404, "NOT_FOUND", "not found")).isEqualTo("This title is no longer available.")
        assertThat(reportErrorMessage(422, "VALIDATION", "note must be at most 1000 characters"))
            .isEqualTo("Couldn't send the report: note must be at most 1000 characters")
        assertThat(reportErrorMessage(null, null, null)).isEqualTo("Couldn't send the report.")
    }

    @Test
    fun `RFC 3339 timestamps parse as Go marshals them`() {
        assertThat(parseRfc3339Millis("2026-09-28T12:00:00Z")).isEqualTo(now)
        assertThat(parseRfc3339Millis("2026-09-28T12:00:00.123456789Z")).isEqualTo(now + 123)
        assertThat(parseRfc3339Millis("2026-09-28T14:00:00+02:00")).isEqualTo(now)
        assertThat(parseRfc3339Millis("2026-09-28T07:30:00.5-04:30")).isEqualTo(now + 500)
        assertThat(parseRfc3339Millis("yesterday")).isNull()
        assertThat(parseRfc3339Millis(null)).isNull()
    }

    @Test
    fun `relative ages read like the web client`() {
        fun ago(ms: Long) = relativeAge(
            java.text.SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", java.util.Locale.US)
                .apply { timeZone = java.util.TimeZone.getTimeZone("UTC") }
                .format(java.util.Date(now - ms)),
            now,
        )
        assertThat(ago(10_000)).isEqualTo("just now")
        assertThat(ago(60_000)).isEqualTo("1 minute ago")
        assertThat(ago(5 * 60_000)).isEqualTo("5 minutes ago")
        assertThat(ago(3 * 3_600_000L)).isEqualTo("3 hours ago")
        assertThat(ago(30 * 3_600_000L)).isEqualTo("yesterday")
        assertThat(ago(5 * 86_400_000L)).isEqualTo("5 days ago")
        assertThat(ago(21 * 86_400_000L)).isEqualTo("3 weeks ago")
        assertThat(ago(90 * 86_400_000L)).isEqualTo("3 months ago")
        assertThat(relativeAge(null, now)).isEmpty()
    }

    @Test
    fun `history shows open reports and recently closed ones only`() {
        val shown = visibleHistory(
            listOf(
                issue("open-old", "video", createdAt = "2025-01-01T00:00:00Z"),
                issue("closed-recent", "audio", status = "dismissed", resolvedAt = "2026-09-20T00:00:00Z"),
                issue("closed-old", "other", status = "resolved", resolvedAt = "2026-07-01T00:00:00Z"),
            ),
            now,
        )
        assertThat(shown.map { it.id }).containsExactly("open-old", "closed-recent").inOrder()
        assertThat(describeIssue(shown[1], now)).isEqualTo("Closed 8 days ago: “Audio problem”")
    }

    @Test
    fun `note length counts code points like the server`() {
        assertThat(noteRemaining("  hi  ")).isEqualTo(ISSUE_NOTE_MAX - 2)
        // One emoji = one character server-side (char_length), two UTF-16 units here.
        assertThat(noteRemaining("😀")).isEqualTo(ISSUE_NOTE_MAX - 1)
    }

    @Test
    fun `kind labels match the web wording and unknown kinds read as something else`() {
        assertThat(IssueKind.entries.map { it.wire })
            .containsExactly("video", "audio", "subtitles", "wrong_match", "other").inOrder()
        assertThat(issueKindLabel("wrong_match")).isEqualTo("Wrong movie/show")
        assertThat(issueKindLabel("future_kind")).isEqualTo("Something else")
    }
}
