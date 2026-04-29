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
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.ui.PlayerView

@OptIn(UnstableApi::class)
@Composable
fun PlayerScreen(
    itemId: String,
    onClose: () -> Unit,
    vm: PlayerViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.prepare(itemId) }
    val ui by vm.state.collectAsState()
    BackHandler(onBack = onClose)

    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        when {
            ui.error != null -> Text(ui.error!!, color = Color.White)
            ui.loading || ui.source == null -> CircularProgressIndicator()
            else -> ExoPlayerHost(source = ui.source!!)
        }
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun ExoPlayerHost(source: PlaybackSource) {
    val context = LocalContext.current

    val player = remember(source) {
        ExoPlayer.Builder(context).build().apply {
            // Same DataSource factory for both branches — only the
            // MediaSource type changes between progressive and HLS.
            // DefaultHttpDataSource handles range requests for direct
            // play; HlsMediaSource fetches the playlist + segments
            // through the same factory.
            val dsFactory = DefaultHttpDataSource.Factory()
            val mediaSource: MediaSource = when (source) {
                is PlaybackSource.DirectPlay ->
                    ProgressiveMediaSource.Factory(dsFactory)
                        .createMediaSource(MediaItem.fromUri(Uri.parse(source.url)))
                is PlaybackSource.Hls ->
                    HlsMediaSource.Factory(dsFactory)
                        .createMediaSource(MediaItem.fromUri(Uri.parse(source.playlistUrl)))
            }
            setMediaSource(mediaSource)
            // For HLS the URL itself is offset to where we want to
            // resume — segments before posMs aren't generated. Seek
            // is a no-op there. For direct play we honour the
            // server-side resume position.
            val startMs = when (source) {
                is PlaybackSource.DirectPlay -> source.startMs
                is PlaybackSource.Hls -> 0L
            }
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
