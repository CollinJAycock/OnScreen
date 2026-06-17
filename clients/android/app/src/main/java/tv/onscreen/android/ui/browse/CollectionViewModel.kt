package tv.onscreen.android.ui.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.CollectionItem
import tv.onscreen.android.data.repository.CollectionRepository
import javax.inject.Inject

@HiltViewModel
class CollectionViewModel @Inject constructor(
    private val collectionRepo: CollectionRepository,
) : ViewModel() {

    private val _items = MutableStateFlow<List<CollectionItem>>(emptyList())
    val items: StateFlow<List<CollectionItem>> = _items

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    // True once the first page load has finished (success OR failure) so the
    // fragment can distinguish "still loading" from "loaded and genuinely empty"
    // and show the empty state instead of a blank, unfocusable grid.
    private val _loaded = MutableStateFlow(false)
    val loaded: StateFlow<Boolean> = _loaded

    private var collectionId: String? = null
    private var offset = 0
    private var total = Int.MAX_VALUE
    private var loading = false
    private val pageSize = 50

    fun load(collectionId: String) {
        this.collectionId = collectionId
        offset = 0
        total = Int.MAX_VALUE
        _items.value = emptyList()
        _error.value = null
        _loaded.value = false
        loadMore()
    }

    fun loadMore() {
        val id = collectionId ?: return
        if (loading || offset >= total) return
        loading = true

        viewModelScope.launch {
            try {
                val (page, count) = collectionRepo.getItems(id, pageSize, offset)
                total = count
                offset += page.size
                _items.value = _items.value + page
                _error.value = null
            } catch (e: Exception) {
                // Only surface an error when we have nothing to show — a failed
                // *pagination* page shouldn't blow away an already-populated grid.
                if (_items.value.isEmpty()) _error.value = e.message ?: "Failed to load"
            } finally {
                loading = false
                _loaded.value = true
            }
        }
    }
}
