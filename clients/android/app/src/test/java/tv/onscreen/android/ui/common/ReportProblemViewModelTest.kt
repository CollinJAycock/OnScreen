package tv.onscreen.android.ui.common

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.model.MediaIssue
import tv.onscreen.android.data.repository.IssuesRepository

@OptIn(ExperimentalCoroutinesApi::class)
class ReportProblemViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    private fun issue(kind: String, status: String = "open", id: String = "i-$kind") = MediaIssue(
        id = id, item_id = "m1", kind = kind, status = status, created_at = "2026-09-26T11:00:00Z",
    )

    private fun http(code: Int, body: String) =
        HttpException(Response.error<Unit>(code, body.toResponseBody(null)))

    private fun repo(history: List<MediaIssue> = emptyList()): IssuesRepository {
        val r = mockk<IssuesRepository>()
        coEvery { r.listMine("m1") } returns history
        return r
    }

    @Test
    fun `start loads the caller's earlier reports`() = runTest(dispatcher) {
        val r = repo(listOf(issue("audio")))
        val vm = ReportProblemViewModel(r)
        vm.start("m1", "f1")
        assertThat(vm.uiState.value.loadingHistory).isTrue()
        advanceUntilIdle()

        assertThat(vm.uiState.value.loadingHistory).isFalse()
        assertThat(vm.uiState.value.history.map { it.kind }).containsExactly("audio")
        // Idempotent for the same item (dialog recreation).
        vm.start("m1", "f1")
        advanceUntilIdle()
        coVerify(exactly = 1) { r.listMine("m1") }
    }

    @Test
    fun `a failed history load still lets the user report`() = runTest(dispatcher) {
        val r = mockk<IssuesRepository>()
        coEvery { r.listMine("m1") } throws java.io.IOException("offline")
        val vm = ReportProblemViewModel(r)
        vm.start("m1", null)
        advanceUntilIdle()

        assertThat(vm.uiState.value.loadingHistory).isFalse()
        assertThat(vm.uiState.value.history).isEmpty()
        vm.pickKind("video")
        assertThat(vm.uiState.value.selectedKind).isEqualTo("video")
    }

    @Test
    fun `picking a kind with an open report toasts instead of advancing`() = runTest(dispatcher) {
        val vm = ReportProblemViewModel(
            repo(listOf(issue("subtitles"), issue("audio", status = "resolved", id = "old"))),
        )
        vm.start("m1", null)
        advanceUntilIdle()

        vm.pickKind("subtitles")
        assertThat(vm.uiState.value.selectedKind).isNull()
        assertThat(vm.uiState.value.toast).isEqualTo(ReportProblem.ALREADY_REPORTED_TEXT)
        vm.toastShown()
        assertThat(vm.uiState.value.toast).isNull()

        // A resolved report of a kind doesn't block a new one.
        vm.pickKind("audio")
        assertThat(vm.uiState.value.selectedKind).isEqualTo("audio")
        vm.backToKinds()
        assertThat(vm.uiState.value.selectedKind).isNull()
    }

    @Test
    fun `submit sends kind, trimmed note and file id, then shows the thank-you`() = runTest(dispatcher) {
        val r = repo()
        val created = issue("wrong_match", id = "new")
        coEvery { r.report("m1", "wrong_match", "It's the 1998 one", "f1") } returns created
        val vm = ReportProblemViewModel(r)
        vm.start("m1", "f1")
        advanceUntilIdle()

        vm.pickKind("wrong_match")
        vm.submit("  It's the 1998 one  ")
        assertThat(vm.uiState.value.submitting).isTrue()
        advanceUntilIdle()

        val s = vm.uiState.value
        assertThat(s.sent).isTrue()
        assertThat(s.submitting).isFalse()
        assertThat(s.history.first()).isEqualTo(created)
        assertThat(ReportProblem.openKinds(s.history)).contains("wrong_match")
    }

    @Test
    fun `a blank note is sent as no note`() = runTest(dispatcher) {
        val r = repo()
        coEvery { r.report("m1", "other", null, null) } returns issue("other")
        val vm = ReportProblemViewModel(r)
        vm.start("m1", null)
        advanceUntilIdle()

        vm.pickKind("other")
        vm.submit("   ")
        advanceUntilIdle()

        coVerify(exactly = 1) { r.report("m1", "other", null, null) }
        assertThat(vm.uiState.value.sent).isTrue()
    }

    @Test
    fun `409 ALREADY_REPORTED toasts, returns to the list and refreshes the history`() = runTest(dispatcher) {
        val r = mockk<IssuesRepository>()
        coEvery { r.listMine("m1") } returnsMany listOf(emptyList(), listOf(issue("video")))
        coEvery { r.report("m1", "video", null, null) } throws http(
            409, """{"error":{"code":"ALREADY_REPORTED","message":"you already have an open report"}}""",
        )
        val vm = ReportProblemViewModel(r)
        vm.start("m1", null)
        advanceUntilIdle()

        vm.pickKind("video")
        vm.submit(null)
        advanceUntilIdle()

        val s = vm.uiState.value
        assertThat(s.sent).isFalse()
        assertThat(s.toast).isEqualTo(ReportProblem.ALREADY_REPORTED_TEXT)
        assertThat(s.selectedKind).isNull()
        assertThat(ReportProblem.openKinds(s.history)).containsExactly("video")
        coVerify(exactly = 2) { r.listMine("m1") }
    }

    @Test
    fun `429 TOO_MANY_OPEN_ISSUES toasts and keeps the note step`() = runTest(dispatcher) {
        val r = repo()
        coEvery { r.report("m1", "audio", "crackles", null) } throws http(
            429, """{"error":{"code":"TOO_MANY_OPEN_ISSUES","message":"you have 10 open reports"}}""",
        )
        val vm = ReportProblemViewModel(r)
        vm.start("m1", null)
        advanceUntilIdle()

        vm.pickKind("audio")
        vm.submit("crackles")
        advanceUntilIdle()

        val s = vm.uiState.value
        assertThat(s.sent).isFalse()
        assertThat(s.submitting).isFalse()
        assertThat(s.selectedKind).isEqualTo("audio")
        assertThat(s.toast)
            .isEqualTo("You have several reports waiting for an admin. Try again once they have been looked at.")
    }

    @Test
    fun `a note over the server limit is refused locally`() = runTest(dispatcher) {
        val r = repo()
        val vm = ReportProblemViewModel(r)
        vm.start("m1", null)
        advanceUntilIdle()

        vm.pickKind("other")
        vm.submit("x".repeat(ReportProblem.NOTE_MAX + 1))
        advanceUntilIdle()

        assertThat(vm.uiState.value.toast).contains("at most")
        coVerify(exactly = 0) { r.report(any(), any(), any(), any()) }
    }

    @Test
    fun `submit without a picked kind does nothing`() = runTest(dispatcher) {
        val r = repo()
        val vm = ReportProblemViewModel(r)
        vm.start("m1", null)
        advanceUntilIdle()

        vm.submit("note")
        advanceUntilIdle()
        coVerify(exactly = 0) { r.report(any(), any(), any(), any()) }
    }
}
