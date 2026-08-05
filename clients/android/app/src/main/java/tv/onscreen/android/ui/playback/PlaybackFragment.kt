package tv.onscreen.android.ui.playback

import android.app.AlertDialog
import tv.onscreen.android.ui.common.focusableOnTv
import android.net.Uri
import android.os.Bundle
import android.view.Gravity
import android.view.KeyEvent
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
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
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
import tv.onscreen.android.ui.KeyEventHandler
import tv.onscreen.android.ui.detail.DetailFragment
import javax.inject.Inject
import kotlin.math.abs

@AndroidEntryPoint
@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
class PlaybackFragment : VideoSupportFragment(), KeyEventHandler {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var notificationsRepo: NotificationsRepository
    @Inject lateinit var trickplayRepo: TrickplayRepository
    @Inject lateinit var onlineSubtitleRepo: OnlineSubtitleRepository
    @Inject lateinit var watchNext: tv.onscreen.android.playback.WatchNextManager

    private lateinit var viewModel: PlaybackViewModel
    private var player: ExoPlayer? = null
    /** Reference to the anonymous `Player.Listener` we attach in
     *  initPlayer, so we can explicitly remove it before handing the
     *  player off to the MediaSessionService. The listener captures
     *  `this` (the fragment); if we leave it attached on the parked
     *  player, the service's player events fire fragment callbacks
     *  (`parentFragmentManager.popBackStack()`, `showErrorDialog`, …)
     *  against a destroyed fragment — IllegalStateException + leaked
     *  view hierarchy for as long as the player lives in the service. */
    private var playerListener: Player.Listener? = null
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
    // The user pressed Cancel (or BACK) on the Up Next card. Sticky for the rest
    // of the item: dismissing only cancelled the pre-roll WATCHER, while the
    // end-of-stream branch called showUpNextOverlay(immediate = true) directly
    // and started a fresh countdown — so an explicit "no, stop after this one"
    // was silently overruled and the next episode played anyway. On a shared TV
    // that means the show keeps rolling after everyone thought it had stopped.
    private var upNextDeclined = false
    // Centered spinner shown during STATE_BUFFERING (the 10-30s transcode warm-up
    // would otherwise be an unexplained black screen on cold start).
    private var bufferingView: android.widget.ProgressBar? = null
    // Renders subtitle cues. Leanback's VideoSupportFragment draws into a bare
    // SurfaceView and LeanbackPlayerAdapter only bridges transport state, so —
    // unlike media3's PlayerView, which Live TV uses and which contains one of
    // these for free — NOTHING here consumed the player's text output. Tracks
    // side-loaded correctly, the picker listed them, selection set the
    // preferred language, and every cue was decoded and thrown away. Subtitles
    // could be chosen but never appeared.
    private var subtitleView: androidx.media3.ui.SubtitleView? = null
    // The Up Next countdown. A field (not a local) so "Play Now" / dismiss /
    // teardown can cancel it — otherwise it keeps ticking and fires a SECOND
    // goToNextEpisode after the user already advanced. navigatedToNext guards
    // goToNextEpisode against a double fragment-replace from the same race.
    private var countdownJob: Job? = null
    private var navigatedToNext = false

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

    /** True once we've parked the audio player in the MediaSessionService
     *  from onStop (app backgrounded / HOME). onStart reclaims it;
     *  onDestroyView leaves the service owning it. Distinguishes the
     *  "handed off, may come back" state from a fresh foreground player. */
    private var parkedToService: Boolean = false

    /** Set when the player reaches STATE_ENDED so we don't publish a
     *  near-duration position back to the detail screen as a resume
     *  point (a finished item should offer Play, not Resume) and so the
     *  onStop handoff skips the just-ended player (the EOS chain path
     *  owns that transition). */
    private var playbackEnded: Boolean = false

    /** Skip-intro / skip-credits overlay button. Inflated lazily on
     *  first marker hit, then shown/hidden as the player crosses
     *  marker windows. */
    private var skipMarkerOverlay: Button? = null
    private var skipMarkerJob: Job? = null
    /** start_ms of the marker the overlay is currently showing, or null
     *  when hidden. Gates showSkipMarker so the per-appearance setup
     *  (label, click listener, focus request) runs exactly once when a
     *  marker first appears — the watcher calls showSkipMarker on every
     *  ~500 ms tick while inside the window, and re-requesting focus each
     *  tick fought the user for D-pad focus. */
    private var shownSkipMarkerStartMs: Long? = null
    private var markers: List<tv.onscreen.android.data.model.Marker> = emptyList()

    /**
     * Dialogs currently on screen. These are activity-window dialogs, so they
     * survive the fragment unless dismissed — and their click handlers reach
     * back into `parentFragmentManager` / `viewLifecycleOwner`, which throw
     * once the fragment is detached. That became reachable when the SCREEN_OFF
     * home-reset started replacing this fragment out from under a live dialog:
     * the TV wakes, the user presses OK on a stale error dialog, and the app
     * crashes with IllegalStateException. Dismissed in onDestroyView, which
     * also prevents the handler from ever running.
     */
    private val openDialogs = mutableListOf<AlertDialog>()

    /** Most recent item detail emitted by the ViewModel. Used by the
     *  Watch Next publisher to upsert title / poster / type metadata
     *  into the system Continue Watching row. */
    private var currentItem: tv.onscreen.android.data.model.ItemDetail? = null
    /** Periodic publisher that re-asserts the Watch Next row every
     *  ~30 s during playback. Cancelled on pause / end / view destroy. */
    private var watchNextJob: Job? = null
    /** Per-marker dismissal: once the user clicks Skip (or the credits
     *  overlay's auto-disappear fires past end_ms), don't re-show that
     *  same marker for the rest of this playback session. Keyed by
     *  start_ms because it's stable across the marker list. */
    private val dismissedMarkers = mutableSetOf<Long>()

    companion object {
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_START_MS = "start_ms"
        private const val UPDATE_PERIOD_MS = 1000

        /** Fraction of view height to keep clear beneath subtitle cues.
         *  Covers the TV overscan region plus the transport bar that slides up
         *  from the bottom, so cues are never cut off by the panel or hidden
         *  behind the controls. */
        private const val SUBTITLE_BOTTOM_PADDING_FRACTION = 0.08f
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
                // Track skip for music (the NEXT/PREV keys on most remotes). NEXT
                // advances to the resolved next sibling; PREVIOUS restarts the
                // current track (no previous-sibling resolver yet).
                android.view.KeyEvent.KEYCODE_MEDIA_NEXT     -> { nextEpisode?.let { goToNextEpisode(it) }; true }
                android.view.KeyEvent.KEYCODE_MEDIA_PREVIOUS -> { player?.seekTo(0); true }
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

                // Only (re)load the player + tracks + progress tracker when the
                // SOURCE actually changes — the initial load, or an audio switch
                // that issues a fresh transcode session. A metadata-only re-emit
                // (e.g. loadNextSibling's copy(nextEpisode=…)) keeps the same
                // source instance; re-running playSource there would RESTART
                // playback, stack a second progress tracker, and re-clobber the
                // user's track selection. playerWasReused skips the (re)load
                // entirely — the reclaimed-from-service player is already playing
                // this exact source — but still re-installs the fragment-side
                // progress tracker.
                val sourceChanged = source !== currentSource
                currentSource = source
                when {
                    playerWasReused -> {
                        playerWasReused = false
                        installProgressTracker(itemId)
                    }
                    // A source that arrives AFTER onStop released the player.
                    // prepare() is several server round-trips (item -> watch
                    // limit -> decision -> transcode start, tens of seconds on a
                    // cold ffmpeg spin-up) and this collector is not
                    // lifecycle-gated, so pressing HOME during that window lands
                    // the emission on a null player: playSource no-ops but the
                    // progress tracker still installs and then reports position
                    // 0 every 10s, overwriting real resume progress with the
                    // start of the episode.
                    sourceChanged && player == null -> Unit
                    sourceChanged -> {
                        playSource(source)
                        applyPreferredTracks(state.preferredAudioLang, state.preferredSubtitleLang, state.forcedSubtitlesOnly)
                        installProgressTracker(itemId)
                    }
                }

                glue?.title = state.item?.title ?: ""
                glue?.subtitle = state.item?.year?.toString() ?: ""

                markers = state.markers
                dismissedMarkers.clear()
                chapters = state.item?.files?.firstOrNull()?.chapters ?: emptyList()
                currentItemType = state.item?.type.orEmpty()
                currentItem = state.item

                refreshSecondaryActions()
                startUpNextWatcher()
                startCrossDeviceSync(itemId)
                startSkipMarkerWatcher()
                installTrickplaySeekProvider(itemId)
                bindAudioBackdrop(state.item)
                startWatchNextWatcher()
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
        // Recycle the outgoing provider's sprite sheets (~30 MB for a feature
        // film) before replacing it. This runs on every uiState emission, but
        // only the provider still attached at onDestroyView time was ever
        // released — so an audio switch or subtitle reload silently orphaned a
        // full sheet cache's worth of native bitmap memory on a 1 GB TV box.
        (glue?.seekProvider as? TrickplaySeekProvider)?.release()
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
    /**
     * Periodically upsert the system Watch Next row so the launcher's
     * Continue Watching strip always reflects an up-to-date position.
     * Republishes every 30 s while the player is alive — the
     * underlying [WatchNextManager] short-circuits cheaply when
     * nothing has actually changed, and the time keeps the
     * `last_engagement_time_utc_millis` field fresh so the launcher
     * sorts our tile near the top of the row. Cancelled on
     * STATE_ENDED and onDestroyView.
     */
    private fun startWatchNextWatcher() {
        watchNextJob?.cancel()
        watchNextJob = viewLifecycleOwner.lifecycleScope.launch {
            while (isActive) {
                val item = currentItem
                val exo = player
                if (item != null && exo != null) {
                    val pos = exo.currentPosition + viewModel.hlsOffsetMs
                    // Content-time duration — see contentDurationMs(). Using
                    // the player's session-relative duration here made pos/dur
                    // cross the manager's 0.9 "finished" threshold almost
                    // immediately on any resumed HLS session, so the launcher's
                    // Continue Watching row was deleted mid-movie.
                    val dur = contentDurationMs()
                    if (dur > 0L && pos > 0L) {
                        // Off the main thread: publishContinueWatching does a
                        // full ContentResolver query plus an insert/update —
                        // cross-process binder IPC and provider disk I/O — and
                        // this ticks every 30 s underneath live video.
                        withContext(Dispatchers.IO) {
                            watchNext.publishContinueWatching(item, pos, dur)
                        }
                    }
                }
                delay(30_000)
            }
        }
    }

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
        // Already showing this exact marker — the watcher just ticked again
        // inside the same window. Don't re-bind the label/listener or, most
        // importantly, re-request focus (that fought the user for D-pad
        // focus every 500 ms).
        if (shownSkipMarkerStartMs == marker.start_ms) return
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
            btn.setOnKeyListener { _, keyCode, event ->
                if (keyCode == KeyEvent.KEYCODE_BACK && event.action == KeyEvent.ACTION_UP) {
                    // BACK dismisses the skip prompt for this window rather than
                    // exiting playback (the button was focused, so BACK would
                    // otherwise fall through to Leanback and pop the player).
                    shownSkipMarkerStartMs?.let { dismissedMarkers.add(it) }
                    hideSkipMarker(userAction = true); true
                } else {
                    false
                }
            }
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
            hideSkipMarker(userAction = true)
        }
        overlay.visibility = View.VISIBLE
        overlay.requestFocus()
        shownSkipMarkerStartMs = marker.start_ms
    }

    /**
     * @param userAction true when the user dismissed the prompt themselves
     *   (pressed Skip, or BACK). False when the window merely elapsed or
     *   playback paused — in that case the user never asked for anything, so
     *   putting the transport bar on screen would be an unprompted interruption
     *   on every episode that has an intro marker.
     */
    private fun hideSkipMarker(userAction: Boolean = false) {
        shownSkipMarkerStartMs = null
        val hadFocus = skipMarkerOverlay?.hasFocus() == true
        skipMarkerOverlay?.visibility = View.GONE
        if (hadFocus) restoreFocusFromOverlay(showBar = userAction)
    }

    /**
     * Hand focus back to Leanback after one of our own overlays (Skip
     * intro/credits, Up Next) gives it up.
     *
     * `view?.requestFocus()` is not enough and was the bug: the fragment root is
     * a container Leanback does not treat as a focus target, so once the overlay
     * button went GONE focus was stranded on nothing. LEFT/RIGHT kept working
     * because those are intercepted at the ACTIVITY level (see
     * onActivityKeyEvent), which masked it — but UP, DOWN and CENTER all route
     * through view focus, so the transport bar, seek bar and the audio /
     * subtitle / chapter / speed buttons could not be opened again for the rest
     * of the item. Pressing Skip Intro effectively disabled the player's UI.
     *
     * Focus goes into leanback's controls dock, which descends into the rows'
     * VerticalGridView — the view leanback's own key interceptor is attached to.
     * (getVerticalGridView() is package-private in leanback 1.0.0, so the dock
     * id is the reachable handle.)
     *
     * @param showBar surface the transport bar as well. Only on a user action:
     *   see hideSkipMarker.
     *
     * tickle(), NOT showControlsOverlay(). showControlsOverlay shows the bar but
     * never arms the auto-hide timer — startFadeTimer is only reached from
     * setFadingEnabled, onResume and tickle() — so the bar it raised sat there
     * permanently over the picture. tickle() is stopFadeTimer + showControlsOverlay
     * + startFadeTimer, and is what leanback itself calls on user input.
     */
    private fun restoreFocusFromOverlay(showBar: Boolean) {
        if (!isAdded) return
        val dock = view?.findViewById<View>(androidx.leanback.R.id.playback_controls_dock)
        val took = dock?.requestFocus() == true
        // Fall back to tickle() when the dock refused focus, so the D-pad can
        // never end up stranded on a hidden overlay.
        if (showBar || !took) tickle()
    }

    /** Show/hide a centered indeterminate spinner while the player buffers — the
     *  transcode warm-up on a cold start would otherwise be a black screen with
     *  no sign anything is happening. */
    // NOTE — the transport bar shows SESSION time, not content time.
    //
    // A transcode/HLS session started at a resume point begins its own timeline
    // at zero, so resuming a 2-hour film at 0:45:00 shows "0:00 / 1:15:00": the
    // position looks like the start of the movie and the runtime has become the
    // leftover 75 minutes. viewModel.hlsOffsetMs is applied to progress
    // reporting, markers and trickplay, but NOT here.
    //
    // This is not fixable behind the Leanback API. LeanbackPlayerAdapter is
    // final, and on PlaybackTransportControlGlue getDuration(),
    // getBufferedPosition() and seekTo() are all final (verified against
    // leanback-1.0.0 with javap) — only getCurrentPosition() is overridable.
    // Offsetting the position alone would leave the bar disagreeing with its
    // own scrub: a seek to the right-hand end would render past the stated
    // duration. That is worse than the current consistent-but-wrong display.
    //
    // The real fix is a product decision, not a patch: either the server emits
    // a full-timeline playlist for a resumed session (correct bar, more
    // transcoding), or the client starts the session at 0 and seeks after
    // prepare (correct bar, transcode from the top). Left as-is deliberately
    // rather than shipping a half-translation.

    /** Lazily attach the cue renderer above the video surface. */
    private fun ensureSubtitleView(): androidx.media3.ui.SubtitleView? {
        subtitleView?.let { return it }
        val rootContainer = (view as? ViewGroup) ?: return null
        val sv = androidx.media3.ui.SubtitleView(requireContext()).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            )
            // Platform captioning preferences (size and style set by the user in
            // Android TV / Fire TV accessibility settings) — respecting these is
            // the whole point of the system captions UI.
            setUserDefaultStyle()
            setUserDefaultTextSize()
            // Keep cues clear of the TV's overscan region and of the transport
            // bar that slides up from the bottom edge.
            setBottomPaddingFraction(SUBTITLE_BOTTOM_PADDING_FRACTION)
        }
        // Insert BENEATH leanback's transport chrome, not on top of it.
        // A plain addView appends as the last child, which put the caption
        // layer above playback_controls_dock — so raising the transport bar
        // drew the bar under the cues instead of over them, and the
        // bottom-padding "clearance" below could never help.
        val dock = rootContainer.findViewById<View>(androidx.leanback.R.id.playback_controls_dock)
        val dockIndex = if (dock != null) rootContainer.indexOfChild(dock) else -1
        if (dockIndex >= 0) rootContainer.addView(sv, dockIndex) else rootContainer.addView(sv)
        subtitleView = sv
        return sv
    }

    private fun setBuffering(show: Boolean) {
        if (!show) {
            bufferingView?.visibility = View.GONE
            return
        }
        val rootContainer = (view as? ViewGroup) ?: return
        val spinner = bufferingView ?: android.widget.ProgressBar(requireContext()).also {
            it.layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
            ).apply { gravity = Gravity.CENTER }
            it.isIndeterminate = true
            rootContainer.addView(it)
            bufferingView = it
        }
        spinner.visibility = View.VISIBLE
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
        val parkedMeta = tv.onscreen.android.playback.AudioHandoff.peekMetadata()
        val parked = itemId?.let { tv.onscreen.android.playback.AudioHandoff.take(it) }
        if (parked == null && tv.onscreen.android.playback.AudioHandoff.peek() != null) {
            // Something is parked, but for a DIFFERENT item — the user
            // started new content while a track was playing in the service.
            // Building a fresh player without stopping the service left two
            // ExoPlayers decoding to the speakers at once: the new one never
            // requests audio focus (Media3 only wires that up on the service
            // side), so the old one is never told to duck or pause. Stopping
            // the service releases the parked player via its onDestroy, which
            // sees the handoff slot still pointing at it.
            try {
                requireContext().applicationContext.stopService(
                    android.content.Intent(
                        requireContext(),
                        tv.onscreen.android.playback.OnScreenMediaSessionService::class.java,
                    ),
                )
            } catch (_: Exception) { }
        }
        val exo = parked ?: buildExoPlayer()
        if (parked != null) {
            // Seed the item type from the handoff metadata NOW. It is
            // otherwise only set on the first uiState emission, several
            // network round-trips away, and onStop inside that window reads
            // isAudioItem() == false and stop()+release()s this very player —
            // killing music the user is actively listening to, then reporting
            // position 0 over its resume point.
            parkedMeta?.itemType?.let { currentItemType = it }
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
                // Each Action MUST carry an icon: PlaybackTransportControlGlue's
                // secondary control bar is icon-only (no text fallback), so a
                // label-only Action renders as a zero-width, invisible button —
                // which is why the audio/subtitle pickers were unreachable.
                val ctx = requireContext()
                fun icon(resId: Int) = androidx.core.content.ContextCompat.getDrawable(ctx, resId)
                audioAction = Action(ACTION_AUDIO_ID, getString(R.string.audio), null, icon(R.drawable.ic_audio_track))
                subtitleAction = Action(ACTION_SUBTITLE_ID, getString(R.string.subtitles), null, icon(R.drawable.ic_subtitles))
                chaptersAction = Action(ACTION_CHAPTERS_ID, getString(R.string.chapters), null, icon(R.drawable.ic_chapters))
                speedAction = Action(ACTION_SPEED_ID, getString(R.string.speed_label, "1.0"), null, icon(R.drawable.ic_speed))
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

        val listener = createPlayerListener()
        exo.addListener(listener)
        playerListener = listener
    }

    /** Build the player listener that drives the screen-on flag,
     *  end-of-stream handling (pop / Up Next / silent track chain), and
     *  error surfacing. Extracted from initPlayer so it can be
     *  re-installed verbatim when the fragment reclaims a player back
     *  from the MediaSessionService on return-to-foreground — the
     *  listener is removed at park time, so reclaim has to add a fresh
     *  one. */
    private fun createPlayerListener(): Player.Listener = object : Player.Listener {
        override fun onCues(cueGroup: androidx.media3.common.text.CueGroup) {
            ensureSubtitleView()?.setCues(cueGroup.cues)
        }

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
            setBuffering(state == Player.STATE_BUFFERING)
            if (state == Player.STATE_ENDED) {
                playbackEnded = true
                progressTracker?.onStop()
                // Pull the row out of the system Continue Watching
                // list — the user finished this title and shouldn't
                // keep seeing it offered as resumable. The next
                // episode (if any) will publish its own row when
                // playback starts on it.
                currentItem?.let { finished ->
                    // Off the main thread — remove() runs an unfiltered
                    // provider query plus a delete (cross-process binder IPC).
                    viewLifecycleOwner.lifecycleScope.launch {
                        withContext(Dispatchers.IO) { watchNext.remove(finished.id) }
                    }
                }
                watchNextJob?.cancel()
                val next = nextEpisode
                when {
                    next == null -> parentFragmentManager.popBackStack()
                    // Music: chain to next track silently. The Up
                    // Next overlay (with title + countdown) makes
                    // sense between episodes — between tracks
                    // it's just chrome the user doesn't want.
                    currentItemType == "track" -> goToNextEpisode(next)
                    // The user already declined. Leave playback rather than
                    // re-offering — this is the whole point of Cancel.
                    upNextDeclined -> parentFragmentManager.popBackStack()
                    else -> showUpNextOverlay(immediate = true)
                }
            }
        }

        override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
            // A direct-play source that ExoPlayer can't decode/demux (an
            // HEVC profile the device rejects, a malformed container, etc.)
            // is recoverable: re-issue it as a full server transcode and
            // rebind, the Android analogue of the web player's codec-
            // escalation. Only for DirectPlay — the ViewModel clears
            // directPlayContext on the first fallback, so an HLS/transcode
            // error that follows falls through to the dialog below instead
            // of looping.
            if (currentSource is PlaybackSource.DirectPlay) {
                val pos = player?.currentPosition ?: 0L
                android.util.Log.w(
                    "PlaybackFragment",
                    "direct play failed (${error.errorCodeName}); falling back to server transcode",
                    error,
                )
                viewModel.fallbackFromDirectPlay(pos)
                return
            }
            // Surface ExoPlayer's actual error to the user instead
            // of the silent failure that produced the "audio file
            // not playable" report. Code + message together pin
            // down whether it's a network/auth issue, a decoder
            // miss, or a malformed source. See
            // https://developer.android.com/reference/androidx/media3/common/PlaybackException
            // for the error code constants. For HTTP/HLS sources
            // include the failing URL so the user (or a tunnel
            // log) can identify which request died — with the
            // ?token= credential stripped before it hits the screen.
            val cause = error.cause
            val urlPart = if (cause is androidx.media3.datasource.HttpDataSource.HttpDataSourceException) {
                "\n${PlaybackHelper.sanitizeUriForDisplay(cause.dataSpec.uri)}"
            } else ""
            val msg = "Playback error ${error.errorCodeName}: ${error.message}$urlPart"
            showErrorDialog(msg)
        }
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
                // Side-load the subtitle tracks. A server HLS session carries
                // NO text streams (it maps only video + one audio; subtitles
                // are emitted as separate .vtt files), so without this the
                // track selector has nothing to select and the subtitle
                // picker is inert on every transcode / remux path.
                //
                // Merged explicitly rather than via
                // MediaItem.setSubtitleConfigurations: only
                // DefaultMediaSourceFactory honours that field, and we build
                // the HlsMediaSource directly — HlsMediaSource.createMediaSource
                // ignores subtitleConfigurations entirely, so setting it there
                // would silently do nothing.
                val subtitleSources = subtitleConfigurations().map { cfg ->
                    androidx.media3.exoplayer.source.SingleSampleMediaSource
                        .Factory(factory)
                        .setTreatLoadErrorsAsEndOfStream(true)
                        .createMediaSource(cfg, C.TIME_UNSET)
                }
                val mediaSource = if (subtitleSources.isEmpty()) {
                    hlsSource
                } else {
                    androidx.media3.exoplayer.source.MergingMediaSource(
                        hlsSource,
                        *subtitleSources.toTypedArray(),
                    )
                }
                exo.setMediaSource(mediaSource)
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

    /**
     * ExoPlayer side-load configs for the item's embedded subtitle streams,
     * served as WebVTT by the server's `/media/subtitles/{fileId}/{index}`
     * endpoint. Applied to HLS sources, where the session itself carries no
     * text tracks; direct play leaves them off because ExoPlayer extracts the
     * container's own subtitle streams and side-loading would duplicate every
     * track in the picker.
     */
    private fun subtitleConfigurations(): List<MediaItem.SubtitleConfiguration> =
        viewModel.uiState.value.subtitleSources.map { s ->
            MediaItem.SubtitleConfiguration.Builder(Uri.parse(s.url))
                .setMimeType(androidx.media3.common.MimeTypes.TEXT_VTT)
                .setLanguage(s.language.ifBlank { null })
                .setLabel(s.label)
                .setSelectionFlags(if (s.forced) C.SELECTION_FLAG_FORCED else 0)
                .build()
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
        // Gate each secondary button on whether the data behind it is
        // meaningful. Permissive on Audio + Subtitle to match Plex /
        // Jellyfin / Emby UX:
        //
        // - Audio: any track (>= 1). Showing "1 of 1" lets the viewer
        //   confirm what they're listening to without leaving playback.
        //   (Previous `> 1` gate hid the button on files with a single
        //   audio track, which is most modern movies — surprising for
        //   users who came from another player.)
        // - Subtitles: always shown. Even when the file has zero
        //   embedded streams, the picker still offers "Off" and
        //   "Find more online…" (OpenSubtitles search) — both of
        //   those are useful surfaces a user expects to reach from
        //   playback regardless of what the file ships with.
        // - Chapters: ≥ 2 (single chapter == the whole movie, useless).
        // - Speed: audiobooks only (a 2× movie is rarely what users
        //   want, and music must stay at 1× to preserve pitch).
        //
        // After mutating the adapter, ask the host to re-bind the
        // controls row — Leanback's ControlBarPresenter is usually
        // observe-the-adapter-and-update, but mid-lifecycle adapter
        // mutations (we clear+re-add on each state emit) have been
        // observed to not always refresh the rendered buttons. The
        // notifyPlaybackRowChanged() call costs ~nothing and turns
        // a flaky "buttons missing after a state re-emit" into a
        // deterministic redraw.
        val sa = subtitleAction ?: return
        val aa = audioAction ?: return
        val ca = chaptersAction ?: return
        val sp = speedAction ?: return
        val secondary = (glue?.controlsRow as? PlaybackControlsRow)?.secondaryActionsAdapter as? ArrayObjectAdapter
            ?: return
        secondary.clear()
        if (audioStreams.isNotEmpty()) secondary.add(aa)
        secondary.add(sa) // always — picker has "Off" + "Find more online…" entries
        if (chapters.size >= 2) secondary.add(ca)
        if (currentItemType == "audiobook") secondary.add(sp)

        // Force a row re-bind so a stale ControlBarPresenter view
        // doesn't keep showing the pre-refresh button set.
        glue?.host?.notifyPlaybackRowChanged()
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

    /**
     * Activity-level key dispatch for D-pad LEFT / RIGHT during
     * playback. Routed through KeyEventHandler (intercepted in
     * MainActivity.dispatchKeyEvent) so we run before Leanback's
     * focus-search consumes the keys for transport-button
     * navigation.
     *
     * Gated on [isControlsOverlayVisible]: when the overlay is up
     * the user is interacting with the transport row, so we let
     * Leanback handle LEFT/RIGHT to move focus between Rewind /
     * PlayPause / FastForward. When the overlay is hidden the
     * surface is the active surface and TV-PC requires LEFT/RIGHT
     * to seek directly — so we step the player ±SKIP_BACK_MS /
     * SKIP_FORWARD_MS without invoking the overlay.
     */
    override fun onActivityKeyEvent(event: KeyEvent): Boolean {
        if (event.action != KeyEvent.ACTION_DOWN) return false
        if (isControlsOverlayVisible) return false
        // The Skip (intro/credits) and Up Next overlays own focus while visible but
        // are NOT the controls overlay, so without this LEFT/RIGHT would seek the
        // video instead of moving between their buttons — RIGHT off "Play Now" would
        // scrub +30s and "Cancel" was unreachable. Let Leanback's focus search have
        // the keys whenever one of those overlays is up.
        if (skipMarkerOverlay?.visibility == View.VISIBLE) return false
        if (upNextOverlay?.visibility == View.VISIBLE) return false
        return when (event.keyCode) {
            KeyEvent.KEYCODE_DPAD_LEFT -> { seekRelative(-SKIP_BACK_MS); true }
            KeyEvent.KEYCODE_DPAD_RIGHT -> { seekRelative(SKIP_FORWARD_MS); true }
            else -> false
        }
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
        // Check the current speed so the user can see what's active (was a plain
        // setItems list with no indication of the current selection).
        val checked = SPEED_OPTIONS.indexOfFirst { it == playbackSpeed }.coerceAtLeast(0)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.speed)
            .setSingleChoiceItems(labels, checked) { d, idx ->
                val chosen = SPEED_OPTIONS[idx]
                playbackSpeed = chosen
                val params = androidx.media3.common.PlaybackParameters(chosen)
                player?.playbackParameters = params
                speedAction?.label1 = getString(R.string.speed_label, "%.2f".format(chosen))
                d.dismiss()
            }
            .show()
            .trackOpen()
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
            .trackOpen()
    }

    private fun fmtTimecode(ms: Long): String {
        val s = ms / 1000
        val h = s / 3600
        val m = (s % 3600) / 60
        val sec = s % 60
        return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
    }

    /** Register a shown dialog for teardown-time dismissal. See [openDialogs]. */
    private fun AlertDialog.trackOpen(): AlertDialog {
        openDialogs.add(this)
        setOnDismissListener { openDialogs.remove(this) }
        return this
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
            .trackOpen()
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
            // the new audio_stream_index.
            //
            // Send the RELATIVE audio-stream ordinal (the position within
            // audio_streams), NOT AudioStream.index. The API's `index` is the
            // ABSOLUTE ffprobe stream index, but the server feeds this value
            // straight to `-map 0:a:%d`, which counts audio streams only —
            // the two conventions are documented as distinct at
            // internal/transcode/ffmpeg.go:181. Because video occupies #0:0
            // they can never coincide for a video file, so passing the
            // absolute index selected the wrong track on multi-audio files
            // and mapped a nonexistent stream (killing the session) on
            // single-audio ones.
            val pos = player?.currentPosition ?: 0L
            viewModel.switchAudioStream(idx, pos)
        } else {
            // Direct play — let ExoPlayer pick the matching track.
            selectAudioByLanguage(stream.language)
        }
    }

    private fun showSubtitlePicker() {
        val labels = mutableListOf(getString(R.string.off))
        labels.addAll(subtitleStreams.map { s ->
            val name = s.title.ifBlank { s.language.ifBlank { "Track ${s.index}" } }
            buildString {
                append(name)
                if (s.forced) append(" (forced)")
                if (s.sdh) append(" (SDH)")
            }
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
            .trackOpen()
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
            .trackOpen()
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
                            // Surface the new track without restarting from
                            // the resume point. reloadSubtitles refreshes the
                            // picker list in place and, for HLS, re-issues the
                            // transcode session at the *current* position so
                            // the subtitle appears in the playlist; direct play
                            // keeps its running source untouched (a re-prepare
                            // would restart playback and still couldn't render
                            // the server-side sidecar sub anyway).
                            viewModel.reloadSubtitles(itemId, player?.currentPosition ?: 0L)
                        } catch (e: Exception) {
                            Toast.makeText(ctx, e.message ?: getString(R.string.subtitles_download_failed), Toast.LENGTH_LONG).show()
                        }
                    }
                }
                .show()
                .trackOpen()
        }
    }

    private fun applyPreferredTracks(audioLang: String?, subtitleLang: String?, forcedSubtitlesOnly: Boolean) {
        val exo = player ?: return
        if (audioLang.isNullOrBlank() && subtitleLang.isNullOrBlank()) return
        val params = exo.trackSelectionParameters.buildUpon().apply {
            if (!audioLang.isNullOrBlank() && audioStreams.any { it.language.equals(audioLang, ignoreCase = true) }) {
                setPreferredAudioLanguage(audioLang)
            }
            // Mirror the web client's pickPreferredSubtitle contract:
            //  - forced-only ON  → only enable subtitles if a FORCED track
            //    in the preferred language exists; otherwise leave text
            //    disabled (no full captions auto-shown).
            //  - forced-only OFF → existing behavior: hand the language to
            //    ExoPlayer's track selector, which does normalized BCP-47
            //    matching and picks the in-language track (preferring forced).
            // ExoPlayer has no clean "forced only" param, so for the
            // forced-only case we gate on whether a forced in-language
            // stream is actually present before enabling text at all.
            if (!subtitleLang.isNullOrBlank()) {
                // Normalized 639-2/B → 639-1 matching so a pref of "en"
                // gates correctly against an ffprobe "eng" stream (see
                // langMatchesSubtitle). ExoPlayer normalizes internally
                // for the actual selection; we only need the gate to
                // agree on which streams count as in-language.
                val inLang = subtitleStreams.filter { langMatchesSubtitle(it.language, subtitleLang) }
                val hasForcedInLang = inLang.any { it.forced }
                val enable = if (forcedSubtitlesOnly) hasForcedInLang else inLang.isNotEmpty()
                if (enable) {
                    setPreferredTextLanguage(subtitleLang)
                    setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
                }
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
        // Keep the picker's checkmark in sync. Unlike HLS (where
        // switchAudioStream re-issues the session and the source re-emit
        // carries the new active track), direct-play swaps happen entirely
        // client-side, so nothing else updates activeAudioIndex — without
        // this the radio dialog re-opens checked on the old track.
        val idx = audioStreams.indexOfFirst { it.language.equals(language, ignoreCase = true) }
        if (idx >= 0) activeAudioIndex = idx
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
        // Honoured even on the immediate (end-of-stream) path, which is exactly
        // the one that used to ignore it.
        if (upNextDeclined) return
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

        // BACK on the overlay = Cancel, NOT "exit playback". Without this, BACK
        // while a button is focused falls through to Leanback and pops the player.
        val backToCancel = View.OnKeyListener { _, keyCode, event ->
            if (keyCode == KeyEvent.KEYCODE_BACK && event.action == KeyEvent.ACTION_UP) {
                dismissUpNext(permanent = true); true
            } else {
                false
            }
        }
        playBtn.setOnKeyListener(backToCancel)
        cancelBtn.setOnKeyListener(backToCancel)

        // Never stack countdowns: a second showUpNextOverlay (e.g. EOS firing
        // while the lead-in countdown is already running) would otherwise leave
        // two timers racing to advance. dismissUpNext + goToNextEpisode also
        // cancel this job.
        countdownJob?.cancel()
        countdownJob = viewLifecycleOwner.lifecycleScope.launch {
            for (sec in UP_NEXT_COUNTDOWN_SEC downTo 1) {
                labelView.text = "UP NEXT · ${sec}s"
                delay(1000)
                if (!isActive) return@launch
            }
            goToNextEpisode(next)
        }

        playBtn.requestFocus()
    }

    private fun dismissUpNext(permanent: Boolean) {
        countdownJob?.cancel()
        val hadFocus = upNextOverlay?.hasFocus() == true
        upNextOverlay?.visibility = View.GONE
        if (permanent) {
            upNextDeclined = true
            upNextJob?.cancel()
            upNextJob = null
        }
        // Same stranding as the skip button — the card owns focus while visible.
        if (hadFocus) restoreFocusFromOverlay(showBar = true)
    }

    private fun goToNextEpisode(ep: ChildItem) {
        // Guard against a double advance — "Play Now" and the countdown elapsing
        // (or the lead-in + EOS overlays) can both call this; only the first wins.
        if (navigatedToNext) return

        // The countdown keeps ticking while the app is backgrounded: onStop
        // releases the player but does not cancel countdownJob, and
        // viewLifecycleOwner's scope only dies at view destroy. So this can be
        // reached ~10s after the user pressed HOME, when the FragmentManager
        // has already saved state — and commit() after that is an
        // IllegalStateException, i.e. a crash from putting the TV to sleep near
        // the end of an episode. Bail instead; nothing is lost, because
        // MainActivity's SCREEN_ON path resets to Home on the way back in.
        //
        // This is the single choke point for every advance (countdown, Play
        // Now, end-of-stream, MEDIA_NEXT), so guarding here covers them all.
        val fm = parentFragmentManager
        if (fm.isStateSaved || !isAdded) return

        navigatedToNext = true
        countdownJob?.cancel()
        upNextJob?.cancel()
        progressTracker?.onStop()

        // Pop this fragment's own back-stack entry before pushing the next
        // episode's, so the container keeps exactly one recorded owner.
        // Previously the advance replaced the fragment WITHOUT adding to the
        // back stack, while the entry that brought us here still recorded
        // "remove DetailFragment, add PlaybackFragment#1" — so BACK from
        // episode 2 re-added the detail screen over a still-playing episode 2
        // rather than leaving playback.
        fm.popBackStack()
        fm.beginTransaction()
            .replace(R.id.main_container, newInstance(ep.id, 0))
            .addToBackStack(null)
            .commit()
    }

    private fun showErrorDialog(message: String) {
        val (title, body) = when {
            message == "content_restricted" ->
                getString(R.string.content_restricted) to ""
            // Parental watch limit — "watch_limit:<reason>" sentinel set by the
            // ViewModel (pre-flight / transcode 403) and ProgressTracker (mid-
            // session heartbeat 403).
            message.startsWith("watch_limit:") ->
                getString(R.string.watch_limit_title) to when (message.removePrefix("watch_limit:")) {
                    "outside_allowed_hours" -> getString(R.string.watch_limit_outside_hours)
                    "daily_limit_reached" -> getString(R.string.watch_limit_daily)
                    else -> getString(R.string.watch_limit_generic)
                }
            // Dolby Vision sentinel from PlaybackViewModel — DV isn't played (it
            // can't be tonemapped correctly server-side; see docs/dolby-vision.md).
            message == "dolby_vision" ->
                "Dolby Vision" to "Dolby Vision is not supported on this device."
            else -> "Playback error" to message
        }
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(title)
            .setMessage(body)
            .setPositiveButton(android.R.string.ok) { d, _ ->
                d.dismiss()
                // Guard the detached case: onDestroyView dismisses tracked
                // dialogs, but a dismiss already in flight can still deliver
                // this callback, and popBackStack on a detached fragment
                // throws IllegalStateException.
                if (isAdded) parentFragmentManager.popBackStack()
            }
            .create()
            .focusableOnTv()
            .also { it.trackOpen() }
            .show()
    }

    /** True for audio content (music tracks + audiobooks) — the items
     *  that keep playing across backgrounding via the
     *  MediaSessionService handoff. Video is released on stop instead. */
    private fun isAudioItem(): Boolean =
        currentItemType == "track" || currentItemType == "audiobook"

    /** Content-time duration for progress reports + completion ratios. The
     *  arithmetic lives in [PlaybackHelper.contentDurationMs] so it is unit
     *  testable; this just feeds it the live player/item state. */
    private fun contentDurationMs(): Long = PlaybackHelper.contentDurationMs(
        itemDurationMs = currentItem?.duration_ms ?: currentItem?.files?.firstOrNull()?.duration_ms,
        playerDurationMs = player?.duration ?: 0L,
        hlsOffsetMs = viewModel.hlsOffsetMs,
    )

    override fun onStart() {
        super.onStart()
        // Returning to the foreground after the app was backgrounded
        // (HOME) while audio kept playing in the service. Take the
        // player back so the transport controls drive the same instance
        // again and the service drops its foreground notification.
        if (parkedToService) {
            reclaimAudioPlayerFromService()
        }
    }

    override fun onPause() {
        super.onPause()
        // Audio keeps playing when the activity is backgrounded (HOME)
        // or partially obscured — onStop hands the player to the
        // foreground MediaSessionService so music doesn't cut out. Only
        // video pauses here (and is fully torn down in onStop).
        if (!isAudioItem()) {
            player?.pause()
            progressTracker?.onPause()
        }
    }

    override fun onStop() {
        super.onStop()
        // Hand the latest position to the detail screen so its Resume
        // label refreshes the instant we pop back. Captured before any
        // release below; no-op for finished items.
        publishProgressResult()

        if (!isAudioItem()) {
            // Tear down video playback as soon as the activity stops so
            // backing out of an episode kills the decoder immediately.
            // On some Google TV builds onDestroyView fires late enough
            // that the user is already on the previous screen with the
            // decoder still running.
            progressTracker?.onStop()
            player?.run { stop(); release() }
            player = null
            // release() invalidates the listener list, but null the
            // field too so we don't keep the captured callback alive
            // until onDestroyView runs.
            playerListener = null
            viewModel.stopActiveTranscode()
            return
        }

        // Audio: hand the player to the foreground MediaSessionService
        // so it keeps playing while the app is backgrounded (HOME) or
        // the user navigates elsewhere in-app. onStart reclaims it on
        // return; onDestroyView leaves the service owning it when the
        // fragment is genuinely torn down. Skipped when the track just
        // ended — createPlayerListener's EOS chain owns that transition
        // and onDestroyView's existing path handles the parked player.
        if (!parkedToService && !playbackEnded && handOffAudioPlayerToService()) {
            parkedToService = true
            // The service drives progress reporting from here; stop the
            // fragment-side ticker without emitting a spurious "stopped"
            // (the track is still playing).
            progressTracker?.stop()
        }
    }

    override fun onDestroyView() {
        subtitleView = null
        super.onDestroyView()
        // Dismiss any dialog still on screen. These are activity-window
        // dialogs that would otherwise outlive the fragment and fire their
        // handlers against a detached one.
        openDialogs.toList().forEach { runCatching { it.dismiss() } }
        openDialogs.clear()
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
        watchNextJob?.cancel()
        watchNextJob = null
        progressTracker?.stop()
        progressTracker = null

        // Recycle the trickplay sprite-sheet bitmaps (~30 MB for a feature film)
        // now, rather than waiting for GC to reclaim them once the fragment graph
        // is collected. Runs before the parked-path early-return below so it fires
        // on both teardown paths.
        (glue?.seekProvider as? TrickplaySeekProvider)?.release()

        if (parkedToService) {
            // Already handed to the service in onStop (in-app nav after
            // backgrounding, or teardown while parked) — the service
            // owns the player now. Don't release it; just drop our refs.
            player = null
            playerListener = null
            viewModel.stopActiveTranscode()
            return
        }

        // Music: hand the player to the MediaSessionService instead
        // of releasing it, so audio continues under the system media
        // controls when the user navigates away. Video has already
        // been torn down in onStop; the release call below is a
        // safety net for state-restore edge cases where onStop ran
        // but the player was rebuilt before reaching here.
        val handedOff = handOffAudioPlayerToService()
        if (!handedOff) {
            player?.release()
        }
        player = null
        // The handoff path clears playerListener before parking; the
        // release path doesn't need to (release() invalidates the
        // listener list) but null it for GC hygiene either way.
        playerListener = null
        viewModel.stopActiveTranscode()
    }

    /** Set up the 10 s progress reporter for [itemId]. Shared by the
     *  initial source-load path and the reclaim-from-service path on
     *  return-to-foreground. Reads currentItem for the duration
     *  fallback, which is populated by the time the reporter fires. */
    private fun installProgressTracker(itemId: String) {
        // Stop any prior tracker before replacing the field — otherwise the old
        // one's 10 s heartbeat coroutine keeps running and we double-report.
        progressTracker?.stop()
        val tracker = ProgressTracker(viewLifecycleOwner.lifecycleScope, itemRepo)
        tracker.positionProvider = { player?.currentPosition ?: 0L }
        tracker.durationProvider = { contentDurationMs() }
        tracker.updateOffset(viewModel.hlsOffsetMs)
        // Daily cap reached / allowed-hours window closed mid-session — pause
        // and surface the same block dialog the start path uses.
        tracker.onBlocked = { reason ->
            player?.pause()
            showErrorDialog("watch_limit:$reason")
        }
        tracker.start(itemId, viewModel.hlsOffsetMs)
        progressTracker = tracker
    }

    /** Hand the current content position back to the detail screen via
     *  a fragment result so its Resume label updates immediately on
     *  return — the detail's own server refetch can race the final
     *  progress write and re-render the pre-playback offset. Skipped for
     *  finished items (a completed title should offer Play, not Resume).
     *  Reads the live player, so call before any release. */
    private fun publishProgressResult() {
        if (playbackEnded) return
        val id = arguments?.getString(ARG_ITEM_ID) ?: return
        val exo = player ?: return
        val pos = exo.currentPosition + viewModel.hlsOffsetMs
        if (pos <= 0L) return
        parentFragmentManager.setFragmentResult(
            DetailFragment.RESULT_PLAYBACK_PROGRESS,
            Bundle().apply {
                putString(DetailFragment.RESULT_KEY_ITEM_ID, id)
                putLong(DetailFragment.RESULT_KEY_POSITION_MS, pos)
            },
        )
    }

    /** Take the audio player back from the MediaSessionService when the
     *  app returns to the foreground (onStart after HOME). Stops the
     *  now-redundant service, re-installs the player listener (removed
     *  at park time) and the progress reporter. The Leanback glue still
     *  wraps the same ExoPlayer instance, so no re-bind is needed.
     *
     *  If the service auto-advanced to a different track while we were
     *  backgrounded, this fragment is bound to the old track — we swap
     *  in a fresh PlaybackFragment for the current track instead (see
     *  below) so the title + progress reporting follow what's playing. */
    private fun reclaimAudioPlayerFromService() {
        parkedToService = false
        val launchId = arguments?.getString(ARG_ITEM_ID) ?: return
        val parkedId = tv.onscreen.android.playback.AudioHandoff.peekMetadata()?.itemId

        // Background auto-advance: the parked track no longer matches the
        // one this fragment launched with. Rebinding the mismatched
        // player here would leave a stale title and mis-attribute
        // progress to the old id. Instead swap in a fresh PlaybackFragment
        // for the track that's actually playing — its initPlayer take()
        // picks up the same parked player (playerWasReused suppresses a
        // restart), so playback stays seamless and all metadata is
        // correct. Deferred via post() to avoid a re-entrant transaction
        // inside onStart.
        if (parkedId != null && parkedId != launchId) {
            playerListener = null
            player = null
            view?.post {
                if (isAdded && !parentFragmentManager.isStateSaved) {
                    parentFragmentManager.beginTransaction()
                        .replace(R.id.main_container, newInstance(parkedId))
                        .commit()
                }
            }
            return
        }

        val reclaimed = tv.onscreen.android.playback.AudioHandoff.take(launchId) ?: run {
            // Nothing parked for this track — the service released the
            // player (queue ended / notification dismissed). Nothing to
            // rebind.
            return
        }
        try {
            activity?.applicationContext?.stopService(
                android.content.Intent(
                    requireContext(),
                    tv.onscreen.android.playback.OnScreenMediaSessionService::class.java,
                ),
            )
        } catch (_: Exception) { }
        player = reclaimed
        val listener = createPlayerListener()
        reclaimed.addListener(listener)
        playerListener = listener
        installProgressTracker(launchId)
    }

    /** When the user backs out of the player while music is still
     *  playing, transfer ownership of the ExoPlayer to the
     *  MediaSessionService and let the service keep it alive under
     *  the system foreground notification. Returns true when the
     *  handoff happened (caller should NOT release the player).
     *
     *  Skipped for video: the surface-view rendering doesn't
     *  translate to a service notification, and the video player
     *  has already been released in onStop. */
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
                // The service reporter needs this to absolutise duration the
                // same way we do — a resumed HLS player only knows its
                // REMAINING time. See AudioHandoff.Metadata.itemDurationMs.
                itemDurationMs = item?.duration_ms,
            )
            // Detach the fragment-owned listener BEFORE parking. Once the
            // service owns the player, our `onPlaybackStateChanged` /
            // `onPlayerError` callbacks would fire against a destroyed
            // fragment (parentFragmentManager.popBackStack(), dialog
            // dismissals, …). The service installs its own auto-advance
            // listener on attach() — that's the right handler for the
            // service-owned phase of playback.
            playerListener?.let { exo.removeListener(it) }
            playerListener = null
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

// Minimal ISO 639-2/B (and a couple of 639-2/T) → 639-1 map, mirroring the
// web client's normalizeLang. ffprobe usually reports 3-letter codes ("eng",
// "spa") while the saved subtitle preference is a 2-letter 639-1 code ("en",
// "es"); the forced-only gate must treat those as equal. Anything not here
// falls back to a primary-subtag comparison, so an unknown code simply won't
// false-match.
private val ISO6392_TO_1: Map<String, String> = mapOf(
    "eng" to "en", "spa" to "es", "fre" to "fr", "fra" to "fr", "ger" to "de",
    "deu" to "de", "ita" to "it", "por" to "pt", "rus" to "ru", "jpn" to "ja",
    "chi" to "zh", "zho" to "zh", "kor" to "ko", "ara" to "ar", "dut" to "nl",
    "nld" to "nl", "swe" to "sv", "nor" to "no", "dan" to "da", "fin" to "fi",
    "pol" to "pl", "tur" to "tr", "heb" to "he", "hin" to "hi", "tha" to "th",
    "vie" to "vi", "ces" to "cs", "cze" to "cs", "gre" to "el", "ell" to "el",
    "hun" to "hu", "ron" to "ro", "rum" to "ro", "ukr" to "uk", "ind" to "id",
)

/** Reduce a language tag to a canonical 639-1 primary subtag (lowercased).
 *  "ENG" → "en", "en-US" → "en", "xyz" → "xyz". */
private fun normalizeSubtitleLang(code: String?): String {
    if (code.isNullOrEmpty()) return ""
    val primary = code.lowercase().split('-', '_').first()
    return ISO6392_TO_1[primary] ?: primary
}

/** True when two language tags resolve to the same 639-1 primary subtag. */
private fun langMatchesSubtitle(a: String?, b: String?): Boolean {
    val na = normalizeSubtitleLang(a)
    return na.isNotEmpty() && na == normalizeSubtitleLang(b)
}
