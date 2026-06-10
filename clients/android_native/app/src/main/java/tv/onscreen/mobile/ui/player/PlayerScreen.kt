package tv.onscreen.mobile.ui.player

import android.app.Activity
import android.app.PictureInPictureParams
import android.net.Uri
import android.os.Build
import android.util.Rational
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Audiotrack
import androidx.compose.material.icons.filled.Bedtime
import androidx.compose.material.icons.filled.Bookmarks
import androidx.compose.material.icons.filled.Lyrics
import androidx.compose.material.icons.filled.FormatSize
import androidx.compose.material.icons.filled.PictureInPicture
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
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
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
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
import tv.onscreen.mobile.data.model.OnlineSubtitle
import tv.onscreen.mobile.data.model.SubtitleStream
import tv.onscreen.mobile.cast.CastMediaInfo
import tv.onscreen.mobile.cast.CastSender
import tv.onscreen.mobile.data.prefs.SubtitleStyle
import android.content.ComponentName
import android.os.Bundle
import androidx.compose.runtime.produceState
import androidx.core.content.ContextCompat
import androidx.media3.common.MediaMetadata
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import kotlinx.coroutines.suspendCancellableCoroutine
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.playback.ActiveVideoTracker
import tv.onscreen.mobile.playback.PlaybackService
import tv.onscreen.mobile.ui.LocalInPipMode

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

    // Cellular gate: when the user has warn-on-cellular enabled and
    // we're on a metered network, block PlayerHost composition until
    // the user confirms. We do the check once after the source
    // resolves — connectivity changes mid-playback don't re-prompt
    // (re-entering the player is the right place for that retry).
    val context = LocalContext.current
    var cellularConfirmed by remember { mutableStateOf(false) }
    var cellularPromptVisible by remember { mutableStateOf(false) }
    val isVideo = ui.item?.files?.firstOrNull()?.video_codec != null
    LaunchedEffect(ui.source, isVideo) {
        if (ui.source != null && isVideo && !cellularConfirmed && !cellularPromptVisible) {
            if (vm.shouldWarnCellular() && isOnCellular(context)) {
                cellularPromptVisible = true
            } else {
                cellularConfirmed = true
            }
        }
    }
    if (cellularPromptVisible) {
        AlertDialog(
            // No-op: a scrim tap or BACK shouldn't eject the user from
            // playback on an accidental touch — force an explicit
            // Continue/Cancel choice from the buttons below.
            onDismissRequest = {},
            title = { Text("Stream over cellular?") },
            text = {
                Text("You're on a metered connection. Streaming video may use a significant amount of data.")
            },
            confirmButton = {
                TextButton(onClick = {
                    cellularPromptVisible = false
                    cellularConfirmed = true
                }) { Text("Continue") }
            },
            dismissButton = {
                TextButton(onClick = onClose) { Text("Cancel") }
            },
        )
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        when {
            ui.error != null -> Text(ui.error!!, color = Color.White)
            ui.loading || ui.source == null -> CircularProgressIndicator()
            // Wait until the user has acknowledged cellular (or it
            // wasn't applicable). Without this gate, ExoPlayer fires
            // up + buffers before the dialog renders.
            isVideo && !cellularConfirmed -> CircularProgressIndicator()
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

/** True when the active default network is cellular (or otherwise
 *  reports as metered). Used by the cellular-warn gate; falls
 *  back to false on any error so a misconfigured device doesn't
 *  see the prompt for every playback. */
private fun isOnCellular(context: android.content.Context): Boolean {
    return try {
        val cm = context.getSystemService(android.content.Context.CONNECTIVITY_SERVICE)
            as? android.net.ConnectivityManager ?: return false
        val net = cm.activeNetwork ?: return false
        val caps = cm.getNetworkCapabilities(net) ?: return false
        caps.hasTransport(android.net.NetworkCapabilities.TRANSPORT_CELLULAR) ||
            !caps.hasCapability(android.net.NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
    } catch (_: Exception) {
        false
    }
}

@OptIn(UnstableApi::class, ExperimentalLayoutApi::class)
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

    // Type-first audio detection — used by ActiveVideoTracker to gate
    // PiP, to pick the audio-vs-video layout, and to choose the player
    // backend. The offline-play synthetic ItemFile carries no codec
    // metadata, so a pure null-codec check would misclassify every
    // offline video as audio.
    val codec = ui.item?.files?.firstOrNull()?.video_codec
    val isAudioOnly = when (itemType) {
        "track", "audiobook", "podcast" -> true
        "movie", "episode", "video", "photo" -> false
        null -> codec.isNullOrEmpty()
        else -> codec.isNullOrEmpty()
    }

    // Audio (music / audiobook / podcast) plays through the
    // background-capable PlaybackService via a MediaController, so it
    // survives this screen leaving the back stack and gets lockscreen /
    // Bluetooth / Android Auto controls + the OS now-playing widget.
    // Video keeps a screen-owned ExoPlayer (PiP is its background story).
    val audioController = rememberAudioController(
        enabled = isAudioOnly,
        source = source,
        itemId = itemId,
        item = ui.item,
    )
    val videoPlayer: ExoPlayer? = remember(source, isAudioOnly) {
        if (isAudioOnly) {
            null
        } else ExoPlayer.Builder(context).build().apply {
            // DefaultDataSource dispatches by URI scheme — file://
            // routes to FileDataSource, http(s):// to the wrapped
            // DefaultHttpDataSource. The bare HTTP factory we used
            // before crashed with ClassCastException the first time
            // an offline file:// URL hit it (downloaded items).
            val dsFactory = androidx.media3.datasource.DefaultDataSource.Factory(
                context,
                DefaultHttpDataSource.Factory(),
            )
            // MIME hint when we know the container — helps ExoPlayer
            // pick the right extractor for offline files where the
            // file:// scheme carries no Content-Type header.
            val containerHint = ui.item?.files?.firstOrNull()?.container?.let { c ->
                when (c.lowercase()) {
                    "mp4", "m4v", "m4a", "m4b" -> "video/mp4"
                    "mkv" -> "video/x-matroska"
                    "webm" -> "video/webm"
                    "mp3" -> "audio/mpeg"
                    "flac" -> "audio/flac"
                    "ogg" -> "audio/ogg"
                    "wav" -> "audio/wav"
                    "aac" -> "audio/aac"
                    else -> null
                }
            }
            val mediaSource: MediaSource = when (source) {
                is PlaybackSource.DirectPlay -> {
                    val mediaItem = MediaItem.Builder()
                        .setUri(Uri.parse(source.url))
                        .apply { containerHint?.let { setMimeType(it) } }
                        .build()
                    ProgressiveMediaSource.Factory(dsFactory).createMediaSource(mediaItem)
                }
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
            // Manage audio focus so starting a video pauses music playing
            // in the background through PlaybackService (which also handles
            // focus) — without this the two streams overlap. Also makes
            // video duck/pause for calls and other media, as expected.
            setAudioAttributes(
                androidx.media3.common.AudioAttributes.Builder()
                    .setUsage(androidx.media3.common.C.USAGE_MEDIA)
                    .setContentType(androidx.media3.common.C.AUDIO_CONTENT_TYPE_MOVIE)
                    .build(),
                /* handleAudioFocus = */ true,
            )
            seekTo(startMs)
            prepare()
            playWhenReady = true
        }
    }

    // Unified Player the rest of this screen drives: the screen-owned
    // ExoPlayer for video, or the background MediaController for audio.
    // Null only while the audio controller is still connecting.
    val player: Player = (videoPlayer ?: audioController) ?: run {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(color = Color.White)
        }
        return
    }

    // Tell MainActivity whether to auto-enter PiP on
    // onUserLeaveHint. We mark "playing" only for video so audio
    // playback doesn't collapse to a black PiP window when the
    // user navigates home.
    DisposableEffect(isAudioOnly) {
        ActiveVideoTracker.set(!isAudioOnly)
        onDispose {
            ActiveVideoTracker.set(false)
            // Drop the source-rect hint along with the playing flag —
            // a stale rect would still be applied to setSourceRectHint
            // even when no video is on screen, which can confuse the
            // PiP animation on the next player session.
            ActiveVideoTracker.setSourceRect(null)
        }
    }

    // PiP mode (set by MainActivity.onPictureInPictureModeChanged
    // via the LocalInPipMode composition local). When true, hide
    // every UI affordance — the player surface keeps rendering
    // because Android composites it into the PiP window directly,
    // but overlays / dialogs / the built-in controller would just
    // clutter the floating frame.
    val inPip = LocalInPipMode.current
    // Closing the player ends playback for every item type. The
    // earlier audio-handoff to a MediaSessionService was removed —
    // the simpler "back stops playback" model matches what users
    // expect (and avoided a class of foreground-service-startup
    // crashes the handoff was introducing).
    DisposableEffect(player, isAudioOnly) {
        onDispose {
            // Video: free the screen-owned ExoPlayer. Audio: the
            // MediaController is released by rememberAudioController's
            // awaitDispose, and leaving the screen must NOT stop the
            // background service player.
            if (!isAudioOnly) player.release()
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
    DisposableEffect(player, itemType, nextSibling, isAudioOnly) {
        if (isAudioOnly) {
            // Audio auto-advances inside PlaybackService (it owns the
            // queue + chaining); the screen doesn't drive end-of-track.
            onDispose { }
        } else {
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
    // for cross-device handoff. The terminal "stopped" goes through
    // reportProgressFinal (app-lifetime scope): a track that
    // auto-advances pops this screen — cancelling the VM scope — at
    // the same instant we report, and that's the event the server
    // scrobbles on, so it must outlive the teardown.
    DisposableEffect(itemId, source, isAudioOnly) {
        if (isAudioOnly) {
            // PlaybackService owns 'playing'/'stopped' reporting for
            // audio so it survives this screen — and the whole app —
            // going away while music keeps playing in the background.
            onDispose { }
        } else {
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
                    vm.reportProgressFinal(itemId, pos, player.duration)
                }
            }
        }
    }

    var showAudioPicker by remember { mutableStateOf(false) }
    var showSubtitlePicker by remember { mutableStateOf(false) }
    var showSubtitleStyle by remember { mutableStateOf(false) }
    var showSleepTimer by remember { mutableStateOf(false) }
    var showLyrics by remember { mutableStateOf(false) }
    var showChapters by remember { mutableStateOf(false) }

    // Runtime playback error + buffering state. PlayerViewModel only
    // surfaces prepare-time errors (PlayerUiState.error); a mid-stream
    // ExoPlayer failure (network drop, bad segment) otherwise freezes on
    // a black frame with no feedback. Track both off the player itself.
    var playbackError by remember { mutableStateOf<PlaybackException?>(null) }
    var isBuffering by remember { mutableStateOf(false) }
    DisposableEffect(player) {
        // Seed from the current state so a player that's already
        // buffering/errored when this attaches reflects immediately.
        isBuffering = player.playbackState == Player.STATE_BUFFERING
        playbackError = player.playerError
        val listener = object : Player.Listener {
            override fun onPlayerError(error: PlaybackException) {
                playbackError = error
            }
            override fun onPlaybackStateChanged(playbackState: Int) {
                isBuffering = playbackState == Player.STATE_BUFFERING
                // A successful (re)prepare clears any stale error overlay.
                if (playbackState == Player.STATE_READY) playbackError = null
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }

    // Mirror the built-in PlayerView controller's visibility. The
    // Media3 controller fades in on tap and auto-hides after a few
    // seconds — our overlay toolbar (subtitles / sleep timer / PiP /
    // cast) follows the same edge so it doesn't sit floating on the
    // video frame the whole time. Wired below in the PlayerView
    // factory via setControllerVisibilityListener.
    var controlsVisible by remember { mutableStateOf(true) }
    val sleepTimer by vm.sleepTimer.collectAsState()
    val sleepTimerFired by vm.sleepTimerFired.collectAsState()
    // When the sleep-timer countdown ends, the VM raises the
    // sleepTimerFired edge. We pause the player from the UI side
    // (the VM doesn't reach into ExoPlayer), then ack so the next
    // timer can fire cleanly.
    LaunchedEffect(sleepTimerFired) {
        if (sleepTimerFired) {
            player.pause()
            vm.consumeSleepTimerFired()
        }
    }
    // EndOfTrack mode: subscribe to player state and forward STATE_ENDED
    // to the VM so it can raise the fired edge. Listener removed on
    // composition leave so we don't double-attach.
    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_ENDED) vm.onPlayerEnded()
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }
    val activeAudioIndex = remember { mutableIntStateOf(-1) }

    // Subtitle style is read once per render and pushed into the
    // SubtitleView on every change. The PlayerView reference is held
    // in a remember so the AndroidView factory and the LaunchedEffect
    // both see the same instance.
    val subtitleStyle by vm.subtitleStyle.collectAsState(initial = SubtitleStyle.DEFAULT)
    val playerViewRef = remember { mutableStateOf<PlayerView?>(null) }
    LaunchedEffect(subtitleStyle, playerViewRef.value) {
        playerViewRef.value?.applySubtitleStyle(subtitleStyle)
    }

    // PiP toggles the PlayerView's built-in controller off so the
    // floating window shows the video frame only. Re-enabling on
    // exit restores the seek bar / play-pause without a recompose.
    LaunchedEffect(inPip, playerViewRef.value) {
        playerViewRef.value?.let { pv ->
            pv.useController = !inPip
            if (inPip) pv.hideController()
        }
    }

    AndroidView(
        modifier = Modifier
            .fillMaxSize()
            // Feed the surface bounds to ActiveVideoTracker so
            // MainActivity can hand them to setSourceRectHint on the
            // PiP params. Without this the auto-enter animation
            // doesn't morph out of the player view — see the comment
            // on ActiveVideoTracker.setSourceRect.
            .onGloballyPositioned { coords ->
                val r = coords.boundsInWindow()
                ActiveVideoTracker.setSourceRect(
                    android.graphics.Rect(
                        r.left.toInt(), r.top.toInt(),
                        r.right.toInt(), r.bottom.toInt(),
                    ),
                )
            },
        factory = { ctx ->
            PlayerView(ctx).apply {
                this.player = player
                useController = true
                playerViewRef.value = this
                applySubtitleStyle(subtitleStyle)
                // Mirror the built-in controller's show/hide into our
                // Compose state so the floating toolbar follows the
                // same auto-hide timer instead of staying pinned to
                // the screen during playback.
                setControllerVisibilityListener(
                    PlayerView.ControllerVisibilityListener { visibility ->
                        controlsVisible = visibility == android.view.View.VISIBLE
                    },
                )
                // Hook the built-in TimeBar to drive trickplay
                // previews. The id is part of Media3's public layout —
                // exo_progress is the DefaultTimeBar inside the
                // controller. addListener takes an OnScrubListener
                // whose onScrubMove fires continuously while the user
                // drags, perfect for thumbnail lookup.
                val timeBar = findViewById<androidx.media3.ui.DefaultTimeBar?>(
                    androidx.media3.ui.R.id.exo_progress,
                )
                timeBar?.addListener(object : androidx.media3.ui.TimeBar.OnScrubListener {
                    override fun onScrubStart(timeBar: androidx.media3.ui.TimeBar, position: Long) {
                        vm.onScrubMove(position)
                    }
                    override fun onScrubMove(timeBar: androidx.media3.ui.TimeBar, position: Long) {
                        vm.onScrubMove(position)
                    }
                    override fun onScrubStop(timeBar: androidx.media3.ui.TimeBar, position: Long, canceled: Boolean) {
                        vm.onScrubStop()
                    }
                })
            }
        },
    )

    // Trickplay scrub-preview overlay. Renders above the seekbar when
    // the user is dragging — disappears on scrub-end. Position-only
    // label always shown; bitmap shows once the sprite loads (it's
    // typically near-instant since trickplay sheets are <100 KB JPGs).
    val scrubPreview = ui.scrubPreview
    if (scrubPreview != null && !inPip) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(bottom = 96.dp),
            contentAlignment = Alignment.BottomCenter,
        ) {
            androidx.compose.foundation.layout.Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                modifier = Modifier
                    .background(
                        color = Color(0xCC000000),
                        shape = RoundedCornerShape(6.dp),
                    )
                    .padding(8.dp),
            ) {
                if (scrubPreview.bitmap != null) {
                    androidx.compose.foundation.Image(
                        bitmap = scrubPreview.bitmap.asImageBitmap(),
                        contentDescription = null,
                        modifier = Modifier.width(160.dp),
                    )
                    Spacer(Modifier.height(4.dp))
                }
                Text(
                    text = formatScrubMs(scrubPreview.positionMs),
                    color = Color.White,
                    style = MaterialTheme.typography.labelMedium,
                )
            }
        }
    }

    // Lyrics overlay. Renders only when the user has toggled it on
    // and the VM has lyrics. Polls the player's currentPosition on a
    // short cadence so synced LRC cues land near the active line.
    if (showLyrics && (ui.lyricsCues != null || ui.lyricsPlain != null) && !inPip) {
        var positionMs by remember { mutableStateOf(0L) }
        LaunchedEffect(player) {
            while (isActive) {
                positionMs = player.currentPosition + vm.hlsOffsetMs
                delay(250)
            }
        }
        LyricsOverlay(
            cues = ui.lyricsCues,
            plain = ui.lyricsPlain,
            positionMs = positionMs,
            onDismiss = { showLyrics = false },
        )
    }

    // Skip-intro / skip-credits overlay. Polls the player position
    // on a short cadence and shows the button when position lands
    // inside a marker window. Clicking jumps the player past it.
    if (!inPip) {
        SkipMarkerOverlay(player = player, markers = ui.markers, hlsOffsetMs = vm.hlsOffsetMs)
    }

    // Pin to top-right. Without the wrapping Box + align(TopEnd) the
    // outer PlayerScreen Box's contentAlignment = Center vertically
    // centers the Row, which sits right on top of Media3's centered
    // play / rewind / fast-forward chevrons and steals their taps.
    if (!inPip && controlsVisible) Box(modifier = Modifier.fillMaxSize()) {
      // Back / close — pinned top-left. Otherwise the only way out of the
      // player is the system back gesture, which a tablet/TV in immersive
      // fullscreen can hide entirely, leaving the viewer stuck. Auto-hides
      // with the rest of the controls (gated on controlsVisible).
      IconButton(
          onClick = onClose,
          modifier = Modifier
              .align(Alignment.TopStart)
              .padding(16.dp),
      ) {
          Icon(
              Icons.AutoMirrored.Filled.ArrowBack,
              contentDescription = "Back",
              tint = Color.White,
          )
      }
      // FlowRow so a fully-loaded toolbar (audio + cast + subtitles +
      // style + chapters + lyrics + sleep + PiP) wraps onto a second
      // line on narrow phones instead of clipping the trailing icons.
      FlowRow(
        modifier = Modifier
            .align(Alignment.TopEnd)
            .padding(16.dp),
        horizontalArrangement = Arrangement.End,
    ) {
        if (ui.audioStreams.size > 1) {
            IconButton(onClick = { showAudioPicker = true }) {
                Icon(Icons.Default.Audiotrack, contentDescription = "Audio", tint = Color.White)
            }
        }
        // Cast / Chromecast. The MediaRouteButton is owned by the Cast
        // SDK — it handles device discovery, picker UI, and connection
        // state itself. We layer a separate "Cast this item" tap on
        // top: when the user is connected and presses it, build a LOAD
        // payload from the active item + file and send it to the
        // receiver. Only surfaces when the file is direct-play castable
        // (matches the web isCastable predicate).
        val castFile = ui.item?.files?.firstOrNull()?.let { f ->
            CastMediaInfo.File(
                id = f.id,
                // ItemFile carries no status field — files surfaced
                // through the player are by definition active (the
                // scanner filters missing rows out before the API
                // serialises). Hardcode "active" so isCastable's
                // status gate doesn't reject it.
                status = "active",
                container = f.container,
                videoCodec = f.video_codec,
                audioCodec = f.audio_codec,
                // ItemFile uses ms; CastMediaInfo wants seconds.
                durationSeconds = f.duration_ms?.let { ms -> ms / 1000L },
                streamToken = f.stream_token,
            )
        }
        if (castFile != null && CastMediaInfo.isCastable(castFile)) {
            // The route button itself — Cast SDK draws it. MediaRouteButton's
            // theme helper reads colorBackground from the inflating context
            // and crashes with `background can not be translucent: #0` when
            // that resolves to a transparent value, which is exactly what
            // the Compose-hosted activity context surfaces. Wrap the
            // factory context in a Theme.AppCompat.* theme that has an
            // opaque background so the contrast calculation succeeds.
            AndroidView(
                factory = { ctx ->
                    // MediaRouterThemeHelper reads ?colorBackground at
                    // construction time and throws if it's translucent.
                    // The Compose-only host theme has a #0 value, so
                    // wrap in Theme.OnScreen.MediaRoute (defined in
                    // res/values/themes.xml) which hardcodes an opaque
                    // dark surface for both the framework + AppCompat
                    // attribute namespaces.
                    val themed = android.view.ContextThemeWrapper(
                        ctx,
                        tv.onscreen.mobile.R.style.Theme_OnScreen_MediaRoute,
                    )
                    androidx.mediarouter.app.MediaRouteButton(themed).also {
                        com.google.android.gms.cast.framework.CastButtonFactory
                            .setUpMediaRouteButton(ctx.applicationContext, it)
                    }
                },
                modifier = Modifier.padding(horizontal = 4.dp),
            )
            // "Send this item" — only useful once the user has picked
            // a device via the route button. We don't try to gate on
            // session state here because connect/disconnect is async;
            // the load() call no-ops when there's no session.
            IconButton(
                onClick = {
                    val item = ui.item ?: return@IconButton
                    val origin = vm.serverOrigin() ?: return@IconButton
                    val payload = CastMediaInfo.build(
                        CastMediaInfo.Item(
                            id = item.id,
                            type = item.type,
                            title = item.title,
                            posterPath = item.poster_path,
                            // ItemDetail doesn't yet carry parent_title;
                            // future enhancement: thread show.title for
                            // episodes through the API model so the Cast
                            // metadata gets a "Show · Episode" subtitle.
                            parentTitle = null,
                        ),
                        castFile,
                        origin,
                    ) ?: return@IconButton
                    if (CastSender.load(context, payload)) {
                        // Stop local audio/video so we don't double-play.
                        player.pause()
                    }
                },
            ) {
                Icon(
                    Icons.Default.Send,
                    contentDescription = "Send to Cast",
                    tint = Color.White,
                )
            }
        }
        if (ui.subtitles.isNotEmpty()) {
            IconButton(onClick = { showSubtitlePicker = true }) {
                Icon(Icons.Default.Subtitles, contentDescription = "Subtitles", tint = Color.White)
            }
            // Style adjuster — separate from the track picker because
            // they're orthogonal concerns. Always offered when at
            // least one subtitle track exists; hidden when there's no
            // text to style.
            IconButton(onClick = { showSubtitleStyle = true }) {
                Icon(
                    Icons.Default.FormatSize,
                    contentDescription = "Subtitle style",
                    tint = Color.White,
                )
            }
        }
        // Chapter picker. Only surfaces when the active file carries
        // chapter markers — meaningful for audiobooks (per-chapter
        // jump), some movies, and shows with chapter encoding.
        val chapters = ui.item?.files?.firstOrNull()?.chapters.orEmpty()
        if (chapters.isNotEmpty()) {
            IconButton(onClick = { showChapters = true }) {
                Icon(
                    Icons.Default.Bookmarks,
                    contentDescription = "Chapters",
                    tint = Color.White,
                )
            }
        }

        // Lyrics toggle. Only surfaces when the VM has lyrics for
        // this track (synced or plain). Tap toggles a full-screen
        // overlay that auto-scrolls to the current line for synced
        // lyrics, or scrolls statically for plain.
        if (ui.lyricsCues != null || ui.lyricsPlain != null) {
            IconButton(onClick = { showLyrics = !showLyrics }) {
                Icon(
                    Icons.Default.Lyrics,
                    contentDescription = "Lyrics",
                    tint = if (showLyrics) MaterialTheme.colorScheme.primary else Color.White,
                )
            }
        }

        // Sleep-timer button. Always offered — users want to sleep on
        // music, audiobooks, AND that "one more episode" Netflix
        // habit equally. Active timer shows the remaining countdown
        // as a label on the chip; otherwise just the icon.
        IconButton(onClick = { showSleepTimer = true }) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    Icons.Default.Bedtime,
                    contentDescription = "Sleep timer",
                    tint = if (sleepTimer != null) MaterialTheme.colorScheme.primary else Color.White,
                )
                sleepTimer?.let { st ->
                    if (st.mode is SleepTimer.Minutes) {
                        Spacer(Modifier.width(4.dp))
                        Text(
                            text = SleepTimerMath.formatRemaining(st.remainingMs),
                            color = MaterialTheme.colorScheme.primary,
                            style = MaterialTheme.typography.labelSmall,
                        )
                    }
                }
            }
        }

        // Picture-in-picture only makes sense for video — audio items
        // just end playback when the user backs out. Gate on
        // video_codec so audiobooks and music don't surface a PiP
        // button that would just shrink the album art.
        val hasVideo = ui.item?.files?.firstOrNull()?.video_codec != null
        if (hasVideo && Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            IconButton(onClick = { enterPip(context as? Activity) }) {
                Icon(Icons.Default.PictureInPicture, contentDescription = "Picture in picture", tint = Color.White)
            }
        }
    }
    }

    if (showAudioPicker && !inPip) {
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

    if (showSubtitlePicker && !inPip) {
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

    if (showChapters && !inPip) {
        val activeChapters = ui.item?.files?.firstOrNull()?.chapters.orEmpty()
        ChapterPickerDialog(
            chapters = activeChapters,
            currentPositionMs = player.currentPosition + vm.hlsOffsetMs,
            onSeek = { ms ->
                // Seek into the chapter; the small +1 ms keeps us
                // strictly inside the chapter (matches activeIndex's
                // inclusive-start contract).
                player.seekTo(ms + 1)
                showChapters = false
            },
            onDismiss = { showChapters = false },
        )
    }

    if (showSleepTimer && !inPip) {
        SleepTimerDialog(
            active = sleepTimer,
            onPick = { mode ->
                vm.setSleepTimer(mode)
                showSleepTimer = false
            },
            onDismiss = { showSleepTimer = false },
        )
    }

    if (showSubtitleStyle && !inPip) {
        SubtitleStyleDialog(
            style = subtitleStyle,
            onSize = vm::setSubtitleSize,
            onColor = vm::setSubtitleColor,
            onBackground = vm::setSubtitleBackground,
            onOutline = vm::setSubtitleOutline,
            onDismiss = { showSubtitleStyle = false },
        )
    }

    if (showOnlineSubtitleSearch && !inPip) {
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

    if (showUpNext && nextSibling != null && !inPip) {
        UpNextOverlay(
            title = nextSibling.title,
            onPlay = { onNext(nextSibling.id) },
            onDismiss = { showUpNext = false },
        )
    }

    // Buffering spinner — centered over the (possibly black) frame so a
    // stall reads as "loading" rather than "frozen". Suppressed once an
    // error overlay is up so we don't stack a spinner under it.
    if (isBuffering && playbackError == null && !inPip) {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(color = Color.White)
        }
    }

    // Runtime playback-error overlay. Replaces the frozen black frame
    // with a message + Retry (re-prepares the player at the current
    // position) and Close. Shown even in PiP — a dead PiP window is
    // worse than a small error card.
    playbackError?.let {
        PlaybackErrorOverlay(
            onRetry = {
                playbackError = null
                player.seekToDefaultPosition()
                player.prepare()
                player.playWhenReady = true
            },
            onClose = onClose,
        )
    }
}

@Composable
private fun PlaybackErrorOverlay(
    onRetry: () -> Unit,
    onClose: () -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(0xCC000000))
            .padding(32.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                "Playback error",
                color = Color.White,
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                "Something went wrong playing this item. Check your connection and try again.",
                color = Color.White.copy(alpha = 0.8f),
                style = MaterialTheme.typography.bodyMedium,
            )
            Spacer(Modifier.height(16.dp))
            Row {
                Button(onClick = onRetry) { Text("Retry") }
                Spacer(Modifier.width(8.dp))
                TextButton(onClick = onClose) {
                    Text("Close", color = Color.White)
                }
            }
        }
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
            // These vertical gaps were Spacer(width=0) — zero-height in a
            // Column, so the label / title / buttons stacked flush. Give
            // them real height.
            Spacer(Modifier.height(4.dp))
            Text(title, color = Color.White, style = MaterialTheme.typography.titleMedium)
            Spacer(Modifier.height(12.dp))
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
    player: Player,
    vm: PlayerViewModel,
) {
    val stream = streams.getOrNull(idx) ?: return
    if (source is PlaybackSource.Hls) {
        // Transcoded HLS sessions carry a single audio stream — to
        // switch we re-issue the session at the current position with
        // a new audio_stream_index. Direct play falls through to
        // ExoPlayer's track selector which sees every audio track in
        // the source container.
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
    player: Player,
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
    player: Player,
    markers: List<Marker>,
    hlsOffsetMs: Long,
) {
    if (markers.isEmpty()) return

    var activeMarker by remember { mutableStateOf<Marker?>(null) }
    // Session-scoped dismissed-marker set. A user who long-presses
    // the Skip button (or taps the × next to it) is saying "I want to
    // watch this; don't show me the button for this marker again."
    // Common case: post-credits scenes the user actually wants to see.
    // Keyed by SkipMarkers.markerKey so two markers at the same position
    // (intro + credits on a clip-show episode) don't shadow each other.
    val dismissed = remember { mutableStateOf(setOf<String>()) }

    LaunchedEffect(markers) {
        while (isActive) {
            val contentPos = player.currentPosition + hlsOffsetMs
            activeMarker = SkipMarkers.activeAt(markers, contentPos, dismissed.value)
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
        Row(verticalAlignment = Alignment.CenterVertically) {
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
            Spacer(Modifier.width(8.dp))
            // × to dismiss for the session. Smaller hit target than the
            // Skip button by design — accidental dismissals are worse
            // than accidental skips.
            IconButton(
                onClick = {
                    dismissed.value = dismissed.value + SkipMarkers.markerKey(marker)
                    activeMarker = null
                },
                modifier = Modifier
                    .background(
                        color = Color(0x66000000),
                        shape = RoundedCornerShape(50),
                    ),
            ) {
                Text("×", color = Color.White, style = MaterialTheme.typography.labelLarge)
            }
        }
    }
}

/**
 * Connect a [MediaController] to [PlaybackService] for audio and hand it
 * the current item, so music plays in the background — surviving this
 * screen leaving the back stack — with lockscreen / Bluetooth / Android
 * Auto controls + the OS now-playing widget. Returns null while
 * connecting, or for non-audio items ([enabled] = false).
 *
 * Only the controller (not the service player) is released when this
 * leaves the composition, so backing out keeps audio playing. Re-entering
 * for the item already playing in the service binds without restarting.
 * Album art comes from the service player's extracted metadata, so no
 * artworkUri is set here.
 */
@Composable
private fun rememberAudioController(
    enabled: Boolean,
    source: PlaybackSource,
    itemId: String,
    item: ItemDetail?,
): MediaController? {
    val context = LocalContext.current
    return produceState<MediaController?>(initialValue = null, enabled, source, itemId) {
        if (!enabled || source !is PlaybackSource.DirectPlay) {
            value = null
            return@produceState
        }
        val token = SessionToken(context, ComponentName(context, PlaybackService::class.java))
        val future = MediaController.Builder(context, token).buildAsync()
        val controller: MediaController? = try {
            suspendCancellableCoroutine { cont ->
                future.addListener(
                    { cont.resumeWith(Result.success(runCatching { future.get() }.getOrNull())) },
                    ContextCompat.getMainExecutor(context),
                )
            }
        } catch (_: Exception) {
            null
        }
        if (controller == null) {
            value = null
            return@produceState
        }

        // Don't restart the track that's already playing in the service
        // when the user re-opens the now-playing screen.
        if (controller.currentMediaItem?.mediaId != itemId) {
            val extras = Bundle().apply {
                item?.type?.let { putString(PlaybackService.EXTRA_TYPE, it) }
                item?.parent_id?.let { putString(PlaybackService.EXTRA_PARENT_ID, it) }
                item?.index?.let { putInt(PlaybackService.EXTRA_INDEX, it) }
            }
            val mediaItem = MediaItem.Builder()
                .setUri(Uri.parse(source.url))
                .setMediaId(itemId)
                .setMediaMetadata(
                    MediaMetadata.Builder()
                        .setTitle(item?.title)
                        .setExtras(extras)
                        .build(),
                )
                .build()
            controller.setMediaItem(mediaItem, source.startMs)
            controller.prepare()
            controller.playWhenReady = true
        }
        value = controller
        awaitDispose { controller.release() }
    }.value
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
        // than crash.
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

/** Format a millisecond position as `H:MM:SS` (or `MM:SS` when under
 *  one hour). Used by the trickplay scrub-preview overlay so the label
 *  reads `1:23:45` not `5025000ms`. */
private fun formatScrubMs(ms: Long): String {
    val totalSec = ms / 1000
    val h = totalSec / 3600
    val m = (totalSec % 3600) / 60
    val s = totalSec % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
}

private fun formatSubtitleLabel(s: SubtitleStream): String {
    val parts = mutableListOf<String>()
    if (s.language.isNotEmpty()) parts += s.language
    if (s.title.isNotEmpty()) parts += s.title
    if (s.forced) parts += "forced"
    return parts.joinToString(" · ")
}

/**
 * Subtitle styling dialog. Mirrors the web client's panel: four pickers
 * (size, colour, background, outline) each driving a one-shot setter on
 * the VM that persists to DataStore. Style change reflects immediately
 * because [PlayerHost] subscribes to the prefs flow and re-applies on
 * each emission.
 */
@Composable
private fun SubtitleStyleDialog(
    style: SubtitleStyle,
    onSize: (SubtitleStyle.Size) -> Unit,
    onColor: (SubtitleStyle.TextColor) -> Unit,
    onBackground: (SubtitleStyle.Background) -> Unit,
    onOutline: (SubtitleStyle.Outline) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text("Subtitle style") },
        text = {
            Column {
                StylePickerRow(
                    label = "Size",
                    options = SubtitleStyle.Size.values().toList(),
                    active = style.size,
                    labelOf = { it.name.lowercase().replaceFirstChar(Char::titlecase) },
                    onPick = onSize,
                )
                StylePickerRow(
                    label = "Color",
                    options = SubtitleStyle.TextColor.values().toList(),
                    active = style.color,
                    labelOf = { it.name.lowercase().replaceFirstChar(Char::titlecase) },
                    onPick = onColor,
                )
                StylePickerRow(
                    label = "Background",
                    options = SubtitleStyle.Background.values().toList(),
                    active = style.background,
                    labelOf = { it.name.lowercase().replaceFirstChar(Char::titlecase) },
                    onPick = onBackground,
                )
                StylePickerRow(
                    label = "Outline",
                    options = SubtitleStyle.Outline.values().toList(),
                    active = style.outline,
                    labelOf = { it.name.lowercase().replaceFirstChar(Char::titlecase) },
                    onPick = onOutline,
                )
            }
        },
    )
}

@Composable
private fun <T> StylePickerRow(
    label: String,
    options: List<T>,
    active: T,
    labelOf: (T) -> String,
    onPick: (T) -> Unit,
) {
    Column(modifier = Modifier.padding(vertical = 6.dp)) {
        Text(label, style = MaterialTheme.typography.labelLarge)
        Spacer(Modifier.height(4.dp))
        Row {
            options.forEach { option ->
                val isActive = option == active
                TextButton(
                    onClick = { onPick(option) },
                ) {
                    Text(
                        text = labelOf(option),
                        // Active token gets the accent colour; the rest
                        // stay onSurfaceVariant so the row reads as
                        // "click any to change", not "this one's
                        // disabled".
                        color = if (isActive) MaterialTheme.colorScheme.primary
                            else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

/**
 * Chapter list picker. Audiobooks especially benefit from this — a
 * 12-hour book with 24 chapters needs a "jump to chapter 7" surface
 * that the seekbar alone can't deliver. Active chapter is highlighted
 * in the primary colour; tap any row to seek there.
 */
@Composable
private fun ChapterPickerDialog(
    chapters: List<tv.onscreen.mobile.data.model.Chapter>,
    currentPositionMs: Long,
    onSeek: (Long) -> Unit,
    onDismiss: () -> Unit,
) {
    val activeIdx = remember(chapters, currentPositionMs) {
        ChapterNav.activeIndex(chapters, currentPositionMs)
    }
    val listState = rememberLazyListState()
    // Auto-scroll so the user sees their current chapter on open.
    LaunchedEffect(activeIdx) {
        if (activeIdx >= 0) {
            val target = (activeIdx - 2).coerceAtLeast(0)
            listState.animateScrollToItem(target)
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text("Chapters") },
        text = {
            LazyColumn(state = listState) {
                itemsIndexed(chapters) { idx, chapter ->
                    val isActive = idx == activeIdx
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onSeek(chapter.start_ms) }
                            .padding(vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = ChapterNav.formatStart(chapter.start_ms),
                            style = MaterialTheme.typography.labelMedium,
                            color = if (isActive) MaterialTheme.colorScheme.primary
                                else MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.width(72.dp),
                        )
                        Text(
                            text = ChapterNav.displayTitle(chapter, idx),
                            style = MaterialTheme.typography.bodyMedium,
                            color = if (isActive) MaterialTheme.colorScheme.primary
                                else MaterialTheme.colorScheme.onSurface,
                        )
                    }
                }
            }
        },
    )
}

/**
 * Synced + plain lyrics overlay. When [cues] is non-null the renderer
 * highlights the cue covering [positionMs] (LRC line-by-line); when
 * only [plain] is present we show a static scroll. Tap-anywhere
 * dismisses so the overlay never traps the user.
 */
@Composable
private fun LyricsOverlay(
    cues: List<tv.onscreen.mobile.lyrics.LrcParser.Cue>?,
    plain: String?,
    positionMs: Long,
    onDismiss: () -> Unit,
) {
    val activeIndex = remember(cues, positionMs) {
        if (cues.isNullOrEmpty()) -1
        else {
            val active = tv.onscreen.mobile.lyrics.LrcParser.cueAt(cues, positionMs)
            if (active != null) cues.indexOf(active) else -1
        }
    }
    val listState = rememberLazyListState()
    // Auto-scroll to keep the active line near the top third of the
    // overlay. Skip when there's no active line yet (intro before
    // first cue) or when the user is dragging — they probably want
    // to read ahead.
    LaunchedEffect(activeIndex) {
        if (activeIndex >= 0) {
            // Place the active cue ~3 lines down so the user sees a
            // bit of context above (just-finished line).
            val target = (activeIndex - 3).coerceAtLeast(0)
            listState.animateScrollToItem(target)
        }
    }
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(0xE6000000))
            .clickable(onClick = onDismiss),
        contentAlignment = Alignment.Center,
    ) {
        if (!cues.isNullOrEmpty()) {
            LazyColumn(
                state = listState,
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 24.dp, vertical = 96.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                itemsIndexed(cues) { idx, cue ->
                    val isActive = idx == activeIndex
                    Text(
                        text = if (cue.text.isEmpty()) "♪" else cue.text,
                        color = if (isActive) Color.White
                            else Color(0x99FFFFFF),
                        style = if (isActive) MaterialTheme.typography.titleMedium
                            else MaterialTheme.typography.bodyMedium,
                    )
                }
            }
        } else if (plain != null) {
            // Plain-text fallback: simple scrollable column. No
            // line-by-line highlight since we have no timing.
            val scroll = rememberScrollState()
            Text(
                text = plain,
                color = Color.White,
                style = MaterialTheme.typography.bodyLarge,
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 24.dp, vertical = 96.dp)
                    .verticalScroll(scroll),
            )
        }
    }
}

/**
 * Sleep-timer picker. Quick-pick chips for the common minute durations
 * plus an "End of track" chip and an "Off" cancel. Active selection
 * is highlighted with the primary colour. Dismiss without picking
 * leaves the timer unchanged.
 */
@Composable
private fun SleepTimerDialog(
    active: SleepTimerState?,
    onPick: (SleepTimer) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text("Sleep timer") },
        text = {
            Column {
                if (active != null) {
                    Text(
                        text = "Active: " + when (active.mode) {
                            is SleepTimer.Minutes -> SleepTimerMath.formatRemaining(active.remainingMs)
                            SleepTimer.EndOfTrack -> "End of track"
                            SleepTimer.Off -> "Off"
                        },
                        style = MaterialTheme.typography.labelLarge,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Spacer(Modifier.height(8.dp))
                }
                // Quick-pick row. FlowRow would be ideal here; the
                // pickers fit on one phone-portrait line so a regular
                // Row keeps things terse.
                Row {
                    SleepTimerMath.QUICK_PICKS_MIN.forEach { mins ->
                        val isActive = (active?.mode as? SleepTimer.Minutes)?.total == mins
                        TextButton(onClick = { onPick(SleepTimer.Minutes(mins)) }) {
                            Text(
                                text = "${mins}m",
                                color = if (isActive) MaterialTheme.colorScheme.primary
                                    else MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
                Row {
                    val isEot = active?.mode == SleepTimer.EndOfTrack
                    TextButton(onClick = { onPick(SleepTimer.EndOfTrack) }) {
                        Text(
                            "End of track",
                            color = if (isEot) MaterialTheme.colorScheme.primary
                                else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    TextButton(onClick = { onPick(SleepTimer.Off) }) {
                        Text("Off", color = MaterialTheme.colorScheme.error)
                    }
                }
            }
        },
    )
}

