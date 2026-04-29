package tv.onscreen.mobile.ui.player

import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.ui.PlayerView
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import javax.inject.Inject

@HiltViewModel
class PlayerViewModel @Inject constructor(
    private val repo: ItemRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(PlayerUi())
    val state: StateFlow<PlayerUi> = _state.asStateFlow()

    fun load(itemId: String) {
        viewModelScope.launch {
            try {
                val detail = repo.getItem(itemId)
                val file = detail.files.firstOrNull() ?: error("no playable file")
                val server = prefs.getServerUrl()?.trimEnd('/').orEmpty()
                val token = file.stream_token ?: prefs.getAccessToken().orEmpty()
                // Use the per-file stream token (24 h, file_id-bound)
                // when present so ExoPlayer's HTTP stack — which bypasses
                // the OkHttp TokenAuthenticator — doesn't 401 mid-stream.
                val url = "$server${file.stream_url}?token=$token"
                _state.value = PlayerUi(url = url, startMs = detail.view_offset_ms)
            } catch (e: Exception) {
                _state.value = PlayerUi(error = e.message)
            }
        }
    }
}

data class PlayerUi(
    val url: String? = null,
    val startMs: Long = 0,
    val error: String? = null,
)

@OptIn(UnstableApi::class)
@Composable
fun PlayerScreen(
    itemId: String,
    onClose: () -> Unit,
    vm: PlayerViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.load(itemId) }
    val ui by vm.state.collectAsState()
    BackHandler(onBack = onClose)

    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        when {
            ui.error != null -> Text(ui.error!!, color = Color.White)
            ui.url == null -> CircularProgressIndicator()
            else -> ExoPlayerHost(url = ui.url!!, startMs = ui.startMs)
        }
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun ExoPlayerHost(url: String, startMs: Long) {
    val context = LocalContext.current
    val player = remember(url) {
        ExoPlayer.Builder(context).build().apply {
            val source = ProgressiveMediaSource.Factory(DefaultHttpDataSource.Factory())
                .createMediaSource(MediaItem.fromUri(Uri.parse(url)))
            setMediaSource(source)
            seekTo(startMs)
            prepare()
            playWhenReady = true
        }
    }

    DisposableEffect(player) {
        onDispose { player.release() }
    }

    AndroidView(
        modifier = Modifier.fillMaxSize(),
        factory = { ctx ->
            PlayerView(ctx).apply {
                this.player = player
                useController = true
            }
        },
    )
}
