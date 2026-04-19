package tv.onscreen.android.ui.notifications

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.repository.NotificationsRepository
import javax.inject.Inject

data class NotificationsUiState(
    val items: List<NotificationItem> = emptyList(),
    val unreadCount: Long = 0,
    val isLoading: Boolean = false,
    val error: String? = null,
)

@HiltViewModel
class NotificationsViewModel @Inject constructor(
    private val repo: NotificationsRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(NotificationsUiState())
    val uiState: StateFlow<NotificationsUiState> = _uiState

    init {
        load()
        startStream()
    }

    private fun startStream() {
        viewModelScope.launch {
            while (true) {
                try {
                    repo.subscribe().collect { incoming ->
                        val current = _uiState.value.items
                        if (current.any { it.id == incoming.id }) return@collect
                        val merged = listOf(incoming) + current
                        _uiState.value = _uiState.value.copy(
                            items = merged,
                            unreadCount = merged.count { !it.read }.toLong(),
                        )
                    }
                } catch (_: Exception) {
                    // Stream dropped; fall through to reconnect.
                }
                delay(5_000)
            }
        }
    }

    fun load() {
        viewModelScope.launch {
            _uiState.value = _uiState.value.copy(isLoading = true, error = null)
            try {
                val items = repo.list(limit = 100)
                val unread = items.count { !it.read }.toLong()
                _uiState.value = NotificationsUiState(items = items, unreadCount = unread)
            } catch (e: Exception) {
                _uiState.value = NotificationsUiState(error = e.message ?: "load failed")
            }
        }
    }

    fun markRead(id: String) {
        viewModelScope.launch {
            try {
                repo.markRead(id)
                val updated = _uiState.value.items.map { if (it.id == id) it.copy(read = true) else it }
                _uiState.value = _uiState.value.copy(
                    items = updated,
                    unreadCount = updated.count { !it.read }.toLong(),
                )
            } catch (_: Exception) {
                // Best-effort; state stays as-is.
            }
        }
    }

    fun markAllRead() {
        viewModelScope.launch {
            try {
                repo.markAllRead()
                val updated = _uiState.value.items.map { it.copy(read = true) }
                _uiState.value = _uiState.value.copy(items = updated, unreadCount = 0)
            } catch (_: Exception) {
            }
        }
    }
}
