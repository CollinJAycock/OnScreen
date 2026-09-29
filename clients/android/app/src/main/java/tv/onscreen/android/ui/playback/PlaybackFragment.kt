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
import androidx.activity.OnBackPressedCallback
import androidx.core.view.isVisible
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
import tv.onscreen.android.data.api.PlaybackStop
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
import tv.onscreen.android.playback.AudioItemTypes
import tv.onscreen.android.playback.AudiobookSpeed
import tv.onscreen.android.playback.BookSpeed
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
    @Inject lateinit var clientName: tv.onscreen.android.data.device.ClientName

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

    /** Last-seen side-load list, for detecting a subtitle download that
     *  changed the sources without changing the playback source. */
    private var lastSubtitleSources: List<SubtitleTrackSource>? = null
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
    /** The speed the player is at, as the picker shows it. Audiobooks only:
     *  anything else is held at 1× (see applyListeningSpeed). */
    private var playbackSpeed: Float = AudiobookSpeed.NORMAL

    /** Cross-device sync subscriber. Cancelled in onDestroyView. */
    private var syncJob: Job? = null

    /** Admin "stop this stream" (playback.stop SSE) subscriber. Cancelled in
     *  onDestroyView. */
    private var adminStopJob: Job? = null

    /** Trickplay-thumbnail load. Single job because installation is
     *  one-shot per session. */
    private var trickplayJob: Job? = null
    /** True for the first source emission after a parked-player
     *  re-take, so we don't restart playback when the user comes
     *  back to a track that's already playing in the service. */
    private var playerWasReused: Boolean = false
    /** The server session a player taken back from the background service
     *  is still reading, for prepare() to adopt (see initPlayer). */
    private var resumedSession: tv.onscreen.android.playback.StreamSession? = null

    /** True once we've parked the audio player in the MediaSessionService
     *  from onStop (app backgrounded / HOME). onStart reclaims it;
     *  onDestroyView leaves the service owning it. Distinguishes the
     *  "handed off, may come back" state from a fresh foreground player. */
    private var parkedToService: Boolean = false

    /** The Settings switch for display frame-rate matching, read at start. */
    private var matchFrameRateEnabled: Boolean = true

    /** The display has been matched to this video's frame rate (or there was
     *  nothing to match): once per player screen, since a re-issued session
     *  or a fallback transcode is the same video. */
    private var frameRateMatched: Boolean = false

    /** Playback is held while the display switches modes (see matchFrameRate). */
    private var heldForFrameRate: Boolean = false

    /** Set when the player reaches STATE_ENDED so we don't publish a
     *  near-duration position back to the detail screen as a resume
     *  point (a finished item should offer Play, not Resume) and so the
     *  onStop handoff skips the just-ended player (the EOS chain path
     *  owns that transition). */
    private var playbackEnded: Boolean = false

    /** Set when the server refused a mid-session 'playing' heartbeat with a
     *  403 (see stopForRefusedPlayback). Keeps the stopped player out of the
     *  background-audio handoff. */
    private var playbackRefused: Boolean = false

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

    /**
     * BACK while the Skip (intro/credits) or Up Next card is showing dismisses
     * the card instead of leaving playback.
     *
     * This used to be a View.OnKeyListener on the cards' buttons watching for
     * KEYCODE_BACK. That only works under LEGACY back dispatch: from targetSdk
     * 36 on Android 16 the platform stops delivering KEYCODE_BACK to views and
     * hands the press to OnBackInvokedDispatcher instead, so the listener would
     * never fire and BACK would pop the player out from under the card. An
     * OnBackPressedCallback is reached under both dispatches (legacy BACK lands
     * in ComponentActivity.onBackPressed, which forwards to the same
     * dispatcher), so it no longer matters whether the manifest's
     * enableOnBackInvokedCallback opt-out is honoured.
     *
     * Enabled only while a card is visible ([syncOverlayBackCallback]), so the
     * rest of the time BACK falls through to the FragmentManager's pop as
     * before. Created per view in onViewCreated and registered against
     * viewLifecycleOwner, so it is unregistered with the view and a recreated
     * view never inherits a stale enabled state.
     */
    private var overlayBackCallback: OnBackPressedCallback? = null

    companion object {
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_START_MS = "start_ms"
        private const val ARG_SPEED_BOOK_ID = "speed_book_id"
        private const val ARG_SPEED_RATE = "speed_rate"
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
        // Skip-back / skip-forward step in milliseconds. 10 s back is
        // the conventional "I missed that line" jump; 30 s forward
        // matches the audiobook / podcast convention and most TV
        // remotes' dedicated FastForward button feel.
        private const val SKIP_BACK_MS = 10_000L
        private const val SKIP_FORWARD_MS = 30_000L
        private const val UP_NEXT_COUNTDOWN_SEC = 10
        private const val UP_NEXT_LEAD_SEC = 25

        /** [bookSpeed]: set when chaining from one chapter of a book to the
         *  next — the speed the book was playing at (see goToNextEpisode). */
        fun newInstance(itemId: String, startMs: Long = 0, bookSpeed: BookSpeed? = null): PlaybackFragment {
            return PlaybackFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_ITEM_ID, itemId)
                    putLong(ARG_START_MS, startMs)
                    if (bookSpeed != null) {
                        putString(ARG_SPEED_BOOK_ID, bookSpeed.bookId)
                        putFloat(ARG_SPEED_RATE, bookSpeed.rate)
                    }
                }
            }
        }
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[PlaybackViewModel::class.java]

        overlayBackCallback = object : OnBackPressedCallback(false) {
            override fun handleOnBackPressed() {
                val skip = skipMarkerOverlay?.takeIf { it.isVisible }
                val upNext = upNextOverlay?.takeIf { it.isVisible }
                when {
                    // Both can be up near the end of an episode (credits marker
                    // + Up Next). The focused card is the one the user is on —
                    // the per-button listeners this replaces had that scoping.
                    upNext != null && (skip == null || upNext.hasFocus()) ->
                        dismissUpNext(permanent = true)
                    skip != null -> {
                        shownSkipMarkerStartMs?.let { dismissedMarkers.add(it) }
                        hideSkipMarker(userAction = true)
                    }
                }
                syncOverlayBackCallback()
            }
        }.also {
            requireActivity().onBackPressedDispatcher.addCallback(viewLifecycleOwner, it)
        }

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
        // NOTE: hardware media keys are handled in onActivityKeyEvent, NOT a
        // view-level OnKeyListener. The old listener sat on the fragment root,
        // which never holds focus under Leanback (the transport row and video
        // surface do), so MEDIA_NEXT / PREVIOUS / STOP were dead on every
        // remote that has them. Activity dispatch runs regardless of focus.

        val itemId = arguments?.getString(ARG_ITEM_ID) ?: return
        val startMs = arguments?.getLong(ARG_START_MS, 0) ?: 0
        val bookSpeed = arguments?.let { args ->
            args.getString(ARG_SPEED_BOOK_ID)?.let { BookSpeed(it, args.getFloat(ARG_SPEED_RATE, AudiobookSpeed.NORMAL)) }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""
            matchFrameRateEnabled = prefs.matchFrameRate.first()

            initPlayer()
            viewModel.prepare(itemId, startMs, serverUrl, bookSpeed, resumedSession)
            startAdminStopWatch(itemId)

            viewModel.uiState.collectLatest { state ->
                // Stopped by the server (admin stop / refused heartbeat): a
                // later emission — a seek re-issue or audio switch that was in
                // flight — must not start playback behind the stop dialog or
                // re-install the heartbeat.
                if (playbackRefused) return@collectLatest
                if (state.error != null) {
                    // An admin stop refused a (re)started session: tear down
                    // like a refused heartbeat, not a plain error dialog.
                    if (PlaybackStop.isSentinel(state.error)) {
                        stopForRefusedPlayback(state.error)
                    } else {
                        showErrorDialog(state.error)
                    }
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
                // An audio item plays with music attributes from its first
                // frame, the ones the background service uses: set only there,
                // they changed on the live player at BACK / HOME, rebuilding
                // the audio output (a stall of up to a second or so). Same
                // attributes again are a no-op.
                if (AudioItemTypes.isAudio(state.item?.type)) {
                    player?.setAudioAttributes(AudioItemTypes.MUSIC_ATTRIBUTES, /* handleAudioFocus= */ true)
                }
                val sourceChanged = source !== currentSource
                currentSource = source
                // A freshly-downloaded external subtitle updates
                // subtitleSources WITHOUT changing the source. The running
                // media source was built from the old list, so the new track
                // has no TrackGroup to select — re-attach at the current
                // position to make it materialize.
                val subsChanged = state.subtitleSources !== lastSubtitleSources
                lastSubtitleSources = state.subtitleSources
                when {
                    !sourceChanged && subsChanged && !playerWasReused &&
                        source != null && player != null -> {
                        val pos = player?.currentPosition ?: 0L
                        playSource(source)
                        if (pos > 0) player?.seekTo(pos)
                    }
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
                        // The server's probed frame rate, known before the
                        // player loads a byte: the display can switch before
                        // the first frame. Players learn it from the stream
                        // later (onTracksChanged), if at all for HLS.
                        if (AudioItemTypes.isAudio(state.item?.type)) {
                            // A movie's display mode is no use to music.
                            frameRates()?.releaseNow()
                        } else {
                            matchFrameRate(state.item?.files?.firstOrNull()?.frame_rate?.toFloat())
                        }
                        // An explicit in-session choice (picked from the
                        // subtitle dialog) outranks the saved preference: a
                        // source re-emit here is usually an audio-track
                        // switch re-issuing the session, and re-applying the
                        // SAVED pref silently undid what the user just chose.
                        if (userSubtitleChoice != null) {
                            applyPreferredTracks(state.preferredAudioLang, null, false)
                            applySubtitleChoice()
                        } else {
                            applyPreferredTracks(state.preferredAudioLang, state.preferredSubtitleLang, state.forcedSubtitlesOnly)
                        }
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

                applyListeningSpeed(state.listeningRate)
                refreshSecondaryActions()
                // Parked in the background service, the player is the
                // service's: onStart restarts these when it takes it back.
                if (!parkedToService) startPlayerWatchers(itemId)
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
    private fun bindAudioBackdrop(item: tv.onscreen.android.data.model.ItemDetail?) {
        val root = view as? android.view.ViewGroup ?: return
        val existing = root.findViewWithTag<android.view.View>("audio_backdrop")
        if (!isAudioItem() || item == null) {
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
                // The transport bar is CONTENT time now (see
                // ContentTimeForwardingPlayer), so the provider's cue
                // positions stay content-absolute — no session offset.
                hlsOffsetMs = 0L,
            )
            glue?.seekProvider = provider
        }
    }

    /** (Re)start the loops that follow and drive [player]: Up Next, the
     *  cross-device position sync, the skip-marker prompt and Watch Next. */
    private fun startPlayerWatchers(itemId: String) {
        startUpNextWatcher()
        startCrossDeviceSync(itemId)
        startSkipMarkerWatcher()
        startWatchNextWatcher()
    }

    /**
     * Stop the loops that drive [player], and the admin-stop watch, for as
     * long as the background service owns the player. The service watches
     * for admin stops itself from then on.
     *
     * They run on the view's scope, which outlives onStop, so they kept
     * running after HOME. The cross-device sync seeked the service's player
     * whenever the service's own progress report came back from the server
     * as a sync event: a paused track re-buffered, and at the end of a
     * track the seek hit a stream the server had already closed, failing
     * the player instead of moving on to the next track.
     *
     * Watch Next keeps running: it only reads a position, from the parked
     * player while that still plays this item ([positionSource]), so the
     * launcher's Continue Watching tile keeps up with background listening.
     */
    private fun stopPlayerWatchers() {
        upNextJob?.cancel()
        upNextJob = null
        syncJob?.cancel()
        syncJob = null
        adminStopJob?.cancel()
        adminStopJob = null
        skipMarkerJob?.cancel()
        skipMarkerJob = null
    }

    /** The player parked in the background service, while it still plays
     *  this screen's item (the service may have moved on to the next track,
     *  or let the player go). Main thread, like the player itself. */
    private fun parkedPlayerOfThisItem(): ExoPlayer? {
        if (!parkedToService) return null
        val meta = tv.onscreen.android.playback.AudioHandoff.peekMetadata() ?: return null
        if (meta.itemId != arguments?.getString(ARG_ITEM_ID)) return null
        return tv.onscreen.android.playback.AudioHandoff.peek()
    }

    /** The player to read this item's position from, with the offset of
     *  the session it reads: this screen's own, or the parked one
     *  ([parkedPlayerOfThisItem]). Read only: the service drives a parked
     *  player. */
    private fun positionSource(): Pair<ExoPlayer, Long>? {
        player?.let { return it to viewModel.hlsOffsetMs }
        val parked = parkedPlayerOfThisItem() ?: return null
        val offsetMs = tv.onscreen.android.playback.AudioHandoff.peekMetadata()?.hlsOffsetMs ?: return null
        return parked to offsetMs
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
                val source = positionSource()
                if (item != null && source != null) {
                    val (exo, offsetMs) = source
                    val pos = exo.currentPosition + offsetMs
                    // Content-time duration — see contentDurationMs(). Using
                    // the player's session-relative duration here made pos/dur
                    // cross the manager's 0.9 "finished" threshold almost
                    // immediately on any resumed HLS session, so the launcher's
                    // Continue Watching row was deleted mid-movie.
                    val dur = contentDurationMs(exo, offsetMs)
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
            // BACK dismisses the skip prompt for this window rather than
            // exiting playback — handled by overlayBackCallback, not a key
            // listener here (see its doc for why).
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
        syncOverlayBackCallback()
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
        syncOverlayBackCallback()
        if (hadFocus) restoreFocusFromOverlay(showBar = userAction)
    }

    /** Arm [overlayBackCallback] exactly while a Skip / Up Next card is on
     *  screen. Called from every place either card changes visibility. */
    private fun syncOverlayBackCallback() {
        overlayBackCallback?.isEnabled =
            skipMarkerOverlay?.isVisible == true || upNextOverlay?.isVisible == true
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
    // The transport bar shows CONTENT time — the glue is fed a
    // ContentTimeForwardingPlayer that translates position/duration/seeks,
    // while the fragment, tracker, markers and handoff keep the raw player.
    // Seeks outside the session window re-issue the session at the target
    // (PlaybackViewModel.reissueAt).

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
                        // Already there: a report of this device's own
                        // coming back (the background service's too, which
                        // the tracker above knows nothing of), or another
                        // device at the same spot. A seek would only
                        // re-buffer.
                        if (abs(playerPos - exo.currentPosition) < 2000L) return@collect
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

    /**
     * Obey the admin "stop this stream" SSE event. The event reaches every
     * one of the user's players, so act only when it targets this one (same
     * item, and this session / client name when the event names one — see
     * PlaybackStop.targets). A transcode stop has no server-side refusal
     * window, so this is its only clean signal; for direct play / remux the
     * 403 PLAYBACK_STOPPED on the next heartbeat is the backstop.
     */
    private fun startAdminStopWatch(itemId: String) {
        adminStopJob?.cancel()
        adminStopJob = viewLifecycleOwner.lifecycleScope.launch {
            while (isActive) {
                try {
                    notificationsRepo.subscribePlaybackStops().collect { evt ->
                        if (playbackRefused || player == null) return@collect
                        val mine = PlaybackStop.targets(
                            evt,
                            playingItemId = itemId,
                            sessionId = viewModel.activeSessionId,
                            clientName = clientName.value,
                        )
                        if (!mine) return@collect
                        stopForRefusedPlayback(
                            PlaybackStop.sentinel(PlaybackStop.text(evt.message)),
                        )
                    }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (_: Exception) {
                    // Stream dropped; reconnect after a short delay.
                }
                delay(5_000)
            }
        }
    }

    private fun initPlayer() {
        // A fragment restored from the back stack (BACK from the item it had
        // moved on to) plays its item again: it is neither moving on nor
        // finished any more. Left set, these kept its audio from being handed
        // to the background service on HOME / BACK.
        navigatedToNext = false
        playbackEnded = false
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
            // Building a fresh player without stopping the old one left two
            // ExoPlayers decoding to the speakers at once. Release the parked
            // player here (final position reported, server session ended) and
            // stop the service: the service may not have started yet (a park
            // starts it a moment later), so stopping it alone isn't enough.
            tv.onscreen.android.playback.AudioHandoff.stopAll(requireContext().applicationContext)
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
            // A transcoded / remuxed track: the player is still reading the
            // server session it was parked with, which prepare() adopts
            // rather than starting a second one.
            resumedSession = parkedMeta?.session
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

        // The glue sees CONTENT time; everything else keeps the raw player.
        // See ContentTimeForwardingPlayer for why this is the one legal way
        // through Leanback's final adapter/glue methods.
        val glueFacingPlayer = ContentTimeForwardingPlayer(
            exo,
            offsetMs = { viewModel.hlsOffsetMs },
            contentDurationMs = { contentDurationMs() },
            onSeekOutsideWindow = { target -> viewModel.reissueAt(target) },
        )
        val adapter = LeanbackPlayerAdapter(requireContext(), glueFacingPlayer, UPDATE_PERIOD_MS)
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
                speedAction = Action(
                    ACTION_SPEED_ID,
                    getString(R.string.speed_label, AudiobookSpeed.label(playbackSpeed)),
                    null,
                    icon(R.drawable.ic_speed),
                )
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

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
            // Started by anything during a display switch (the on-screen Play
            // included): the hold is over, and its end mustn't restart a
            // pause that follows.
            if (playWhenReady) heldForFrameRate = false
        }

        override fun onTracksChanged(tracks: androidx.media3.common.Tracks) {
            // Re-resolve the user's explicit subtitle choice against the NEW
            // track groups: selection overrides bind to concrete TrackGroup
            // instances, so after an audio-switch re-issues the session (or
            // any re-prepare) the old override silently stops matching.
            applySubtitleChoice()
            // No frame rate from the server (an older one): the stream's.
            if (!frameRateMatched && !isAudioItem()) {
                val fps = selectedVideoFrameRate(tracks)
                if (fps != null) {
                    matchFrameRate(fps)
                } else if (tracks.groups.any { it.type == C.TRACK_TYPE_VIDEO && it.isSelected }) {
                    // Nobody knows this video's rate (a server stream carries
                    // none): the last video's mode is no better than the
                    // default, so don't let it switch back mid-playback.
                    android.util.Log.i("PlaybackFragment", "frame rate unknown (not from the server, not in the stream): display left as it is")
                    frameRateMatched = true
                    frameRates()?.releaseNow()
                }
            }
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
                    // Includes a book's last chapter: the book ends there.
                    next == null -> parentFragmentManager.popBackStack()
                    // Audio: chain to the next track, or the book's next
                    // chapter, silently. The Up Next overlay (with title +
                    // countdown) makes sense between episodes — between
                    // tracks or chapters it's just chrome the user doesn't
                    // want.
                    isAudioItem() -> goToNextEpisode(next)
                    // The user already declined. Leave playback rather than
                    // re-offering — this is the whole point of Cancel.
                    upNextDeclined -> parentFragmentManager.popBackStack()
                    else -> showUpNextOverlay(immediate = true)
                }
            }
        }

        override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
            // Already stopped by the server (admin stop / refused heartbeat):
            // the stream dying afterwards is expected, and the stop dialog is
            // the real explanation — don't stack a raw error on top of it or
            // start a fallback transcode.
            if (playbackRefused) return
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
            // A stopped remux/transcode dies as a playlist/segment 403/404 —
            // usually before the playback.stop event, or instead of it when
            // the SSE stream was down. The player pausing has already paused
            // the heartbeat, so its 403 PLAYBACK_STOPPED backstop would never
            // fire: send one 'playing' beat now and, if the server refuses
            // it, show the stop (or other refusal) message, not a raw error.
            val tracker = progressTracker
            if (currentSource is PlaybackSource.Hls && tracker != null &&
                PlaybackHelper.isStoppedStreamStatus(error)
            ) {
                viewLifecycleOwner.lifecycleScope.launch {
                    val refusal = tracker.probeRefusal()
                    when {
                        refusal != null -> stopForRefusedPlayback(refusal)
                        // The SSE stop may have landed during the probe.
                        !playbackRefused -> showErrorDialog(msg)
                    }
                }
                return
            }
            showErrorDialog(msg)
        }
    }

    /** The selected video track's frame rate, or null when unknown. */
    private fun selectedVideoFrameRate(tracks: androidx.media3.common.Tracks): Float? {
        for (group in tracks.groups) {
            if (group.type != C.TRACK_TYPE_VIDEO) continue
            for (i in 0 until group.length) {
                if (!group.isTrackSelected(i)) continue
                val fps = group.getTrackFormat(i).frameRate
                if (fps > 0f) return fps
            }
        }
        return null
    }

    /**
     * Switch the display to a refresh rate that shows video at [fps] evenly
     * (see FrameRateSwitcher), once per player screen. When the switch
     * blanks the TV, playback that hasn't started yet holds until the display
     * is back: the first seconds (and their sound, through a receiver) went
     * missing in the resync. Playback already under way just carries on.
     */
    private fun matchFrameRate(fps: Float?) {
        if (frameRateMatched || fps == null || fps <= 0f) return
        val exo = player ?: return
        val switcher = frameRates() ?: return
        frameRateMatched = true
        if (!matchFrameRateEnabled) {
            switcher.releaseNow()
            return
        }
        // Android 12+: the surface's own frame-rate vote decides, and Media3's
        // seamless-only vote on the same surface would replace it.
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.S) {
            exo.videoChangeFrameRateStrategy = C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_OFF
        }
        val holdable = !exo.isPlaying && exo.playWhenReady
        val waits = switcher.match(fps, surfaceView?.holder) {
            // Not if anything else started or paused it meanwhile: the user
            // (keys, on-screen controls), or a system panel over the player
            // (onPause). Each ends the hold.
            if (!heldForFrameRate) return@match
            heldForFrameRate = false
            if (player === exo && !playbackRefused) exo.playWhenReady = true
        }
        if (waits && holdable) {
            heldForFrameRate = true
            exo.playWhenReady = false
        }
    }

    /** The player screen is done with the display: back to its default mode,
     *  after a moment when moving on to the next episode (which may keep it;
     *  FrameRateSwitcher.release). */
    private fun releaseFrameRate() {
        heldForFrameRate = false
        if (!frameRateMatched) return
        frameRateMatched = false
        frameRates()?.release(nextComing = navigatedToNext)
    }

    private fun frameRates(): FrameRateSwitcher? =
        (activity as? tv.onscreen.android.ui.MainActivity)?.frameRates

    private fun playSource(source: PlaybackSource) {
        val exo = player ?: return
        when (source) {
            is PlaybackSource.DirectPlay -> {
                // Container subtitle tracks come from ExoPlayer's own
                // extractor; side-load only the EXTERNAL files (OpenSubtitles
                // downloads), which live server-side, not in the container.
                // Direct play goes through DefaultMediaSourceFactory, which —
                // unlike the hand-built HlsMediaSource below — honours
                // subtitleConfigurations.
                val externalSubs = subtitleConfigurations(
                    viewModel.uiState.value.subtitleSources.filter { it.embeddedIndex == null },
                )
                exo.setMediaItem(
                    MediaItem.Builder()
                        .setUri(Uri.parse(source.url))
                        .setSubtitleConfigurations(externalSubs)
                        .build(),
                )
                exo.prepare()
                if (source.startMs > 0) exo.seekTo(source.startMs)
                exo.playWhenReady = true
            }
            is PlaybackSource.Hls -> {
                // playlistUrl is clean (see StreamTokenVault); the resolver puts
                // the playlist's `?token=` back on the wire. Segment/variant
                // URIs carry their own server-embedded token and pass through.
                // Timeouts and warm-up retries: see TranscodeHls.
                val factory = tv.onscreen.android.playback.StreamTokenVault.resolverFactory(
                    tv.onscreen.android.playback.TranscodeHls.httpFactory(),
                )
                val hlsSource = HlsMediaSource.Factory(factory)
                    .setLoadErrorHandlingPolicy(tv.onscreen.android.playback.TranscodeHls.errorPolicy())
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
                // Re-base cue timestamps for a resumed session: the server's
                // cached VTT is absolute content time, but this HLS timeline
                // starts at zero at the resume point — unshifted, the cues
                // shown were from hlsOffsetMs EARLIER in the film.
                val vttOffset = viewModel.hlsOffsetMs
                val subFactory = if (vttOffset > 0) {
                    androidx.media3.datasource.DataSource.Factory {
                        ShiftedVttDataSource(factory.createDataSource(), vttOffset)
                    }
                } else {
                    factory
                }
                val subtitleSources = subtitleConfigurations().map { cfg ->
                    androidx.media3.exoplayer.source.SingleSampleMediaSource
                        .Factory(subFactory)
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
    private fun subtitleConfigurations(
        sources: List<SubtitleTrackSource> = viewModel.uiState.value.subtitleSources,
    ): List<MediaItem.SubtitleConfiguration> =
        sources.map { s ->
            MediaItem.SubtitleConfiguration.Builder(Uri.parse(s.url))
                .setMimeType(androidx.media3.common.MimeTypes.TEXT_VTT)
                .setLanguage(s.language.ifBlank { null })
                .setLabel(s.label)
                // The id survives into the TrackGroup's Format, letting the
                // picker select THIS track rather than "any track with the
                // same language" — same-language duplicates (SDH + full,
                // forced + full) were unreachable before.
                .setId(s.trackId)
                .setSelectionFlags(if (s.forced) C.SELECTION_FLAG_FORCED else 0)
                .build()
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
        // - Speed: audiobooks and their chapter files only (a 2× movie is
        //   rarely what users want, and music stays at 1×).
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
        if (AudiobookSpeed.hasSpeed(currentItemType)) secondary.add(sp)

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
        val offset = viewModel.hlsOffsetMs
        val rawTarget = exo.currentPosition + deltaMs
        // Rewinding past the head of a resumed session: the earlier content
        // is not in this session's window at all, so seekTo could only clamp
        // to the resume point — "rewind" appeared to do nothing at the exact
        // moment a user wants it (they resumed too far in). Re-issue the
        // session at the content target instead. Leanback's scrub bar is only
        // focusable when a trickplay provider exists, so on most titles these
        // keys are the ONLY seek affordance.
        if (rawTarget < 0 && offset > 0) {
            viewModel.reissueAt((offset + rawTarget).coerceAtLeast(0L))
            return
        }
        val target = rawTarget.coerceAtLeast(0L)
        val dur = exo.duration
        val clamped = if (dur > 0 && dur != Long.MAX_VALUE && target > dur) dur else target
        exo.seekTo(clamped)
    }

    /** Toggle the player between playing and paused. Driven by the
     *  hardware-media-key handler installed in onViewCreated. */
    private fun togglePlayPause() {
        val exo = player ?: return
        heldForFrameRate = false
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
        // Hardware media keys work regardless of which overlay has focus —
        // that's their contract on every TV platform.
        when (event.keyCode) {
            KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> { togglePlayPause(); return true }
            KeyEvent.KEYCODE_MEDIA_PLAY -> { heldForFrameRate = false; player?.play(); return true }
            KeyEvent.KEYCODE_MEDIA_PAUSE -> { heldForFrameRate = false; player?.pause(); return true }
            KeyEvent.KEYCODE_MEDIA_STOP -> { heldForFrameRate = false; player?.pause(); return true }
            KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> { seekRelative(SKIP_FORWARD_MS); return true }
            KeyEvent.KEYCODE_MEDIA_REWIND -> { seekRelative(-SKIP_BACK_MS); return true }
            // Track skip for music, chapter skip for a multi-file book (the
            // NEXT/PREV keys on most remotes). NEXT advances to the resolved
            // next sibling; PREVIOUS restarts the current track (no
            // previous-sibling resolver yet).
            KeyEvent.KEYCODE_MEDIA_NEXT -> { nextEpisode?.let { goToNextEpisode(it) }; return true }
            KeyEvent.KEYCODE_MEDIA_PREVIOUS -> { player?.seekTo(0); return true }
        }
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
        // Same stack ExoPlayer.Builder builds by default (DefaultMediaSourceFactory
        // over DefaultDataSource over DefaultHttpDataSource), wrapped in the
        // StreamTokenVault resolver: direct-play MediaItems carry CLEAN urls so
        // the token never reaches the MediaSession this player is parked in for
        // background audio, and the resolver re-attaches `?token=` per request.
        // Covers the service's auto-advance setMediaItem too, since the service
        // drives this same player instance.
        val dsFactory = tv.onscreen.android.playback.StreamTokenVault.resolverFactory(
            androidx.media3.datasource.DefaultDataSource.Factory(ctx, DefaultHttpDataSource.Factory()),
        )
        val builder = ExoPlayer.Builder(ctx)
            .setMediaSourceFactory(
                androidx.media3.exoplayer.source.DefaultMediaSourceFactory(dsFactory),
            )
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
        return builder.build().apply {
            // Debug builds: Media3's stock event log (track groups, selection
            // changes, load errors) — the only practical way to diagnose
            // silent side-load failures on a physical device over adb.
            if (tv.onscreen.android.BuildConfig.DEBUG) {
                addAnalyticsListener(androidx.media3.exoplayer.util.EventLogger())
            }
            // Declare the usage and let Media3 manage audio focus. Without
            // this the fragment player never REQUESTED focus, so other
            // apps' audio kept playing underneath, and nothing paused us
            // when another app took the output.
            setAudioAttributes(
                androidx.media3.common.AudioAttributes.Builder()
                    .setUsage(C.USAGE_MEDIA)
                    .setContentType(C.AUDIO_CONTENT_TYPE_MOVIE)
                    .build(),
                /* handleAudioFocus= */ true,
            )
        }
    }

    private fun showSpeedPicker() {
        val labels = AudiobookSpeed.PRESETS.map { AudiobookSpeed.label(it) }.toTypedArray()
        // Check the current speed so the user can see what's active (was a plain
        // setItems list with no indication of the current selection). -1 (none
        // checked) when the book is at a speed set elsewhere between presets.
        val checked = AudiobookSpeed.presetIndex(playbackSpeed)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.speed)
            .setSingleChoiceItems(labels, checked) { d, idx ->
                val chosen = AudiobookSpeed.PRESETS[idx]
                // Straight onto the live player — pitch is kept (Media3's
                // PlaybackParameters(speed) leaves pitch at 1). The same
                // instance goes to the background service on HOME / BACK, so
                // the speed goes with it. Then saved for the book.
                player?.setPlaybackSpeed(chosen)
                showSpeed(chosen)
                viewModel.setListeningRate(chosen)
                d.dismiss()
            }
            .show()
            .trackOpen()
    }

    /**
     * Put the player at the right speed for the item on screen: an audiobook
     * (or a chapter of one) at the book's saved speed once the ViewModel has
     * it, anything else at 1×. The player can be one reclaimed from the
     * background service, so it may already be at a book's speed — which is
     * right for that book and wrong for anything else. Until the lookup
     * returns, a book keeps whatever the player is at.
     */
    private fun applyListeningSpeed(rate: Float?) {
        // A book's saved speed can arrive after HOME handed the player over:
        // it still belongs on that player, or the book plays on at 1x.
        val exo = player ?: parkedPlayerOfThisItem() ?: return
        val current = exo.playbackParameters.speed
        val target = when {
            !AudiobookSpeed.hasSpeed(currentItemType) -> AudiobookSpeed.NORMAL
            rate != null -> rate
            else -> current
        }
        if (!AudiobookSpeed.same(current, target)) exo.setPlaybackSpeed(target)
        showSpeed(target)
    }

    /** Reflect [speed] in the picker's checkmark and the action's label. */
    private fun showSpeed(speed: Float) {
        playbackSpeed = speed
        speedAction?.label1 = getString(R.string.speed_label, AudiobookSpeed.label(speed))
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
            // Direct play — pick the exact track by its ordinal (container
            // order matches audio_streams order). Selecting by language made
            // same-language duplicates (5.1 + stereo, commentary) unreachable.
            selectAudioByOrdinal(idx)
        }
    }

    /** One selectable subtitle row: either a container track ExoPlayer
     *  extracted itself (direct play — addressed by its ordinal among
     *  non-side-loaded text groups) or a side-loaded VTT (addressed by the
     *  format id stamped in subtitleConfigurations). */
    private data class SubtitleRow(
        val label: String,
        val trackId: String?,      // side-load format id, null = container track
        val containerOrdinal: Int, // ordinal among container text groups, -1 = side-load
    )

    private fun subtitleRows(): List<SubtitleRow> {
        val sources = viewModel.uiState.value.subtitleSources
        val isHls = viewModel.uiState.value.source is PlaybackSource.Hls
        fun decorate(name: String, forced: Boolean, sdh: Boolean, external: Boolean) = buildString {
            append(name)
            if (forced) append(" (forced)")
            if (sdh) append(" (SDH)")
            if (external) append(" (downloaded)")
        }
        return if (isHls) {
            // Every renderable track is a side-load (the session playlist
            // carries no text streams). Image-based tracks are already
            // filtered out — the server can't serve them as VTT.
            sources.map { s ->
                SubtitleRow(decorate(s.label, s.forced, s.sdh, s.embeddedIndex == null), s.trackId, -1)
            }
        } else {
            // Direct play: container tracks render natively (PGS included) in
            // container order, plus any external side-loads on the end.
            subtitleStreams.mapIndexed { ord, s ->
                val name = s.title.ifBlank { s.language.ifBlank { "Track ${s.index}" } }
                SubtitleRow(decorate(name, s.forced, s.sdh, false), null, ord)
            } + sources.filter { it.embeddedIndex == null }.map { s ->
                SubtitleRow(decorate(s.label, s.forced, s.sdh, true), s.trackId, -1)
            }
        }
    }

    private fun showSubtitlePicker() {
        val rows = subtitleRows()
        val labels = mutableListOf(getString(R.string.off))
        labels.addAll(rows.map { it.label })
        // "Find more online…" entry tacks an OpenSubtitles search on
        // the end of the picker. Index = labels.size — beyond every
        // track row — so the radio-row indices for real tracks don't
        // shift around.
        val findMoreIdx = labels.size
        labels.add(getString(R.string.subtitles_find_more))

        // Active-row detection from the player's ACTUAL selection state
        // (Tracks.Group.isSelected), not from preferred-language params —
        // the old raw-string compare against normalized selector state
        // showed "Off" checked while subtitles were rendering.
        val selected = selectedSubtitleRow(rows)
        val checked = if (selected < 0) 0 else selected + 1

        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.subtitles)
            .setSingleChoiceItems(labels.toTypedArray(), checked) { d, idx ->
                d.dismiss()
                when (idx) {
                    0 -> disableSubtitles()
                    findMoreIdx -> showOnlineSubtitleSearch()
                    else -> selectSubtitleRow(rows[idx - 1])
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

    /** Direct-play audio selection by ORDINAL among the player's audio
     *  groups (container order matches `audio_streams` order), not by
     *  language — two same-language tracks (5.1 + stereo, commentary)
     *  were unreachable when selected by language string. */
    private fun selectAudioByOrdinal(ordinal: Int) {
        val exo = player ?: return
        val group = exo.currentTracks.groups
            .filter { it.type == C.TRACK_TYPE_AUDIO }
            .getOrNull(ordinal) ?: return
        exo.trackSelectionParameters = exo.trackSelectionParameters.buildUpon()
            .setOverrideForType(
                androidx.media3.common.TrackSelectionOverride(group.mediaTrackGroup, 0),
            )
            .build()
        // Keep the picker's checkmark in sync. Unlike HLS (where
        // switchAudioStream re-issues the session and the source re-emit
        // carries the new active track), direct-play swaps happen entirely
        // client-side, so nothing else updates activeAudioIndex — without
        // this the radio dialog re-opens checked on the old track.
        activeAudioIndex = ordinal
    }

    /** The user's explicit in-session subtitle choice: a SubtitleRow key
     *  ("id:<formatId>" / "ord:<n>"), "off", or null = no explicit choice
     *  (saved preferences apply). Re-applied after every player rebuild —
     *  an audio-track switch re-issues the HLS session, and before this
     *  existed the rebuild silently re-applied the SAVED preference,
     *  undoing whatever the user had picked minutes earlier. */
    private var userSubtitleChoice: String? = null

    /** Our stamped side-load id, extracted from a Format id — or null for a
     *  container track. NOT plain equality: MergingMediaSource prefixes each
     *  child's ids with its index ("sub:emb:3" surfaces as "1:sub:emb:3"),
     *  which made an == match fail silently — the track was never selected,
     *  so the VTT never even loaded and the picker's choice did nothing.
     *  Verified against a live Fire TV EventLogger track dump. */
    private fun sideLoadIdOf(g: androidx.media3.common.Tracks.Group): String? {
        val id = g.mediaTrackGroup.getFormat(0).id ?: return null
        val at = id.indexOf("sub:")
        return if (at >= 0) id.substring(at) else null
    }

    /** Text track groups that came from the source itself, in order —
     *  excludes our side-loads (identified by the stamped id marker). */
    private fun containerTextGroups(): List<androidx.media3.common.Tracks.Group> =
        player?.currentTracks?.groups
            ?.filter { it.type == C.TRACK_TYPE_TEXT }
            ?.filterNot { sideLoadIdOf(it) != null }
            ?: emptyList()

    private fun sideLoadedTextGroup(trackId: String): androidx.media3.common.Tracks.Group? =
        player?.currentTracks?.groups
            ?.filter { it.type == C.TRACK_TYPE_TEXT }
            ?.firstOrNull { sideLoadIdOf(it) == trackId }

    /** Index into [rows] of the currently-rendering track, or -1 for off. */
    private fun selectedSubtitleRow(rows: List<SubtitleRow>): Int {
        val groups = player?.currentTracks?.groups
            ?.filter { it.type == C.TRACK_TYPE_TEXT } ?: return -1
        val active = groups.firstOrNull { it.isSelected } ?: return -1
        val sideId = sideLoadIdOf(active)
        if (sideId != null) {
            return rows.indexOfFirst { it.trackId == sideId }
        }
        val ordinal = containerTextGroups().indexOfFirst { it.mediaTrackGroup == active.mediaTrackGroup }
        return rows.indexOfFirst { it.containerOrdinal == ordinal }
    }

    private fun selectSubtitleRow(row: SubtitleRow) {
        userSubtitleChoice = row.trackId?.let { "id:$it" } ?: "ord:${row.containerOrdinal}"
        applySubtitleChoice()
    }

    private fun disableSubtitles() {
        userSubtitleChoice = "off"
        val exo = player ?: return
        exo.trackSelectionParameters = exo.trackSelectionParameters.buildUpon()
            .clearOverridesOfType(C.TRACK_TYPE_TEXT)
            .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            .build()
    }

    /** Apply [userSubtitleChoice] against the player's CURRENT track groups.
     *  Called on pick, and again from onTracksChanged after a rebuild —
     *  overrides bind to concrete TrackGroup instances, so a re-issued
     *  session needs the choice re-resolved against its new groups. */
    private fun applySubtitleChoice() {
        val exo = player ?: return
        val choice = userSubtitleChoice ?: return
        if (choice == "off") {
            exo.trackSelectionParameters = exo.trackSelectionParameters.buildUpon()
                .clearOverridesOfType(C.TRACK_TYPE_TEXT)
                .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
                .build()
            return
        }
        val group = when {
            choice.startsWith("id:") -> sideLoadedTextGroup(choice.removePrefix("id:"))
            choice.startsWith("ord:") ->
                containerTextGroups().getOrNull(choice.removePrefix("ord:").toIntOrNull() ?: -1)
            else -> null
        } ?: return // groups not loaded yet — onTracksChanged retries
        exo.trackSelectionParameters = exo.trackSelectionParameters.buildUpon()
            .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
            .setOverrideForType(
                androidx.media3.common.TrackSelectionOverride(group.mediaTrackGroup, 0),
            )
            .build()
    }

    private fun startUpNextWatcher() {
        upNextJob?.cancel()
        if (nextEpisode == null) return
        // Music tracks and book chapters chain at EOS only — the
        // lead-in overlay would clip the last ~25 s of the song
        // (where the outro / fade lives) or of the chapter. Episodes
        // still get the overlay (the credits roll covers the same
        // window, so the early countdown isn't a content loss there).
        if (isAudioItem()) return
        upNextJob = viewLifecycleOwner.lifecycleScope.launch {
            while (isActive) {
                delay(1000)
                val exo = player ?: continue
                // Pause = "I'm stepping away", the same gate the skip-marker
                // watcher applies. Without it, pausing during the credits
                // still popped the overlay and auto-started the next episode
                // while the user was out of the room.
                if (!exo.isPlaying) continue
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

        // BACK on the overlay = Cancel, NOT "exit playback". Handled by
        // overlayBackCallback (armed here) rather than a KEYCODE_BACK key
        // listener on the buttons, which Android 16's back dispatch bypasses.
        syncOverlayBackCallback()

        // Never stack countdowns: a second showUpNextOverlay (e.g. EOS firing
        // while the lead-in countdown is already running) would otherwise leave
        // two timers racing to advance. dismissUpNext + goToNextEpisode also
        // cancel this job.
        countdownJob?.cancel()
        countdownJob = viewLifecycleOwner.lifecycleScope.launch {
            var sec = UP_NEXT_COUNTDOWN_SEC
            while (sec >= 1) {
                labelView.text = "UP NEXT · ${sec}s"
                delay(1000)
                if (!isActive) return@launch
                // Hold the countdown while paused — the user who catches the
                // card and hits pause has explicitly asked to stay.
                if (player?.isPlaying != false) sec--
            }
            goToNextEpisode(next)
        }

        playBtn.requestFocus()
    }

    private fun dismissUpNext(permanent: Boolean) {
        countdownJob?.cancel()
        val hadFocus = upNextOverlay?.hasFocus() == true
        upNextOverlay?.visibility = View.GONE
        syncOverlayBackCallback()
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

        // The book's next chapter plays at the speed this one was at: the
        // next fragment builds a fresh player, which would otherwise start at
        // 1× until the book's speed is looked up again. nextEpisode is only
        // ever a chapter of this chapter's own book (AudiobookChapters).
        val bookSpeed = currentItem?.let { cur ->
            AudiobookSpeed.carried(
                fromType = cur.type,
                fromBookId = AudiobookSpeed.bookIdOf(cur.type, cur.id, cur.parent_id),
                toType = ep.type,
                rate = player?.playbackParameters?.speed ?: playbackSpeed,
            )
        }

        // Pop this fragment's own back-stack entry before pushing the next
        // episode's, so the container keeps exactly one recorded owner.
        // Previously the advance replaced the fragment WITHOUT adding to the
        // back stack, while the entry that brought us here still recorded
        // "remove DetailFragment, add PlaybackFragment#1" — so BACK from
        // episode 2 re-added the detail screen over a still-playing episode 2
        // rather than leaving playback.
        fm.popBackStack()
        fm.beginTransaction()
            .replace(R.id.main_container, newInstance(ep.id, 0, bookSpeed))
            .addToBackStack(null)
            .commit()
    }

    /** [leaveOnCancel]: BACK on the dialog also leaves playback, instead of
     *  dropping the user back onto a player whose controls still work. */
    private fun showErrorDialog(message: String, leaveOnCancel: Boolean = false) {
        val (title, body) = when {
            message == "content_restricted" ->
                getString(R.string.content_restricted) to ""
            // Admin stop — "playback_stopped:<sentence>" from the playback.stop
            // SSE event, a 403 PLAYBACK_STOPPED heartbeat or a refused
            // session start. The sentence is shown verbatim.
            PlaybackStop.isSentinel(message) ->
                getString(R.string.playback_stopped_title) to PlaybackStop.sentenceOf(message)
            // Mid-session heartbeat 403 that is not the watch limit — library
            // access revoked or rating ceiling lowered (ProgressTracker).
            message == ProgressTracker.CONTENT_REVOKED ->
                getString(R.string.content_revoked) to ""
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
            .apply {
                if (leaveOnCancel) {
                    setOnCancelListener { if (isAdded) parentFragmentManager.popBackStack() }
                }
            }
            .create()
            .focusableOnTv()
            .also { it.trackOpen() }
            .show()
    }

    /** True for audio content (music tracks, audiobooks and their chapter
     *  files — see [AudioItemTypes]) — the items that keep playing across
     *  backgrounding via the MediaSessionService handoff. Video is
     *  released on stop instead. */
    private fun isAudioItem(): Boolean = AudioItemTypes.isAudio(currentItemType)

    /** Content-time duration for progress reports + completion ratios. The
     *  arithmetic lives in [PlaybackHelper.contentDurationMs] so it is unit
     *  testable; this just feeds it the live player/item state. */
    private fun contentDurationMs(): Long = contentDurationMs(player, viewModel.hlsOffsetMs)

    /** [contentDurationMs] for [exo] reading a session that starts at
     *  [hlsOffsetMs]: a parked player, whose session left the view model. */
    private fun contentDurationMs(exo: ExoPlayer?, hlsOffsetMs: Long): Long = PlaybackHelper.contentDurationMs(
        itemDurationMs = currentItem?.duration_ms ?: currentItem?.files?.firstOrNull()?.duration_ms,
        playerDurationMs = exo?.duration ?: 0L,
        hlsOffsetMs = hlsOffsetMs,
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
            // A panel over the player pauses it, and the display switch
            // finishing mustn't start it again behind the panel.
            heldForFrameRate = false
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
            releaseFrameRate()
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
        // Not when moving on to the next item (MEDIA_NEXT, Play Now): that
        // player is done — goToNextEpisode already reported it stopped — and
        // parking it only for the next fragment to release it again sent a
        // second 'stopped' for it.
        if (!parkedToService && !playbackEnded && !navigatedToNext && handOffAudioPlayerToService()) {
            parkedToService = true
            // The service drives progress reporting from here; stop the
            // fragment-side ticker without emitting a spurious "stopped"
            // (the track is still playing).
            progressTracker?.stop()
            // It drives the player too, until onStart takes it back
            // (reclaimAudioPlayerFromService): nothing here may seek it
            // meanwhile. The service can also release it (end of the queue,
            // a long pause), and a reference kept here reached a dead
            // player. What still needs it looks it up while it plays this
            // item (parkedPlayerOfThisItem).
            stopPlayerWatchers()
            player = null
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
        adminStopJob?.cancel()
        adminStopJob = null
        skipMarkerJob?.cancel()
        skipMarkerJob = null
        skipMarkerOverlay = null
        trickplayJob?.cancel()
        trickplayJob = null
        watchNextJob?.cancel()
        watchNextJob = null
        progressTracker?.stop()
        progressTracker = null
        releaseFrameRate()

        // Recycle the trickplay sprite-sheet bitmaps (~30 MB for a feature film)
        // now, rather than waiting for GC to reclaim them once the fragment graph
        // is collected. Runs before the parked-path early-return below so it fires
        // on both teardown paths.
        (glue?.seekProvider as? TrickplaySeekProvider)?.release()

        if (parkedToService) {
            // Already handed to the service in onStop (in-app nav after
            // backgrounding, or teardown while parked) — the service
            // owns the player now. Don't release it; just drop our refs.
            // Its server session went with it (handOffSession), so this
            // only ends one the view model still owns: none, normally.
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
        //
        // NEVER park a finished player. onStop already gates on
        // !playbackEnded, but this path didn't — and ExoPlayer keeps
        // playWhenReady == true at STATE_ENDED, so the handoff's
        // liveness check waved the ended player through: the last track
        // of an album ended, the fragment popped, and the service
        // adopted a dead player — leaked instance, phantom "playing"
        // notification, endless heartbeat against a finished item.
        val handedOff = !playbackEnded && !navigatedToNext && handOffAudioPlayerToService()
        if (!handedOff) {
            player?.release()
        }
        player = null
        // The handoff path clears playerListener before parking; the
        // release path doesn't need to (release() invalidates the
        // listener list) but null it for GC hygiene either way.
        playerListener = null
        // A parked player took its server session along; this ends the
        // session only when the player was released above.
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
        // The server refused a 'playing' heartbeat (403): watch cap / allowed
        // hours, or the item left this profile's reach mid-session.
        tracker.onBlocked = { sentinel -> stopForRefusedPlayback(sentinel) }
        // Bound to the item now; the 10 s heartbeat starts when the player
        // actually plays (onIsPlayingChanged). Starting it here reported
        // "playing" at position 0 while a session was still buffering its
        // first segment, sometimes for playback that never began. A player
        // taken back from the background service is already playing.
        tracker.bind(itemId, viewModel.hlsOffsetMs)
        if (player?.isPlaying == true) tracker.start(itemId, viewModel.hlsOffsetMs)
        progressTracker = tracker
    }

    /** Tear playback down after the server refused a mid-session heartbeat
     *  (see ProgressTracker.onBlocked). Pausing alone was not a teardown: the
     *  paused player still held its stream token, BACK on the dialog left the
     *  play control live, and a paused audio player past 0 ms passed the
     *  handoff's liveness gate — so it was parked in the MediaSessionService
     *  and resumable from the system media controls. stop() keeps the
     *  position, so onStop's terminal 'stopped' report still saves the resume
     *  point (the server only gates 'playing'). */
    private fun stopForRefusedPlayback(sentinel: String) {
        // One stop, one dialog: an admin stop can arrive twice (the SSE event
        // AND the next heartbeat's 403 PLAYBACK_STOPPED).
        if (playbackRefused) return
        playbackRefused = true
        // The server may have killed the stream before its explanation got
        // here (a stopped transcode's segments 404 first), leaving a raw
        // "Playback error" up — the refusal message replaces it.
        openDialogs.toList().forEach { runCatching { it.dismiss() } }
        // A stop inside the credits window left the Up Next card frozen at
        // "UP NEXT · 1s" (with its Play Now) behind the refusal dialog.
        dismissUpNext(permanent = true)
        player?.run {
            pause()
            stop()
        }
        // Also makes the ViewModel drop (and retire) any session re-issue
        // still in flight.
        viewModel.stopForRefusal()
        showErrorDialog(sentinel, leaveOnCancel = true)
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
        val parkedMeta = tv.onscreen.android.playback.AudioHandoff.peekMetadata()
        val parkedId = parkedMeta?.itemId

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
            // player (queue ended, pause window passed, the system stopped
            // it). Drop our reference to it too: this fragment still held
            // the released player, and its teardown parked it again, so
            // replaying the track took back a dead player and landed on Home.
            playerListener = null
            player = null
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
        // The server session came back with the player (handed over in
        // handOffAudioPlayerToService): this view model owns it again.
        parkedMeta?.session?.let { viewModel.adoptSession(it) }
        player = reclaimed
        val listener = createPlayerListener()
        reclaimed.addListener(listener)
        playerListener = listener
        installProgressTracker(launchId)
        // Stopped at the handoff (onStop).
        startPlayerWatchers(launchId)
        startAdminStopWatch(launchId)
        // Same player, same speed — including one changed from the system
        // media controls while it played in the background.
        showSpeed(reclaimed.playbackParameters.speed)
    }

    /** When the user backs out of the player while audio (music, a
     *  book, a chapter file of one) is still playing, transfer
     *  ownership of the ExoPlayer to the MediaSessionService and let
     *  the service keep it alive under the system foreground
     *  notification. Returns true when the handoff happened (caller
     *  should NOT release the player).
     *
     *  Skipped for video: the surface-view rendering doesn't
     *  translate to a service notification, and the video player
     *  has already been released in onStop. */
    private fun handOffAudioPlayerToService(): Boolean {
        val exo = player ?: return false
        if (!isAudioItem()) return false
        // A finished player must never be parked: ExoPlayer keeps
        // playWhenReady == true at STATE_ENDED, so the playWhenReady gate
        // below cannot catch end-of-stream on its own.
        if (exo.playbackState == Player.STATE_ENDED) return false
        // The server refused this playback mid-session (stopForRefusedPlayback):
        // never hand it to the background service to be resumed from there.
        if (playbackRefused) return false
        if (!exo.playWhenReady && exo.currentPosition == 0L) return false
        val itemId = arguments?.getString(ARG_ITEM_ID) ?: return false
        val ctx = activity?.applicationContext ?: return false
        val item = viewModel.uiState.value.item
        // A transcoded / remuxed track streams from a server session. It
        // goes with the player: ending it when this fragment goes away
        // stopped the parked audio seconds after BACK or HOME.
        val stream = viewModel.handOffSession()
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
                // Chapters and books carry their length on the file, as
                // contentDurationMs() reads it.
                itemDurationMs = item?.duration_ms ?: item?.files?.firstOrNull()?.duration_ms,
                session = stream,
                nowPlaying = viewModel.uiState.value.nowPlaying
                    ?: item?.let { tv.onscreen.android.playback.NowPlaying.of(it) },
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
            val gen = tv.onscreen.android.playback.AudioHandoff.park(exo, meta)
            // Started service so it survives the activity going away.
            // The service reads from AudioHandoff on its next attach()
            // and binds the parked player to a Media3 MediaSession,
            // which surfaces play/pause/skip on the system rail and
            // keeps the foreground notification alive. Started a moment
            // later, and only if nothing took the player back first: a
            // fragment replaced by one for the same item reclaims it
            // within milliseconds, and starting and stopping the service
            // in that window crashed the app (see startServiceWhenSettled).
            tv.onscreen.android.playback.AudioHandoff.startServiceWhenSettled(ctx, gen)
            true
        } catch (_: Exception) {
            tv.onscreen.android.playback.AudioHandoff.clear()
            // Not handed over after all: the session stays ours to end.
            stream?.let { viewModel.adoptSession(it) }
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
