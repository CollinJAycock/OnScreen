package tv.onscreen.mobile.ui.player

import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Audiotrack
import androidx.compose.material.icons.filled.Subtitles
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.ui.PlayerView
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.AudioStream
import tv.onscreen.mobile.data.model.Marker
import tv.onscreen.mobile.data.model.SubtitleStream

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
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        when {
            ui.error != null -> Text(ui.error!!, color = Color.White)
            ui.loading || ui.source == null -> CircularProgressIndicator()
            else -> PlayerHost(itemId = itemId, ui = ui, vm = vm)
        }
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun PlayerHost(
    itemId: String,
    ui: PlayerUiState,
    vm: PlayerViewModel,
) {
    val context = LocalContext.current
    val source = ui.source!!
    val scope = rememberCoroutineScope()

    val player = remember(source) {
        ExoPlayer.Builder(context).build().apply {
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
            val startMs = when (source) {
                is PlaybackSource.DirectPlay -> source.startMs
                is PlaybackSource.Hls -> 0L
            }
            // Apply server-side language preferences once at prepare
            // time. ExoPlayer's track selector handles direct-play
            // language switches; HLS sessions only carry one audio
            // stream so this is a no-op there until the user opens
            // the picker and triggers a session re-issue.
            ui.preferredAudioLang?.let { lang ->
                trackSelectionParameters = trackSelectionParameters.buildUpon()
                    .setPreferredAudioLanguage(lang)
                    .build()
            }
            ui.preferredSubtitleLang?.let { lang ->
                trackSelectionParameters = trackSelectionParameters.buildUpon()
                    .setPreferredTextLanguage(lang)
                    .build()
            }
            seekTo(startMs)
            prepare()
            playWhenReady = true
        }
    }

    DisposableEffect(player) {
        onDispose { player.release() }
    }

    // Progress reporting — fires every 10s while playing, plus a
    // final "stopped" event when the screen leaves the back stack.
    // Same cadence the TV client uses; keeps the resume marker fresh
    // for cross-device handoff.
    DisposableEffect(itemId, source) {
        val job = scope.launch {
            while (isActive) {
                delay(10_000)
                if (player.playWhenReady && player.duration > 0) {
                    val pos = player.currentPosition + vm.hlsOffsetMs
                    vm.reportProgress(itemId, pos, player.duration, "playing")
                }
            }
        }
        onDispose {
            job.cancel()
            if (player.duration > 0) {
                val pos = player.currentPosition + vm.hlsOffsetMs
                vm.reportProgress(itemId, pos, player.duration, "stopped")
            }
        }
    }

    var showAudioPicker by remember { mutableStateOf(false) }
    var showSubtitlePicker by remember { mutableStateOf(false) }
    val activeAudioIndex = remember { mutableIntStateOf(-1) }

    AndroidView(
        modifier = Modifier.fillMaxSize(),
        factory = { ctx ->
            PlayerView(ctx).apply {
                this.player = player
                useController = true
            }
        },
    )

    // Skip-intro / skip-credits overlay. Polls the player position
    // on a short cadence and shows the button when position lands
    // inside a marker window. Clicking jumps the player past it.
    SkipMarkerOverlay(player = player, markers = ui.markers, hlsOffsetMs = vm.hlsOffsetMs)

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(16.dp),
        horizontalArrangement = Arrangement.End,
    ) {
        if (ui.audioStreams.size > 1) {
            IconButton(onClick = { showAudioPicker = true }) {
                Icon(Icons.Default.Audiotrack, contentDescription = "Audio", tint = Color.White)
            }
        }
        if (ui.subtitles.isNotEmpty()) {
            IconButton(onClick = { showSubtitlePicker = true }) {
                Icon(Icons.Default.Subtitles, contentDescription = "Subtitles", tint = Color.White)
            }
        }
    }

    if (showAudioPicker) {
        AudioPickerDialog(
            streams = ui.audioStreams,
            activeIndex = activeAudioIndex.intValue,
            onPick = { idx ->
                showAudioPicker = false
                activeAudioIndex.intValue = idx
                applyAudioSelection(idx, ui.audioStreams, source, player, vm)
            },
            onDismiss = { showAudioPicker = false },
        )
    }

    if (showSubtitlePicker) {
        SubtitlePickerDialog(
            streams = ui.subtitles,
            player = player,
            onDismiss = { showSubtitlePicker = false },
        )
    }
}

@OptIn(UnstableApi::class)
private fun applyAudioSelection(
    idx: Int,
    streams: List<AudioStream>,
    source: PlaybackSource,
    player: ExoPlayer,
    vm: PlayerViewModel,
) {
    val stream = streams.getOrNull(idx) ?: return
    if (source is PlaybackSource.Hls) {
        // Transcoded HLS sessions carry a single audio stream — to
        // switch we re-issue the session at the current position
        // with a new audio_stream_index. Direct play falls through
        // to ExoPlayer's track selector which sees every audio
        // track in the source container.
        vm.switchAudioStream(stream.index, player.currentPosition)
    } else {
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
            .setPreferredAudioLanguage(stream.language)
            .build()
    }
}

@Composable
private fun AudioPickerDialog(
    streams: List<AudioStream>,
    activeIndex: Int,
    onPick: (Int) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text("Audio") },
        text = {
            Column {
                streams.forEachIndexed { i, s ->
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton(
                            selected = i == activeIndex,
                            onClick = { onPick(i) },
                        )
                        Spacer(Modifier.width(8.dp))
                        Text(formatAudioLabel(s))
                    }
                }
            }
        },
    )
}

@OptIn(UnstableApi::class)
@Composable
private fun SubtitlePickerDialog(
    streams: List<SubtitleStream>,
    player: ExoPlayer,
    onDismiss: () -> Unit,
) {
    val current = player.trackSelectionParameters
    val disabled = current.disabledTrackTypes.contains(C.TRACK_TYPE_TEXT)
    val activeLang = current.preferredTextLanguages.firstOrNull()

    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text("Subtitles") },
        text = {
            Column {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    RadioButton(
                        selected = disabled,
                        onClick = {
                            player.trackSelectionParameters =
                                player.trackSelectionParameters.buildUpon()
                                    .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
                                    .build()
                            onDismiss()
                        },
                    )
                    Spacer(Modifier.width(8.dp))
                    Text("Off")
                }
                streams.forEach { s ->
                    val selected = !disabled && s.language == activeLang
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        RadioButton(
                            selected = selected,
                            onClick = {
                                player.trackSelectionParameters =
                                    player.trackSelectionParameters.buildUpon()
                                        .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
                                        .setPreferredTextLanguage(s.language)
                                        .build()
                                onDismiss()
                            },
                        )
                        Spacer(Modifier.width(8.dp))
                        Text(formatSubtitleLabel(s))
                    }
                }
            }
        },
    )
}

@Composable
private fun SkipMarkerOverlay(
    player: ExoPlayer,
    markers: List<Marker>,
    hlsOffsetMs: Long,
) {
    if (markers.isEmpty()) return

    var activeMarker by remember { mutableStateOf<Marker?>(null) }

    LaunchedEffect(markers) {
        while (isActive) {
            val contentPos = player.currentPosition + hlsOffsetMs
            activeMarker = markers.firstOrNull { contentPos in it.start_ms..it.end_ms }
            delay(500)
        }
    }

    val marker = activeMarker ?: return
    Box(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp),
        contentAlignment = Alignment.BottomEnd,
    ) {
        Button(
            onClick = {
                // Seek slightly past the end so we don't immediately
                // re-enter the same marker window on the next tick.
                val target = (marker.end_ms - hlsOffsetMs + 500).coerceAtLeast(0)
                player.seekTo(target)
                activeMarker = null
            },
            shape = RoundedCornerShape(24.dp),
        ) {
            Text(if (marker.kind == "intro") "Skip intro" else "Skip credits")
        }
    }
}

private fun formatAudioLabel(s: AudioStream): String {
    val parts = mutableListOf<String>()
    if (s.language.isNotEmpty()) parts += s.language
    if (s.title.isNotEmpty()) parts += s.title
    parts += "${s.channels}ch"
    parts += s.codec
    return parts.joinToString(" · ")
}

private fun formatSubtitleLabel(s: SubtitleStream): String {
    val parts = mutableListOf<String>()
    if (s.language.isNotEmpty()) parts += s.language
    if (s.title.isNotEmpty()) parts += s.title
    if (s.forced) parts += "forced"
    return parts.joinToString(" · ")
}

