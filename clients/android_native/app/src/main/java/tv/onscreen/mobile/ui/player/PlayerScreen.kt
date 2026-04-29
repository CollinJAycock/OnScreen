package tv.onscreen.mobile.ui.player

import android.app.Activity
import android.app.PictureInPictureParams
import android.net.Uri
import android.os.Build
import android.util.Rational
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
import androidx.compose.material.icons.filled.PictureInPicture
import androidx.compose.material.icons.filled.Search
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
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.ui.PlayerView
import android.content.Intent
import tv.onscreen.mobile.playback.AudioHandoff
import tv.onscreen.mobile.playback.OnScreenMediaSessionService
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.AudioStream
import tv.onscreen.mobile.data.model.Marker
import tv.onscreen.mobile.data.model.OnlineSubtitle
import tv.onscreen.mobile.data.model.SubtitleStream

@OptIn(UnstableApi::class)
@Composable
fun PlayerScreen(
    itemId: String,
    onClose: () -> Unit,
    onNext: (String) -> Unit,
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
            else -> PlayerHost(
                itemId = itemId,
                ui = ui,
                vm = vm,
                onClose = onClose,
                onNext = onNext,
            )
        }
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun PlayerHost(
    itemId: String,
    ui: PlayerUiState,
    vm: PlayerViewModel,
    onClose: () -> Unit,
    onNext: (String) -> Unit,
) {
    val context = LocalContext.current
    val source = ui.source!!
    val scope = rememberCoroutineScope()
    val itemType = ui.item?.type
    var showUpNext by remember { mutableStateOf(false) }
    val nextSibling = ui.nextSibling

    // Re-entry: if the same item is parked in AudioHandoff (because
    // an earlier instance of this screen handed playback to the
    // background service), stop the service so the foreground
    // notification disappears and rebuild a fresh player here. The
    // brief audio cut on re-entry is the cost of not threading a
    // bound-service binder through the Compose tree just to call
    // detach() — the player will resume from the last reported
    // progress position, so the user sees a small skip back at most.
    LaunchedEffect(itemId) {
        if (AudioHandoff.peekMetadata()?.itemId == itemId) {
            AudioHandoff.clear()
            context.stopService(Intent(context, OnScreenMediaSessionService::class.java))
        }
    }

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

    // Audio-only playback: park into the MediaSessionService on
    // dispose so backing out of the screen doesn't kill the music.
    // Video playback releases the player normally — PiP is the
    // backgrounding affordance for video.
    val isAudioOnly = ui.item?.files?.firstOrNull()?.video_codec == null
    DisposableEffect(player) {
        onDispose {
            if (isAudioOnly && player.playWhenReady && player.duration > 0) {
                val item = ui.item
                AudioHandoff.park(
                    player,
                    AudioHandoff.Metadata(
                        itemId = itemId,
                        itemType = item?.type ?: "track",
                        parentId = item?.parent_id,
                        index = item?.index,
                        hlsOffsetMs = vm.hlsOffsetMs,
                    ),
                )
                val intent = Intent(context, OnScreenMediaSessionService::class.java)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    context.startForegroundService(intent)
                } else {
                    context.startService(intent)
                }
            } else {
                player.release()
            }
        }
    }

    // Cross-device resume: when another of the user's devices reports
    // progress for this item, the VM emits a position to seek to.
    // We consume + clear so a recomposition doesn't seek twice off
    // the same emission.
    val remoteResume by vm.remoteResumeMs.collectAsState()
    LaunchedEffect(remoteResume) {
        val target = remoteResume ?: return@LaunchedEffect
        val playerTarget = (target - vm.hlsOffsetMs).coerceAtLeast(0L)
        player.seekTo(playerTarget)
        vm.clearRemoteResume()
    }

    // EOS handling — episodes surface the Up Next overlay (or pop
    // the back stack if there's no next), tracks chain silently to
    // the next track without an overlay (the lead-in countdown
    // would clip the song's outro fade).
    DisposableEffect(player, itemType, nextSibling) {
        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED) {
                    val nx = nextSibling
                    when {
                        nx == null -> onClose()
                        itemType == "track" -> onNext(nx.id)
                        else -> showUpNext = true
                    }
                }
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }

    // Lead-in Up Next overlay for episodes — surfaces in the last
    // ~25s of the stream so the user can confirm before the credits
    // finish. Skipped for tracks (see EOS handler).
    LaunchedEffect(player, itemType, nextSibling) {
        if (itemType != "episode" || nextSibling == null) return@LaunchedEffect
        while (isActive) {
            delay(1000)
            val dur = player.duration
            val pos = player.currentPosition
            if (dur > 0 && dur != Long.MAX_VALUE) {
                val remaining = dur - pos
                if (remaining in 0..25_000 && !showUpNext) {
                    showUpNext = true
                }
            }
        }
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
        // Picture-in-picture only makes sense for video — audio-only
        // playback gets the (forthcoming) MediaSession service for
        // backgrounding instead. Gate on resolution_h so audiobooks
        // and music don't surface a PiP button that would just shrink
        // the album art.
        val hasVideo = ui.item?.files?.firstOrNull()?.video_codec != null
        if (hasVideo && Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            IconButton(onClick = { enterPip(context as? Activity) }) {
                Icon(Icons.Default.PictureInPicture, contentDescription = "Picture in picture", tint = Color.White)
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

    var showOnlineSubtitleSearch by remember { mutableStateOf(false) }

    if (showSubtitlePicker) {
        SubtitlePickerDialog(
            streams = ui.subtitles,
            player = player,
            onFindMore = {
                showSubtitlePicker = false
                showOnlineSubtitleSearch = true
            },
            onDismiss = { showSubtitlePicker = false },
        )
    }

    if (showOnlineSubtitleSearch) {
        OnlineSubtitleSearchDialog(
            itemId = itemId,
            preferredLang = ui.preferredSubtitleLang,
            vm = vm,
            onDismiss = {
                showOnlineSubtitleSearch = false
                vm.clearOnlineSubtitleSearch()
            },
        )
    }

    if (showUpNext && nextSibling != null) {
        UpNextOverlay(
            title = nextSibling.title,
            onPlay = { onNext(nextSibling.id) },
            onDismiss = { showUpNext = false },
        )
    }
}

@Composable
private fun UpNextOverlay(
    title: String,
    onPlay: () -> Unit,
    onDismiss: () -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp),
        contentAlignment = Alignment.TopEnd,
    ) {
        Column(
            modifier = Modifier
                .background(
                    color = Color(0xCC000000),
                    shape = RoundedCornerShape(12.dp),
                )
                .padding(16.dp),
        ) {
            Text("Up next", color = Color.White.copy(alpha = 0.6f))
            Spacer(Modifier.width(0.dp))
            Text(title, color = Color.White, style = MaterialTheme.typography.titleMedium)
            Spacer(Modifier.width(0.dp))
            Row {
                Button(onClick = onPlay) { Text("Play now") }
                Spacer(Modifier.width(8.dp))
                TextButton(onClick = onDismiss) { Text("Dismiss") }
            }
        }
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
    onFindMore: () -> Unit,
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
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 8.dp),
                ) {
                    TextButton(onClick = onFindMore) {
                        Icon(Icons.Default.Search, contentDescription = null)
                        Spacer(Modifier.width(6.dp))
                        Text("Find more online…")
                    }
                }
            }
        },
    )
}

@Composable
private fun OnlineSubtitleSearchDialog(
    itemId: String,
    preferredLang: String?,
    vm: PlayerViewModel,
    onDismiss: () -> Unit,
) {
    val ui by vm.onlineSubtitleSearch.collectAsState()
    var lang by remember { mutableStateOf(preferredLang ?: "en") }
    var query by remember { mutableStateOf("") }

    LaunchedEffect(Unit) {
        // Pre-seed a search on the user's preferred language so the
        // dialog isn't empty on open.
        vm.searchOnlineSubtitles(itemId, lang.ifEmpty { null }, null)
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text("Find subtitles") },
        text = {
            Column {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    androidx.compose.material3.OutlinedTextField(
                        value = lang,
                        onValueChange = { lang = it.take(3).lowercase() },
                        singleLine = true,
                        label = { Text("Lang") },
                        modifier = Modifier.width(96.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                    androidx.compose.material3.OutlinedTextField(
                        value = query,
                        onValueChange = { query = it },
                        singleLine = true,
                        label = { Text("Title (optional)") },
                        modifier = Modifier.width(220.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                    Button(onClick = {
                        vm.searchOnlineSubtitles(
                            itemId,
                            lang.ifEmpty { null },
                            query.ifBlank { null },
                        )
                    }) { Text("Search") }
                }
                Spacer(Modifier.width(8.dp))
                when {
                    ui.loading -> CircularProgressIndicator()
                    ui.error != null -> Text(ui.error!!)
                    ui.results.isEmpty() -> Text("No matches yet — try different terms.")
                    else -> Column {
                        ui.results.take(20).forEach { sub ->
                            OnlineSubtitleRow(
                                sub = sub,
                                onPick = {
                                    vm.downloadOnlineSubtitle(itemId, sub) {
                                        // Server attaches the .srt to
                                        // the file's media_files row;
                                        // user has to open the
                                        // subtitle picker again to
                                        // toggle it on. (Auto-toggle
                                        // would need a fresh prepare
                                        // and a small audio cut.)
                                        onDismiss()
                                    }
                                },
                            )
                        }
                    }
                }
            }
        },
    )
}

@Composable
private fun OnlineSubtitleRow(sub: OnlineSubtitle, onPick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.padding(end = 8.dp)) {
            Text(sub.file_name, style = MaterialTheme.typography.bodyMedium)
            val parts = mutableListOf<String>().apply {
                add(sub.language)
                if (sub.hd) add("HD")
                if (sub.hearing_impaired) add("CC")
                if (sub.from_trusted) add("trusted")
                if (sub.download_count > 0) add("⬇ ${sub.download_count}")
            }
            Text(
                parts.joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.width(8.dp))
        Button(onClick = onPick) { Text("Add") }
    }
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

/** Drop the host activity into PiP at a 16:9 aspect ratio. The
 *  Compose tree keeps rendering — Android composites the player
 *  surface into the floating window, controls automatically hide.
 *  Returning safely when the activity is null or the OS rejects the
 *  request keeps a misconfigured device from crashing the player. */
private fun enterPip(activity: Activity?) {
    if (activity == null || Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
    try {
        val params = PictureInPictureParams.Builder()
            .setAspectRatio(Rational(16, 9))
            .build()
        activity.enterPictureInPictureMode(params)
    } catch (_: Exception) {
        // Some launchers / form-factors reject PiP — swallow rather
        // than crash; the user can fall back to backgrounding music
        // via the MediaSession service.
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

