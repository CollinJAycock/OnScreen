package tv.onscreen.mobile.ui.item

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
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
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.repository.ItemRepository
import javax.inject.Inject

@HiltViewModel
class ItemDetailViewModel @Inject constructor(
    private val repo: ItemRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ItemDetailUi())
    val state: StateFlow<ItemDetailUi> = _state.asStateFlow()

    fun load(itemId: String) {
        viewModelScope.launch {
            _state.value = ItemDetailUi(loading = true)
            try {
                _state.value = ItemDetailUi(loading = false, detail = repo.getItem(itemId))
            } catch (e: Exception) {
                _state.value = ItemDetailUi(loading = false, error = e.message)
            }
        }
    }
}

data class ItemDetailUi(
    val loading: Boolean = false,
    val detail: ItemDetail? = null,
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ItemDetailScreen(
    itemId: String,
    onPlay: (String) -> Unit,
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: ItemDetailViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.load(itemId) }
    val ui by vm.state.collectAsState()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(ui.detail?.title ?: "") },
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
                ui.detail != null -> {
                    val d = ui.detail!!
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text(d.title, style = MaterialTheme.typography.headlineSmall)
                        if (d.year != null) {
                            Text(d.year.toString(), style = MaterialTheme.typography.bodyMedium)
                        }
                        Spacer(Modifier.height(16.dp))
                        Button(onClick = { onPlay(itemId) }) {
                            Icon(Icons.Default.PlayArrow, contentDescription = null)
                            Spacer(Modifier.height(0.dp))
                            Text("Play")
                        }
                        if (!d.summary.isNullOrEmpty()) {
                            Spacer(Modifier.height(16.dp))
                            Text(d.summary, style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                }
            }
        }
    }
}
