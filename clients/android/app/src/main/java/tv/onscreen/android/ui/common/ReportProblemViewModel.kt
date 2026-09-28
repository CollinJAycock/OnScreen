package tv.onscreen.android.ui.common

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.MediaIssue
import tv.onscreen.android.data.repository.IssuesRepository
import javax.inject.Inject

data class ReportProblemUiState(
    /** The caller's earlier reports are being fetched. */
    val loadingHistory: Boolean = false,
    /** The caller's own reports on the item, newest first. */
    val history: List<MediaIssue> = emptyList(),
    /** Kind picked, awaiting the optional note + Send. Null = the kind list. */
    val selectedKind: String? = null,
    val submitting: Boolean = false,
    /** The report went through; the dialog shows its thank-you. */
    val sent: Boolean = false,
    /** One-shot message for a toast; cleared by [ReportProblemViewModel.toastShown]. */
    val toast: String? = null,
)

/**
 * "Report a problem" dialog state. Two steps — pick a kind, then send with an
 * optional note — plus the caller's earlier reports, so a kind that already
 * has an open report is disabled ("You reported … 2 days ago") instead of
 * inviting a 409.
 */
@HiltViewModel
class ReportProblemViewModel @Inject constructor(
    private val issues: IssuesRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(ReportProblemUiState())
    val uiState: StateFlow<ReportProblemUiState> = _uiState

    private var itemId: String? = null
    private var fileId: String? = null
    /** Guards a slow history response landing after a newer load started. */
    private var loadSeq = 0

    /** Bind to [itemId] and fetch the history. Idempotent for the same item,
     *  so a recreated dialog (rotation / process restore) doesn't refetch. */
    fun start(itemId: String, fileId: String?) {
        if (this.itemId == itemId) return
        this.itemId = itemId
        this.fileId = fileId
        _uiState.value = ReportProblemUiState()
        loadHistory()
    }

    private fun loadHistory() {
        val id = itemId ?: return
        val seq = ++loadSeq
        _uiState.value = _uiState.value.copy(loadingHistory = true)
        viewModelScope.launch {
            val list = try {
                issues.listMine(id)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // The history is a courtesy; the report can still be sent.
                emptyList()
            }
            if (seq != loadSeq) return@launch
            _uiState.value = _uiState.value.copy(loadingHistory = false, history = list)
        }
    }

    /** The user picked [kind]. A kind with an open report can't be sent again
     *  (the server would answer 409), so say so instead of advancing. */
    fun pickKind(kind: String) {
        val s = _uiState.value
        if (s.sent || s.submitting) return
        if (kind in ReportProblem.openKinds(s.history)) {
            _uiState.value = s.copy(toast = ReportProblem.ALREADY_REPORTED_TEXT)
            return
        }
        _uiState.value = s.copy(selectedKind = kind)
    }

    /** Back from the note step to the kind list. */
    fun backToKinds() {
        val s = _uiState.value
        if (s.submitting || s.sent) return
        _uiState.value = s.copy(selectedKind = null)
    }

    /** Send the report for the picked kind with an optional [note]. */
    fun submit(note: String?) {
        val s = _uiState.value
        val id = itemId ?: return
        val kind = s.selectedKind ?: return
        if (s.submitting || s.sent) return
        val trimmed = note?.trim().orEmpty()
        if (trimmed.codePointCount(0, trimmed.length) > ReportProblem.NOTE_MAX) {
            _uiState.value = s.copy(toast = "Notes can be at most ${ReportProblem.NOTE_MAX} characters.")
            return
        }
        _uiState.value = s.copy(submitting = true)
        viewModelScope.launch {
            try {
                val created = issues.report(id, kind, trimmed.ifEmpty { null }, fileId)
                val cur = _uiState.value
                _uiState.value = cur.copy(
                    submitting = false,
                    sent = true,
                    selectedKind = null,
                    history = listOf(created) + cur.history,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                val msg = ReportProblem.errorMessage(e)
                val already = ReportProblem.isAlreadyReported(e)
                _uiState.value = _uiState.value.copy(
                    submitting = false,
                    toast = msg,
                    // Filed elsewhere meanwhile (another device): back to the
                    // list and refresh so that kind shows as reported.
                    selectedKind = if (already) null else _uiState.value.selectedKind,
                )
                if (already) loadHistory()
            }
        }
    }

    fun toastShown() {
        if (_uiState.value.toast != null) _uiState.value = _uiState.value.copy(toast = null)
    }
}
