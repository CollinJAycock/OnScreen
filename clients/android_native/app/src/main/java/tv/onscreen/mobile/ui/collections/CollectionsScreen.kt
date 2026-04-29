package tv.onscreen.mobile.ui.collections

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.CollectionItem
import tv.onscreen.mobile.data.model.MediaCollection
import tv.onscreen.mobile.data.repository.CollectionRepository
import javax.inject.Inject

@HiltViewModel
class CollectionsViewModel @Inject constructor(
    private val repo: CollectionRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(CollectionsUi())
    val state: StateFlow<CollectionsUi> = _state.asStateFlow()

    init { load() }

    fun load() {
        viewModelScope.launch {
            _state.value = CollectionsUi(loading = true)
            try {
                _state.value = CollectionsUi(loading = false, items = repo.getCollections())
            } catch (e: Exception) {
                _state.value = CollectionsUi(loading = false, error = e.message)
            }
        }
    }
}

data class CollectionsUi(
    val loading: Boolean = false,
    val items: List<MediaCollection> = emptyList(),
    val error: String? = null,
)

@HiltViewModel
class CollectionDetailViewModel @Inject constructor(
    private val repo: CollectionRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(CollectionDetailUi())
    val state: StateFlow<CollectionDetailUi> = _state.asStateFlow()

    fun load(id: String) {
        viewModelScope.launch {
            _state.value = CollectionDetailUi(loading = true)
            try {
                val (items, _) = repo.getItems(id, limit = 200)
                _state.value = CollectionDetailUi(loading = false, items = items)
            } catch (e: Exception) {
                _state.value = CollectionDetailUi(loading = false, error = e.message)
            }
        }
    }
}

data class CollectionDetailUi(
    val loading: Boolean = false,
    val items: List<CollectionItem> = emptyList(),
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CollectionsScreen(
    onOpenCollection: (String) -> Unit,
    onBack: () -> Unit,
    vm: CollectionsViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsState()
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Collections") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                ui.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                ui.error != null -> Text(ui.error!!, modifier = Modifier.align(Alignment.Center))
                ui.items.isEmpty() -> Text(
                    "No collections",
                    modifier = Modifier.align(Alignment.Center),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                else -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(ui.items, key = { it.id }) { c ->
                        Column(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable { onOpenCollection(c.id) }
                                .padding(vertical = 12.dp),
                        ) {
                            Text(c.name, style = MaterialTheme.typography.bodyLarge)
                            val sub = listOfNotNull(c.type, c.genre).joinToString(" · ")
                            if (sub.isNotEmpty()) {
                                Text(
                                    sub,
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CollectionDetailScreen(
    collectionId: String,
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: CollectionDetailViewModel = hiltViewModel(),
) {
    LaunchedEffect(collectionId) { vm.load(collectionId) }
    val ui by vm.state.collectAsState()
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Collection") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                ui.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                ui.error != null -> Text(ui.error!!, modifier = Modifier.align(Alignment.Center))
                else -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(ui.items, key = { it.id }) { item ->
                        Column(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable { onOpenItem(item.id) }
                                .padding(vertical = 12.dp),
                        ) {
                            Text(item.title, style = MaterialTheme.typography.bodyLarge)
                            Text(
                                item.type,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }
}
