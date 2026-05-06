package tv.onscreen.mobile.ui.playlists

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.Playlist
import tv.onscreen.mobile.data.repository.PlaylistRepository
import javax.inject.Inject

data class PlaylistsUi(
    val loading: Boolean = true,
    val playlists: List<Playlist> = emptyList(),
    val error: String? = null,
    /** Set when the user is mid-creation; null when the dialog is
     *  closed. The dialog reads this for its draft state so a
     *  configuration-change doesn't lose the half-typed rule. */
    val draft: SmartPlaylistRulesDraft? = null,
    /** Generic post-action error surface (create / delete failures).
     *  Cleared on next successful action or on user dismiss. */
    val actionError: String? = null,
)

@HiltViewModel
class PlaylistsViewModel @Inject constructor(
    private val repo: PlaylistRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(PlaylistsUi())
    val state: StateFlow<PlaylistsUi> = _state.asStateFlow()

    init { refresh() }

    fun refresh() {
        viewModelScope.launch {
            _state.value = _state.value.copy(loading = true, error = null)
            try {
                _state.value = _state.value.copy(
                    loading = false,
                    playlists = repo.list(),
                )
            } catch (e: Exception) {
                _state.value = _state.value.copy(
                    loading = false,
                    error = e.message ?: "Couldn't load playlists",
                )
            }
        }
    }

    /** Open the creator dialog with a fresh draft. */
    fun openCreator() {
        _state.value = _state.value.copy(draft = SmartPlaylistRulesDraft())
    }

    fun closeCreator() {
        _state.value = _state.value.copy(draft = null, actionError = null)
    }

    fun updateDraft(transform: (SmartPlaylistRulesDraft) -> SmartPlaylistRulesDraft) {
        val cur = _state.value.draft ?: return
        _state.value = _state.value.copy(draft = transform(cur))
    }

    /**
     * Save the current draft as a smart playlist. Validates first;
     * on validation error, surfaces the message via [PlaylistsUi.actionError]
     * (the creator dialog renders it). On API failure, similar.
     * On success, refreshes the list and closes the dialog.
     */
    fun saveDraft() {
        val draft = _state.value.draft ?: return
        when (val res = SmartPlaylistValidator.validate(draft)) {
            is SmartPlaylistValidator.Result.Error -> {
                _state.value = _state.value.copy(actionError = res.message)
            }
            is SmartPlaylistValidator.Result.Ok -> {
                viewModelScope.launch {
                    try {
                        repo.createSmart(name = res.name, rules = res.rules)
                        _state.value = _state.value.copy(draft = null, actionError = null)
                        refresh()
                    } catch (e: Exception) {
                        _state.value = _state.value.copy(
                            actionError = e.message ?: "Couldn't save playlist",
                        )
                    }
                }
            }
        }
    }

    fun delete(id: String) {
        viewModelScope.launch {
            try {
                repo.delete(id)
                refresh()
            } catch (e: Exception) {
                _state.value = _state.value.copy(actionError = e.message ?: "Delete failed")
            }
        }
    }
}
