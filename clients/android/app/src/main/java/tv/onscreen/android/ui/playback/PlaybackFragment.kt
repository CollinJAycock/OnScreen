package tv.onscreen.android.ui.playback

import android.app.AlertDialog
import android.net.Uri
import android.os.Bundle
import android.view.Gravity
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.FrameLayout
import android.widget.TextView
import androidx.leanback.app.VideoSupportFragment
import androidx.leanback.app.VideoSupportFragmentGlueHost
import androidx.leanback.media.PlaybackTransportControlGlue
import androidx.leanback.widget.Action
import androidx.leanback.widget.ArrayObjectAdapter
import androidx.leanback.widget.PlaybackControlsRow
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.ui.leanback.LeanbackPlayerAdapter
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.AudioStream
import tv.onscreen.android.data.model.Chapter
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.SubtitleStream
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.NotificationsRepository
import android.widget.Toast
import tv.onscreen.android.data.repository.OnlineSubtitleRepository
import tv.onscreen.android.data.repository.TrickplayRepository
import javax.inject.Inject
import kotlin.math.abs

@AndroidEntryPoint
@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
class PlaybackFragment : VideoSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var notificationsRepo: NotificationsRepository
    @Inject lateinit var trickplayRepo: TrickplayRepository
    @Inject lateinit var onlineSubtitleRepo: OnlineSubtitleRepository

    private lateinit var viewModel: PlaybackViewModel
    private var player: ExoPlayer? = null
    private var progressTracker: ProgressTracker? = null
    private var glue: PlaybackTransportControlGlue<LeanbackPlayerAdapter>? = null

    private var audioStreams: List<AudioStream> = emptyList()
    private var subtitleStreams: List<SubtitleStream> = emptyList()
    // Index of the currently-active audio stream within audioStreams
    // (-1 = default, server picks). Tracked here because in HLS
    // playback the active track isn't observable from ExoPlayer
    // (the server emitted only one), so we need our own state to
    // mark the radio button in the picker.
    private var activeAudioIndex: Int = -1
    // Source mode of the current playback session — drives whether
    // the audio picker can use ExoPlayer's track selector
    // (direct play) or has to re-issue the transcode session
    // (HLS / remux). null until the first source emission.
    private var currentSource: PlaybackSource? = null
    private var nextEpisode: ChildItem? = null
    private var serverUrl: String = ""

    private var upNextOverlay: View? = null
    private var upNextJob: Job? = null
    private var upNextShown = false

    private var audioAction: Action? = null
    private var subtitleAction: Action? = null
    private var chaptersAction: Action? = null
    private var speedAction: Action? = null
    private var rewindAction: PlaybackControlsRow.RewindAction? = null
    private var fastForwardAction: PlaybackControlsRow.FastForwardAction? = null
    private var chapters: List<Chapter> = emptyList()
    private var currentItemType: String = ""
    private var playbackSpeed: Float = 1.0f

    /** Cross-device sync subscriber. Cancelled in onDestroyView. */
    private var syncJob: Job? = null

    /** Trickplay-thumbnail load. Single job because installation is
     *  one-shot per session. */
    private var trickplayJob: Job? = null
    /** True for the first source emission after a parked-player
     *  re-take, so we don't restart playback when the user comes
     *  back to a track that's already playing in the service. */
    private var playerWasReused: Boolean = false

    /** Skip-intro / skip-credits overlay button. Inflated lazily on
     *  first marker hit, then shown/hidden as the player crosses
     *  marker windows. */
    private var skipMarkerOverlay: Button? = null
    private var skipMarkerJob: Job? = null
    private var markers: List<tv.onscreen.android.data.model.Marker> = emptyList()
    /** Per-marker dismissal: once the user clicks Skip (or the credits
     *  overlay's auto-disappear fires past end_ms), don't re-show that
     *  same marker for the rest of this playback session. Keyed by
     *  start_ms because it's stable across the marker list. */
    private val dismissedMarkers = mutableSetOf<Long>()

    companion object {
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_START_MS = "start_ms"
        private const val UPDATE_PERIOD_MS = 1000
        private const val ACTION_AUDIO_ID = 100L
        private const val ACTION_SUBTITLE_ID = 101L
        private const val ACTION_CHAPTERS_ID = 102L
        private const val ACTION_SPEED_ID = 103L
        private val SPEED_OPTIONS = floatArrayOf(0.75f, 1.0f, 1.25f, 1.5f, 1.75f, 2.0f)
        // Skip-back / skip-forward step in milliseconds. 10 s back is
        // the conventional "I missed that line" jump; 30 s forward
        // matches the audiobook / podcast convention and most TV
        // remotes' dedicated FastForward button feel.
        private const val SKIP_BACK_MS = 10_000L
        private const val SKIP_FORWARD_MS = 30_000L
        private const val UP_NEXT_COUNTDOWN_SEC = 10
        private const val UP_NEXT_LEAD_SEC = 25

        fun newInstance(itemId: String, startMs: Long = 0): PlaybackFragment {
            return PlaybackFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_ITEM_ID, itemId)
                    putLong(ARG_START_MS, startMs)
                }
            }
        }
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[PlaybackViewModel::class.java]

        // Explicit hardware-media-key handler so Google's TV app
        // quality requirement TV-PP (toggle play/pause on
        // KEYCODE_MEDIA_PLAY_PAUSE) holds without depending on
        // Leanback's transport controls being focused. ExoPlayer +
        // MediaSession typically catch these via MediaButtonReceiver,
        // but registering an explicit listener is defensive belt-and-
        // braces — remotes whose media keys arrive as
        // KeyEvents-only (without a media-button intent) still work.
        // Rewind / fast-forward keys reuse the existing seekRelative
        // helper so the step size matches the on-screen actions.
        // D-pad center is intentionally NOT intercepted here — the
        // Leanback overlay handles "press to show, second press to
        // activate" which is the standard Android TV UX.
        view.isFocusableInTouchMode = true
        view.setOnKeyListener { _, keyCode, event ->
            if (event.action != android.view.KeyEvent.ACTION_DOWN) {
                return@setOnKeyListener false
            }
            when (keyCode) {
                android.view.KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> { togglePlayPause(); true }
                android.view.KeyEvent.KEYCODE_MEDIA_PLAY       -> { player?.play(); true }
                android.view.KeyEvent.KEYCODE_MEDIA_PAUSE      -> { player?.pause(); true }
                android.view.KeyEvent.KEYCODE_MEDIA_STOP       -> { player?.pause(); true }
                android.view.KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> { seekRelative(SKIP_FORWARD_MS); true }
                android.view.KeyEvent.KEYCODE_MEDIA_REWIND       -> { seekRelative(-SKIP_BACK_MS); true }
                else -> false
            }
        }

        val itemId = arguments?.getString(ARG_ITEM_ID) ?: return
        val startMs = arguments?.getLong(ARG_START_MS, 0) ?: 0

        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""

            initPlayer()
            viewModel.prepare(itemId, startMs, serverUrl)

            viewModel.uiState.collectLatest { state ->
                if (state.error != null) {
                    showErrorDialog(state.error)
                    return@collectLatest
                }
                val source = state.source ?: return@collectLatest
                audioStreams = state.audioStreams
                subtitleStreams = state.subtitles
                nextEpisode = state.nextEpisode
                currentSource = source

                // Skip the first prepare() when we re-took a player
                // already playing this item from the
                // MediaSessionService — calling setMediaSource
                // would restart the song from 0:00. The transcode
                // session and HLS playlist URL haven't changed
                // since the original prepare, so the existing
                // ExoPlayer state is still correct.
                if (playerWasReused) {
                    playerWasReused = false
                } else {
                    playSource(source)
                }
                applyPreferredTracks(state.preferredAudioLang, state.preferredSubtitleLang)

                val tracker = ProgressTracker(viewLifecycleOwner.lifecycleScope, itemRepo)
                tracker.positionProvider = { player?.currentPosition ?: 0L }
                tracker.durationProvider = {
                    val dur = player?.duration ?: 0L
                    if (dur <= 0 || dur == Long.MAX_VALUE) {
                        state.item?.duration_ms ?: state.item?.files?.firstOrNull()?.duration_ms ?: 0L
                    } else dur
                }
                tracker.updateOffset(viewModel.hlsOffsetMs)
                tracker.start(itemId, viewModel.hlsOffsetMs)
                progressTracker = tracker

                glue?.title = state.item?.title ?: ""
                glue?.subtitle = state.item?.year?.toString() ?: ""

                markers = state.markers
                dismissedMarkers.clear()
                chapters = state.item?.files?.firstOrNull()?.chapters ?: emptyList()
                currentItemType = state.item?.type.orEmpty()

                refreshSecondaryActions()
                startUpNextWatcher()
                startCrossDeviceSync(itemId)
                startSkipMarkerWatcher()
                installTrickplaySeekProvider(itemId)
                bindAudioBackdrop(state.item)
            }
        }
    }

    /**
     * If the server has trickplay thumbnails generated for this item,
     * install a [TrickplaySeekProvider] on the playback glue so the
     * seek bar shows preview images as the user scrubs. Best-effort:
     * any failure (no trickplay generated, .vtt parse error, network)
     * silently leaves the seek bar in its plain position-only mode.
     *
     * Runs in the background after the player is set up — installing
     * the provider mid-session is supported by Leanback (it just
     * upgrades the seek-bar's interaction the next time the user
     * scrubs).
     */
    /** Audio playback gets a music-Now-Playing-style backdrop: a
     *  full-bleed blurred album fanart layer plus a centered cover-art
     *  card. Video items keep the surface view as-is (the album-art
     *  layer is removed). The Leanback transport controls overlay both
     *  cases the same way; this is purely visual.
     *
     *  We attach the overlay as a sibling of the existing
     *  VideoSupportFragment view — the surface view sits at the
     *  bottom of the z-order and our album-cover ImageView paints on
     *  top of it. Controls draw above both. */
    /** Aspect ratio of the active video for picture-in-picture, or
     *  null when audio. PictureInPictureParams accepts ratios
     *  roughly between 0.42 and 2.39; we coerce to a safe default
     *  rather than throw if the player hasn't reported a video size
     *  yet. */
    fun activePiPAspect(): Pair<Int, Int>? {
        if (currentItemType == "track" || currentItemType == "audiobook") return null
        val exo = player ?: return null
        val w = exo.videoSize.width.takeIf { it > 0 } ?: 16
        val h = exo.videoSize.height.takeIf { it > 0 } ?: 9
        return w to h
    }

    /** Toggle the Leanback transport overlay based on PiP state. The
     *  controls would draw across the small floating window awkwardly
     *  and consume D-pad focus when the PiP window doesn't have
     *  input focus — better to hide them entirely and let the system
     *  PiP chrome handle play/pause. */
    fun onPiPModeChanged(inPiP: Boolean) {
        glue?.host?.let { host ->
            if (inPiP) host.hideControlsOverlay(false) else host.showControlsOverlay(false)
        }
    }

    private fun bindAudioBackdrop(item: tv.onscreen.android.data.model.ItemDetail?) {
        val root = view as? android.view.ViewGroup ?: return
        val isAudio = currentItemType == "track" || currentItemType == "audiobook"
        val existing = root.findViewWithTag<android.view.View>("audio_backdrop")
        if (!isAudio || item == null) {
            if (existing != null) root.removeView(existing)
            return
        }
        val artPath = item.poster_path ?: item.fanart_path
        if (artPath.isNullOrEmpty()) {
            if (existing != null) root.removeView(existing)
            return
        }

        val ctx = requireContext()
        val container = if (existing != null) {
            existing as android.widget.FrameLayout
        } else {
            val frame = android.widget.FrameLayout(ctx).apply {
                tag = "audio_backdrop"
                layoutParams = android.widget.FrameLayout.LayoutParams(
                    android.widget.FrameLayout.LayoutParams.MATCH_PARENT,
                    android.widget.FrameLayout.LayoutParams.MATCH_PARENT,
                )
                setBackgroundColor(android.graphics.Color.BLACK)
            }
            // Full-bleed darkened backdrop (uses the same poster, dimmed).
            android.widget.ImageView(ctx).apply {
                layoutParams = android.widget.FrameLayout.LayoutParams(
                    android.widget.FrameLayout.LayoutParams.MATCH_PARENT,
                    android.widget.FrameLayout.LayoutParams.MATCH_PARENT,
                )
                scaleType = android.widget.ImageView.ScaleType.CENTER_CROP
                imageAlpha = 60
                tag = "audio_backdrop_bg"
                frame.addView(this)
            }
            // Centered cover card.
            android.widget.ImageView(ctx).apply {
                val density = ctx.resources.displayMetrics.density
                val side = (320 * density).toInt()
                layoutParams = android.widget.FrameLayout.LayoutParams(side, side).apply {
                    gravity = android.view.Gravity.CENTER
                }
                scaleType = android.widget.ImageView.ScaleType.CENTER_CROP
                tag = "audio_backdrop_cover"
                elevation = 12f * density
                frame.addView(this)
            }
            // Insert the backdrop as the first child so the existing
            // surface + transport stay on top in the z-order.
            root.addView(frame, 0)
            frame
        }

        val bg = container.findViewWithTag<android.widget.ImageView>("audio_backdrop_bg")
        val cover = container.findViewWithTag<android.widget.ImageView>("audio_backdrop_cover")
        val url = tv.onscreen.android.data.artworkUrl(serverUrl, artPath, width = 800)
        bg?.let { coil.Coil.imageLoader(ctx).enqueue(coil.request.ImageRequest.Builder(ctx).data(url).target(it).build()) }
        cover?.let { coil.Coil.imageLoader(ctx).enqueue(coil.request.ImageRequest.Builder(ctx).data(url).target(it).build()) }
    }

    private fun installTrickplaySeekProvider(itemId: String) {
        trickplayJob?.cancel()
        trickplayJob = viewLifecycleOwner.lifecycleScope.launch {
            val status = trickplayRepo.status(itemId)
            if (status.status != "done") return@launch
            val cues = trickplayRepo.fetchCues(itemId) ?: return@launch
            if (cues.isEmpty()) return@launch
            val provider = TrickplaySeekProvider(
                itemId = itemId,
                cues = cues,
                repo = trickplayRepo,
                scope = viewLifecycleOwner.lifecycleScope,
                hlsOffsetMs = viewModel.hlsOffsetMs,
            )
            glue?.seekProvider = provider
        }
    }

    /**
     * Watch the player's content position and surface a "SKIP INTRO" /
     * "SKIP CREDITS" button when it falls inside a marker window.
     * The button stays visible for the full window or until the user
     * clicks it (then jumps to end_ms) or arrows away from it.
     *
     * `dismissedMarkers` prevents re-showing the same window if the
     * user passes through it manually or the auto-hide fires — without
     * it, scrubbing back inside the intro after dismissal would pop
     * the overlay again, which is annoying mid-rewatch.
     *
     * HLS sessions: positions are translated content-time → player-time
     * via `viewModel.hlsOffsetMs` so the markers (server-side
     * content-time) line up regardless of which transcode window is
     * loaded.
     */
    private fun startSkipMarkerWatcher() {
        skipMarkerJob?.cancel()
        if (markers.isEmpty()) return
        skipMarkerJob = viewLifecycleOwner.lifecycleScope.launch {
            while (isActive) {
                delay(500)
                val exo = player ?: continue
                if (!exo.isPlaying) {
                    // Hide the overlay during pause — when playback
                    // resumes, the position check kicks back in.
                    hideSkipMarker()
                    continue
                }
                val contentMs = exo.currentPosition + viewModel.hlsOffsetMs
                val active = markers.firstOrNull { m ->
                    contentMs >= m.start_ms && contentMs < m.end_ms &&
                        m.start_ms !in dismissedMarkers
                }
                if (active != null) {
                    showSkipMarker(active)
                } else {
                    hideSkipMarker()
                }
            }
        }
    }

    private fun showSkipMarker(marker: tv.onscreen.android.data.model.Marker) {
        val rootContainer = (view as? ViewGroup) ?: return
        val overlay = skipMarkerOverlay ?: run {
            val btn = LayoutInflater.from(requireContext())
                .inflate(R.layout.overlay_skip_marker, rootContainer, false) as Button
            val lp = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
            ).apply {
                gravity = Gravity.BOTTOM or Gravity.END
                bottomMargin = 80
                rightMargin = 60
            }
            btn.layoutParams = lp
            rootContainer.addView(btn)
            skipMarkerOverlay = btn
            btn
        }
        val labelRes = if (marker.kind == "credits") R.string.skip_credits else R.string.skip_intro
        overlay.setText(labelRes)
        overlay.setOnClickListener {
            dismissedMarkers.add(marker.start_ms)
            val targetPlayerMs = (marker.end_ms - viewModel.hlsOffsetMs).coerceAtLeast(0)
            player?.seekTo(targetPlayerMs)
            hideSkipMarker()
        }
        if (overlay.visibility != View.VISIBLE) {
            overlay.visibility = View.VISIBLE
            overlay.requestFocus()
        }
    }

    private fun hideSkipMarker() {
        skipMarkerOverlay?.visibility = View.GONE
    }

    /**
     * Subscribe to `progress.updated` SSE events for the current item.
     * When another of the user's devices reports new progress AND local
     * playback is paused/idle, seek to the new position so a tap on Play
     * picks up where the other device left off.
     *
     * Skipped during local active playback — the user driving this device
     * has authoritative position. Self-loop guard ignores echoes within
     * 2 s of our own most recent saveProgress. HLS sessions translate
     * content-time → player-time via the captured offset; positions
     * outside the loaded playlist range bail (a fresh transcode on the
     * next launch will pick up the new resume position from the
     * server-side store anyway).
     */
    private fun startCrossDeviceSync(itemId: String) {
        syncJob?.cancel()
        syncJob = viewLifecycleOwner.lifecycleScope.launch {
            // Reconnect loop matches the notifications-list pattern —
            // one underlying SSE per subscriber, restart on completion.
            while (isActive) {
                try {
                    notificationsRepo.subscribeProgressUpdates().collect { evt ->
                        if (evt.item_id != itemId) return@collect
                        val exo = player ?: return@collect
                        // Don't fight the local user mid-playback.
                        if (exo.isPlaying) return@collect
                        // Self-loop guard: every saveProgress on this
                        // device round-trips back as a sync event.
                        val tracker = progressTracker
                        val lastSelf = tracker?.lastReportedContentMs ?: -1L
                        if (lastSelf >= 0 && abs(evt.position_ms - lastSelf) < 2000L) {
                            return@collect
                        }
                        val playerPos = evt.position_ms - viewModel.hlsOffsetMs
                        val dur = exo.duration
                        if (playerPos < 0 || (dur > 0 && dur != Long.MAX_VALUE && playerPos > dur)) {
                            // Sync position is outside the currently-loaded
                            // session. Skip: the new position is already
                            // committed server-side, so the next playback
                            // start will pick it up via item.view_offset_ms.
                            return@collect
                        }
                        exo.seekTo(playerPos)
                    }
                } catch (_: Exception) {
                    // Stream dropped; reconnect after a short delay.
                }
                delay(5_000)
            }
        }
    }

    private fun initPlayer() {
        // Re-entry handoff: if the user backed out of this same item
        // while music was playing, the previous fragment instance
        // parked the player in AudioHandoff and the
        // MediaSessionService picked it up. Take it back here so
        // playback continues seamlessly under the Leanback transport
        // controls — without this we'd build a fresh ExoPlayer and
        // run two parallel sessions for the duration of the new
        // fragment's life.
        val itemId = arguments?.getString(ARG_ITEM_ID)
        val parked = itemId?.let { tv.onscreen.android.playback.AudioHandoff.take(it) }
        val exo = parked ?: buildExoPlayer()
        if (parked != null) {
            // Reused-parked-player flag suppresses the next
            // setMediaSource/prepare on the first state emission so
            // the song doesn't restart from 0:00 when the user
            // re-enters mid-track. Cleared after the first source
            // arrives.
            playerWasReused = true
            // Service is about to be empty — stop it so the foreground
            // notification disappears now that the fragment owns the
            // player again.
            try {
                requireContext().applicationContext.stopService(
                    android.content.Intent(
                        requireContext(),
                        tv.onscreen.android.playback.OnScreenMediaSessionService::class.java,
                    ),
                )
            } catch (_: Exception) { }
        }
        // Local wake lock — keeps the CPU alive during playback so the
        // ExoPlayer worker thread isn't paused. The screen-on flag is
        // toggled separately in onIsPlayingChanged below.
        exo.setWakeMode(androidx.media3.common.C.WAKE_MODE_LOCAL)
        player = exo

        val adapter = LeanbackPlayerAdapter(requireContext(), exo, UPDATE_PERIOD_MS)
        val host = VideoSupportFragmentGlueHost(this)

        glue = object : PlaybackTransportControlGlue<LeanbackPlayerAdapter>(requireContext(), adapter) {
            override fun onCreatePrimaryActions(adapter: ArrayObjectAdapter) {
                // Order: Rewind, [PlayPause inserted by super], FastForward.
                // PlaybackTransportControlGlue inserts its own play/pause
                // action ahead of whatever we add here, so the rendered
                // row ends up [Rewind] [PlayPause] [FastForward] which
                // matches the conventional TV remote layout.
                rewindAction = PlaybackControlsRow.RewindAction(requireContext())
                fastForwardAction = PlaybackControlsRow.FastForwardAction(requireContext())
                adapter.add(rewindAction)
                super.onCreatePrimaryActions(adapter)
                adapter.add(fastForwardAction)
            }

            override fun onCreateSecondaryActions(adapter: ArrayObjectAdapter) {
                super.onCreateSecondaryActions(adapter)
                audioAction = Action(ACTION_AUDIO_ID, getString(R.string.audio))
                subtitleAction = Action(ACTION_SUBTITLE_ID, getString(R.string.subtitles))
                chaptersAction = Action(ACTION_CHAPTERS_ID, getString(R.string.chapters))
                speedAction = Action(ACTION_SPEED_ID, getString(R.string.speed_label, "1.0"))
                adapter.add(audioAction)
                adapter.add(subtitleAction)
                adapter.add(chaptersAction)
                adapter.add(speedAction)
            }

            override fun onActionClicked(action: Action) {
                when (action.id) {
                    ACTION_AUDIO_ID -> showAudioPicker()
                    ACTION_SUBTITLE_ID -> showSubtitlePicker()
                    ACTION_CHAPTERS_ID -> showChapterPicker()
                    ACTION_SPEED_ID -> showSpeedPicker()
                    else -> {
                        // Match by reference rather than id — the
                        // RewindAction / FastForwardAction subclasses
                        // override the action id internally.
                        when (action) {
                            rewindAction -> seekRelative(-SKIP_BACK_MS)
                            fastForwardAction -> seekRelative(SKIP_FORWARD_MS)
                            else -> super.onActionClicked(action)
                        }
                    }
                }
            }
        }.apply {
            this.host = host
            isSeekEnabled = true
        }

        exo.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                // Screen-on flag tracks active playback so the Fire TV
                // / Android TV screensaver doesn't kick in mid-show.
                // Toggling on isPlaying (rather than ACTION_DOWN /
                // user activity) means we release the flag the moment
                // the user pauses, so paused-and-walked-away doesn't
                // hold the screen forever.
                val window = activity?.window
                if (isPlaying) {
                    window?.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                    progressTracker?.start(arguments?.getString(ARG_ITEM_ID) ?: return, viewModel.hlsOffsetMs)
                } else {
                    window?.clearFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                    progressTracker?.onPause()
                }
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED) {
                    progressTracker?.onStop()
                    val next = nextEpisode
                    when {
                        next == null -> parentFragmentManager.popBackStack()
                        // Music: chain to next track silently. The Up
                        // Next overlay (with title + countdown) makes
                        // sense between episodes — between tracks
                        // it's just chrome the user doesn't want.
                        currentItemType == "track" -> goToNextEpisode(next)
                        else -> showUpNextOverlay(immediate = true)
                    }
                }
            }

            override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
                // Surface ExoPlayer's actual error to the user instead
                // of the silent failure that produced the "audio file
                // not playable" report. Code + message together pin
                // down whether it's a network/auth issue, a decoder
                // miss, or a malformed source. See
                // https://developer.android.com/reference/androidx/media3/common/PlaybackException
                // for the error code constants. For HTTP/HLS sources
                // include the failing URL so the user (or a tunnel
                // log) can identify which request died.
                val cause = error.cause
                val urlPart = if (cause is androidx.media3.datasource.HttpDataSource.HttpDataSourceException) {
                    "\n${cause.dataSpec.uri}"
                } else ""
                val msg = "Playback error ${error.errorCodeName}: ${error.message}$urlPart"
                showErrorDialog(msg)
            }
        })
    }

    private fun playSource(source: PlaybackSource) {
        val exo = player ?: return
        when (source) {
            is PlaybackSource.DirectPlay -> {
                exo.setMediaItem(MediaItem.fromUri(Uri.parse(source.url)))
                exo.prepare()
                if (source.startMs > 0) exo.seekTo(source.startMs)
                exo.playWhenReady = true
            }
            is PlaybackSource.Hls -> {
                val (factory, errorPolicy) = transcodeHttpFactory()
                val hlsSource = HlsMediaSource.Factory(factory)
                    .setLoadErrorHandlingPolicy(errorPolicy)
                    .createMediaSource(MediaItem.fromUri(Uri.parse(source.playlistUrl)))
                exo.setMediaSource(hlsSource)
                exo.prepare()
                // seg0AudioGapSec compensation. After a mid-stream
                // resume with AC3 → AAC re-encode, the first audible
                // AAC frame lands a few seconds into segment 0. Seek
                // there before play starts so the user sees the first
                // video frame and hears the first audio frame at the
                // same instant — without this, the screen shows
                // silent video while the audio pipeline warms up.
                // Zero on the legacy / direct-resume path; the seek
                // is a no-op there.
                if (source.initialSeekMs > 0) {
                    exo.seekTo(source.initialSeekMs)
                }
                exo.playWhenReady = true
            }
        }
    }

    /// Default DefaultHttpDataSource timeouts are 8 s connect / 8 s
    /// read. The first segment waits for the ffmpeg transcoder to
    /// spin up server-side, which on remote / Cloudflare-Tunnel
    /// deployments routinely takes 10–20 s. Bump both, allow cross-
    /// protocol redirects (Tunnel's HTTP→HTTPS handling), and send
    /// an identifiable UA so requests look like a real client to any
    /// WAF rules in front of the server. Used by the HLS branch above.
    private fun transcodeHttpFactory(): Pair<DefaultHttpDataSource.Factory, androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy> {
        val factory = DefaultHttpDataSource.Factory()
            .setConnectTimeoutMs(30_000)
            .setReadTimeoutMs(60_000)
            .setAllowCrossProtocolRedirects(true)
            .setUserAgent("OnScreen-Android/1.0 (ExoPlayer)")
            .setDefaultRequestProperties(mapOf())
        // Retry HTTP / IO failures up to 6 times with the default
        // exponential backoff (1 s, 2 s, 4 s, 8 s, 8 s, 8 s ≈ 30 s total).
        // Catches the case where the manifest/playlist isn't ready on
        // the first poll because the transcoder is still warming up.
        val errorPolicy = androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy(6)
        return factory to errorPolicy
    }

    private fun refreshSecondaryActions() {
        // Hide audio/subtitle/chapter buttons if no choices to make.
        val sa = subtitleAction ?: return
        val aa = audioAction ?: return
        val ca = chaptersAction ?: return
        val sp = speedAction ?: return
        val secondary = (glue?.controlsRow as? PlaybackControlsRow)?.secondaryActionsAdapter as? ArrayObjectAdapter
            ?: return
        secondary.clear()
        if (audioStreams.size > 1) secondary.add(aa)
        if (subtitleStreams.isNotEmpty()) secondary.add(sa)
        // Single-chapter is degenerate (the whole movie). Only surface
        // the picker when there are at least 2 chapters worth jumping
        // between.
        if (chapters.size >= 2) secondary.add(ca)
        // Audiobooks get a speed picker — the canonical
        // audiobook-listener feature. Movies/episodes don't (a
        // 2x movie is rarely what the user wants); music keeps
        // playback at 1x to preserve pitch.
        if (currentItemType == "audiobook") secondary.add(sp)
    }

    /** Seek by [deltaMs] from the current position, clamped to the
     *  player's known duration. Used by the primary Rewind /
     *  FastForward actions and by the corresponding remote media
     *  keys, which dispatch through onActionClicked → onActionClicked
     *  → here once the actions exist on the controls row. */
    private fun seekRelative(deltaMs: Long) {
        val exo = player ?: return
        val target = (exo.currentPosition + deltaMs).coerceAtLeast(0L)
        val dur = exo.duration
        val clamped = if (dur > 0 && dur != Long.MAX_VALUE && target > dur) dur else target
        exo.seekTo(clamped)
    }

    /** Toggle the player between playing and paused. Driven by the
     *  hardware-media-key handler installed in onViewCreated. */
    private fun togglePlayPause() {
        val exo = player ?: return
        if (exo.isPlaying) exo.pause() else exo.play()
    }

    /** Build the ExoPlayer with a buffer profile chosen by the
     *  device's available RAM. Low-RAM Fire TV / Android TV devices
     *  (1 GB and similar — `ActivityManager.isLowRamDevice()` true)
     *  get tighter LoadControl bounds: ~halved buffer durations and
     *  a lower target byte cap. Without this, the default 50 s
     *  buffer pulls 30-60 MB of decoded video per session, which on
     *  a 1 GB box leaves the rest of the app fighting the OOM
     *  killer for what's left. Matches Google's TV-ME quality
     *  guideline for memory limits on low-RAM devices. */
    private fun buildExoPlayer(): ExoPlayer {
        val ctx = requireContext()
        val am = ctx.getSystemService(android.content.Context.ACTIVITY_SERVICE)
            as? android.app.ActivityManager
        val builder = ExoPlayer.Builder(ctx)
        if (am?.isLowRamDevice == true) {
            val loadControl = androidx.media3.exoplayer.DefaultLoadControl.Builder()
                .setBufferDurationsMs(
                    /* minBufferMs */ 15_000,
                    /* maxBufferMs */ 30_000,
                    /* bufferForPlaybackMs */ 1_500,
                    /* bufferForPlaybackAfterRebufferMs */ 3_000,
                )
                .setTargetBufferBytes(16 * 1024 * 1024) // 16 MB cap (default ~64 MB)
                .setPrioritizeTimeOverSizeThresholds(true)
                .build()
            builder.setLoadControl(loadControl)
        }
        return builder.build()
    }

    private fun showSpeedPicker() {
        val labels = SPEED_OPTIONS.map { "%.2fx".format(it) }.toTypedArray()
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.speed)
            .setItems(labels) { d, idx ->
                val chosen = SPEED_OPTIONS[idx]
                playbackSpeed = chosen
                val params = androidx.media3.common.PlaybackParameters(chosen)
                player?.playbackParameters = params
                speedAction?.label1 = getString(R.string.speed_label, "%.2f".format(chosen))
                d.dismiss()
            }
            .show()
    }

    private fun showChapterPicker() {
        if (chapters.isEmpty()) return
        val labels = chapters.mapIndexed { i, c ->
            val title = c.title.ifBlank { getString(R.string.chapter_n, i + 1) }
            "${i + 1}. $title · ${fmtTimecode(c.start_ms)}"
        }.toTypedArray()

        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.chapters)
            .setItems(labels) { d, idx ->
                val target = chapters[idx].start_ms
                // Server stores chapter offsets in content-time; HLS
                // sessions need the player-time translation through
                // the captured offset.
                val playerMs = (target - viewModel.hlsOffsetMs).coerceAtLeast(0)
                player?.seekTo(playerMs)
                d.dismiss()
            }
            .show()
    }

    private fun fmtTimecode(ms: Long): String {
        val s = ms / 1000
        val h = s / 3600
        val m = (s % 3600) / 60
        val sec = s % 60
        return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
    }

    private fun showAudioPicker() {
        if (audioStreams.isEmpty()) return
        val labels = audioStreams.mapIndexed { i, a ->
            val name = a.title.ifBlank { a.language.ifBlank { "Track ${a.index}" } }
            val ch = if (a.channels > 0) " · ${a.channels}ch" else ""
            "${i + 1}. $name$ch"
        }.toTypedArray()

        // Mark the active track. activeAudioIndex defaults to -1
        // (server picked / first track); coerce that to the first
        // entry so the radio dialog always has a checked row.
        val checked = if (activeAudioIndex in audioStreams.indices) activeAudioIndex else 0

        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.audio)
            .setSingleChoiceItems(labels, checked) { d, idx ->
                d.dismiss()
                if (idx == activeAudioIndex) return@setSingleChoiceItems
                activeAudioIndex = idx
                applyAudioSelection(idx)
            }
            .show()
    }

    /**
     * Apply an audio-track selection. Direct-play files have every
     * track present in the container so ExoPlayer's track selector
     * can swap by language without a network round-trip; transcoded
     * HLS sessions only carry the one audio the server picked at
     * start time, so a swap requires re-issuing the session with
     * the new audio_stream_index. The view model figures out the
     * source mode and routes accordingly.
     */
    private fun applyAudioSelection(idx: Int) {
        val stream = audioStreams.getOrNull(idx) ?: return
        if (currentSource is PlaybackSource.Hls) {
            // Transcode path (HLS) — emits a single audio track per
            // session, so a swap requires re-issuing the session with
            // the new audio_stream_index. Server-side
            // audio_stream_index is the FFmpeg stream index, which
            // the API exposes via AudioStream.index.
            val pos = player?.currentPosition ?: 0L
            viewModel.switchAudioStream(stream.index, pos)
        } else {
            // Direct play — let ExoPlayer pick the matching track.
            selectAudioByLanguage(stream.language)
        }
    }

    private fun showSubtitlePicker() {
        val labels = mutableListOf(getString(R.string.off))
        labels.addAll(subtitleStreams.map { s ->
            val name = s.title.ifBlank { s.language.ifBlank { "Track ${s.index}" } }
            if (s.forced) "$name (forced)" else name
        })
        // "Find more online…" entry tacks an OpenSubtitles search on
        // the end of the picker. Index = labels.size — beyond every
        // track row — so the radio-row indices for real tracks don't
        // shift around.
        val findMoreIdx = labels.size
        labels.add(getString(R.string.subtitles_find_more))

        // Best-effort active-row detection: pull the current
        // preferred-text-language from ExoPlayer's track-selection
        // params. -1 (off) maps to row 0; an unmatched language
        // also maps to "Off" so the dialog always has a checked row.
        val preferred = player?.trackSelectionParameters?.preferredTextLanguages?.firstOrNull()
        val textDisabled = player?.trackSelectionParameters?.disabledTrackTypes?.contains(C.TRACK_TYPE_TEXT) == true
        val checked = if (textDisabled || preferred.isNullOrBlank()) {
            0
        } else {
            val match = subtitleStreams.indexOfFirst { it.language.equals(preferred, ignoreCase = true) }
            if (match < 0) 0 else match + 1
        }

        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.subtitles)
            .setSingleChoiceItems(labels.toTypedArray(), checked) { d, idx ->
                d.dismiss()
                when (idx) {
                    0 -> disableSubtitles()
                    findMoreIdx -> showOnlineSubtitleSearch()
                    else -> selectSubtitleByLanguage(subtitleStreams[idx - 1].language)
                }
            }
            .show()
    }

    /** Two-step OpenSubtitles flow: search → pick → download → reload
     *  the item so the new track shows up in the next subtitle picker
     *  open. Keeps the standard track flow above untouched and uses
     *  the same dialog style. */
    private fun showOnlineSubtitleSearch() {
        val itemId = arguments?.getString(ARG_ITEM_ID) ?: return
        val fileId = viewModel.uiState.value.item?.files?.firstOrNull()?.id ?: return
        val ctx = requireContext()
        val loading = AlertDialog.Builder(ctx, R.style.PlayerDialog)
            .setTitle(R.string.subtitles_searching)
            .setMessage(R.string.subtitles_searching_msg)
            .setCancelable(true)
            .show()
        viewLifecycleOwner.lifecycleScope.launch {
            val results = try {
                onlineSubtitleRepo.search(itemId)
            } catch (e: Exception) {
                loading.dismiss()
                Toast.makeText(ctx, e.message ?: getString(R.string.subtitles_search_failed), Toast.LENGTH_SHORT).show()
                return@launch
            }
            loading.dismiss()
            if (results.isEmpty()) {
                Toast.makeText(ctx, R.string.subtitles_no_results, Toast.LENGTH_SHORT).show()
                return@launch
            }
            val labels = results.map { r ->
                buildString {
                    append(r.language.uppercase())
                    if (r.from_trusted) append(" ★")
                    if (r.hearing_impaired) append(" SDH")
                    append(" · ")
                    append(r.file_name)
                }
            }.toTypedArray()
            AlertDialog.Builder(ctx, R.style.PlayerDialog)
                .setTitle(R.string.subtitles_pick_result)
                .setItems(labels) { _, which ->
                    val pick = results.getOrNull(which) ?: return@setItems
                    viewLifecycleOwner.lifecycleScope.launch {
                        try {
                            onlineSubtitleRepo.download(itemId, fileId, pick)
                            Toast.makeText(ctx, R.string.subtitles_downloaded, Toast.LENGTH_SHORT).show()
                            // Re-prepare so the new track surfaces in
                            // ExoPlayer's track list. Cheaper than
                            // tearing down the player session — the
                            // server side just returned a row that
                            // the next item-fetch will include.
                            viewModel.prepare(itemId, player?.currentPosition ?: 0L,
                                prefs.serverUrl.first() ?: "")
                        } catch (e: Exception) {
                            Toast.makeText(ctx, e.message ?: getString(R.string.subtitles_download_failed), Toast.LENGTH_LONG).show()
                        }
                    }
                }
                .show()
        }
    }

    private fun applyPreferredTracks(audioLang: String?, subtitleLang: String?) {
        val exo = player ?: return
        if (audioLang.isNullOrBlank() && subtitleLang.isNullOrBlank()) return
        val params = exo.trackSelectionParameters.buildUpon().apply {
            if (!audioLang.isNullOrBlank() && audioStreams.any { it.language.equals(audioLang, ignoreCase = true) }) {
                setPreferredAudioLanguage(audioLang)
            }
            if (!subtitleLang.isNullOrBlank() && subtitleStreams.any { it.language.equals(subtitleLang, ignoreCase = true) }) {
                setPreferredTextLanguage(subtitleLang)
                setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
            }
        }.build()
        exo.trackSelectionParameters = params
    }

    private fun selectAudioByLanguage(language: String) {
        val exo = player ?: return
        val params = exo.trackSelectionParameters.buildUpon()
            .setPreferredAudioLanguage(language.ifBlank { null })
            .build()
        exo.trackSelectionParameters = params
    }

    private fun selectSubtitleByLanguage(language: String) {
        val exo = player ?: return
        val params = exo.trackSelectionParameters.buildUpon()
            .setPreferredTextLanguage(language.ifBlank { null })
            .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
            .build()
        exo.trackSelectionParameters = params
    }

    private fun disableSubtitles() {
        val exo = player ?: return
        val params = exo.trackSelectionParameters.buildUpon()
            .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            .build()
        exo.trackSelectionParameters = params
    }

    private fun startUpNextWatcher() {
        upNextJob?.cancel()
        if (nextEpisode == null) return
        // Music tracks chain at EOS only — the lead-in overlay
        // would clip the last ~25 s of the song, which is exactly
        // where the outro / fade lives. Episodes still get the
        // overlay (the credits roll covers the same window, so
        // the early countdown isn't a content loss there).
        if (currentItemType == "track") return
        upNextJob = viewLifecycleOwner.lifecycleScope.launch {
            while (isActive) {
                delay(1000)
                val exo = player ?: continue
                val pos = exo.currentPosition
                val dur = exo.duration
                if (dur > 0 && dur != Long.MAX_VALUE) {
                    val remaining = dur - pos
                    if (remaining in 0..(UP_NEXT_LEAD_SEC * 1000L) && !upNextShown) {
                        showUpNextOverlay(immediate = false)
                    }
                }
            }
        }
    }

    private fun showUpNextOverlay(immediate: Boolean) {
        if (upNextShown && !immediate) return
        val next = nextEpisode ?: return
        val rootContainer = (view as? ViewGroup) ?: return

        if (upNextOverlay == null) {
            val overlay = LayoutInflater.from(requireContext())
                .inflate(R.layout.overlay_up_next, rootContainer, false)
            val lp = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
            ).apply {
                gravity = Gravity.TOP or Gravity.END
                topMargin = 60
                rightMargin = 60
            }
            overlay.layoutParams = lp
            rootContainer.addView(overlay)
            upNextOverlay = overlay
        }

        val overlay = upNextOverlay ?: return
        overlay.visibility = View.VISIBLE
        upNextShown = true

        val titleView = overlay.findViewById<TextView>(R.id.up_next_title)
        val labelView = overlay.findViewById<TextView>(R.id.up_next_label)
        val playBtn = overlay.findViewById<Button>(R.id.btn_play_now)
        val cancelBtn = overlay.findViewById<Button>(R.id.btn_cancel)

        titleView.text = next.title

        playBtn.setOnClickListener { goToNextEpisode(next) }
        cancelBtn.setOnClickListener { dismissUpNext(permanent = true) }

        val countdownJob = viewLifecycleOwner.lifecycleScope.launch {
            for (sec in UP_NEXT_COUNTDOWN_SEC downTo 1) {
                labelView.text = "UP NEXT · ${sec}s"
                delay(1000)
                if (!isActive) return@launch
            }
            goToNextEpisode(next)
        }
        // Cancel countdown if overlay is dismissed.
        cancelBtn.setOnClickListener {
            countdownJob.cancel()
            dismissUpNext(permanent = true)
        }

        if (immediate) playBtn.requestFocus() else playBtn.requestFocus()
    }

    private fun dismissUpNext(permanent: Boolean) {
        upNextOverlay?.visibility = View.GONE
        if (permanent) {
            upNextJob?.cancel()
            upNextJob = null
        }
    }

    private fun goToNextEpisode(ep: ChildItem) {
        progressTracker?.onStop()
        val newFrag = newInstance(ep.id, 0)
        parentFragmentManager.beginTransaction()
            .replace(R.id.main_container, newFrag)
            .commit()
    }

    private fun showErrorDialog(message: String) {
        val (title, body) = if (message == "content_restricted") {
            getString(R.string.content_restricted) to ""
        } else {
            "Playback error" to message
        }
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(title)
            .setMessage(body)
            .setPositiveButton(android.R.string.ok) { d, _ ->
                d.dismiss()
                parentFragmentManager.popBackStack()
            }
            .show()
    }

    override fun onPause() {
        super.onPause()
        player?.pause()
        progressTracker?.onPause()
    }

    override fun onStop() {
        super.onStop()
        progressTracker?.onStop()
    }

    override fun onDestroyView() {
        super.onDestroyView()
        // Drop the screen-on flag so navigating back to a non-player
        // screen lets the system idle-timer take over again.
        activity?.window?.clearFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        upNextJob?.cancel()
        upNextJob = null
        syncJob?.cancel()
        syncJob = null
        skipMarkerJob?.cancel()
        skipMarkerJob = null
        skipMarkerOverlay = null
        trickplayJob?.cancel()
        trickplayJob = null
        progressTracker?.stop()
        progressTracker = null

        // For music: hand the player to the MediaSessionService
        // instead of releasing it, so audio continues under the
        // system media controls when the user navigates away.
        // Video releases as before — PiP is the right "keep going"
        // surface for video.
        val handedOff = handOffAudioPlayerToService()
        if (!handedOff) {
            player?.release()
        }
        player = null
        viewModel.stopActiveTranscode()
    }

    /** When the user backs out of the player while music is still
     *  playing, transfer ownership of the ExoPlayer to the
     *  MediaSessionService and let the service keep it alive under
     *  the system foreground notification. Returns true when the
     *  handoff happened (caller should NOT release the player).
     *
     *  Skipped for video: the surface-view rendering doesn't
     *  translate to a service notification and the floating PiP
     *  window already covers the "keep watching" use case. */
    private fun handOffAudioPlayerToService(): Boolean {
        val exo = player ?: return false
        val isAudio = currentItemType == "track" || currentItemType == "audiobook"
        if (!isAudio) return false
        if (!exo.playWhenReady && exo.currentPosition == 0L) return false
        val itemId = arguments?.getString(ARG_ITEM_ID) ?: return false
        val ctx = activity?.applicationContext ?: return false
        val item = viewModel.uiState.value.item
        return try {
            // Capture the metadata the service needs for progress
            // reports + auto-advance — pulling it from the item
            // endpoint inside the service would race with the
            // activity going away.
            val meta = tv.onscreen.android.playback.AudioHandoff.Metadata(
                itemId = itemId,
                itemType = currentItemType.ifEmpty { item?.type.orEmpty() },
                parentId = item?.parent_id,
                index = item?.index,
                hlsOffsetMs = viewModel.hlsOffsetMs,
            )
            tv.onscreen.android.playback.AudioHandoff.park(exo, meta)
            // Started service so it survives the activity going away.
            // The service reads from AudioHandoff on its next attach()
            // and binds the parked player to a Media3 MediaSession,
            // which surfaces play/pause/skip on the system rail and
            // keeps the foreground notification alive.
            ctx.startService(
                android.content.Intent(
                    ctx,
                    tv.onscreen.android.playback.OnScreenMediaSessionService::class.java,
                ),
            )
            true
        } catch (_: Exception) {
            tv.onscreen.android.playback.AudioHandoff.clear()
            false
        }
    }
}
