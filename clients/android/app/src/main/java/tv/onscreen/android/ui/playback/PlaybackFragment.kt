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
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.SubtitleStream
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.NotificationsRepository
import javax.inject.Inject
import kotlin.math.abs

@AndroidEntryPoint
@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
class PlaybackFragment : VideoSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var notificationsRepo: NotificationsRepository

    private lateinit var viewModel: PlaybackViewModel
    private var player: ExoPlayer? = null
    private var progressTracker: ProgressTracker? = null
    private var glue: PlaybackTransportControlGlue<LeanbackPlayerAdapter>? = null

    private var audioStreams: List<AudioStream> = emptyList()
    private var subtitleStreams: List<SubtitleStream> = emptyList()
    private var nextEpisode: ChildItem? = null
    private var serverUrl: String = ""

    private var upNextOverlay: View? = null
    private var upNextJob: Job? = null
    private var upNextShown = false

    private var audioAction: Action? = null
    private var subtitleAction: Action? = null

    /** Cross-device sync subscriber. Cancelled in onDestroyView. */
    private var syncJob: Job? = null

    companion object {
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_START_MS = "start_ms"
        private const val UPDATE_PERIOD_MS = 1000
        private const val ACTION_AUDIO_ID = 100L
        private const val ACTION_SUBTITLE_ID = 101L
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

                playSource(source)
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

                refreshSecondaryActions()
                startUpNextWatcher()
                startCrossDeviceSync(itemId)
            }
        }
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
        val exo = ExoPlayer.Builder(requireContext()).build()
        player = exo

        val adapter = LeanbackPlayerAdapter(requireContext(), exo, UPDATE_PERIOD_MS)
        val host = VideoSupportFragmentGlueHost(this)

        glue = object : PlaybackTransportControlGlue<LeanbackPlayerAdapter>(requireContext(), adapter) {
            override fun onCreateSecondaryActions(adapter: ArrayObjectAdapter) {
                super.onCreateSecondaryActions(adapter)
                audioAction = Action(ACTION_AUDIO_ID, getString(R.string.audio))
                subtitleAction = Action(ACTION_SUBTITLE_ID, getString(R.string.subtitles))
                adapter.add(audioAction)
                adapter.add(subtitleAction)
            }

            override fun onActionClicked(action: Action) {
                when (action.id) {
                    ACTION_AUDIO_ID -> showAudioPicker()
                    ACTION_SUBTITLE_ID -> showSubtitlePicker()
                    else -> super.onActionClicked(action)
                }
            }
        }.apply {
            this.host = host
            isSeekEnabled = true
        }

        exo.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                if (isPlaying) {
                    progressTracker?.start(arguments?.getString(ARG_ITEM_ID) ?: return, viewModel.hlsOffsetMs)
                } else {
                    progressTracker?.onPause()
                }
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED) {
                    progressTracker?.onStop()
                    if (nextEpisode != null) {
                        showUpNextOverlay(immediate = true)
                    } else {
                        parentFragmentManager.popBackStack()
                    }
                }
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
                val factory = DefaultHttpDataSource.Factory().setDefaultRequestProperties(mapOf())
                val hlsSource = HlsMediaSource.Factory(factory)
                    .createMediaSource(MediaItem.fromUri(Uri.parse(source.playlistUrl)))
                exo.setMediaSource(hlsSource)
                exo.prepare()
                exo.playWhenReady = true
            }
        }
    }

    private fun refreshSecondaryActions() {
        // Hide audio/subtitle buttons if no choices to make.
        val sa = subtitleAction ?: return
        val aa = audioAction ?: return
        val secondary = (glue?.controlsRow as? PlaybackControlsRow)?.secondaryActionsAdapter as? ArrayObjectAdapter
            ?: return
        secondary.clear()
        if (audioStreams.size > 1) secondary.add(aa)
        if (subtitleStreams.isNotEmpty()) secondary.add(sa)
    }

    private fun showAudioPicker() {
        if (audioStreams.isEmpty()) return
        val labels = audioStreams.mapIndexed { i, a ->
            val name = a.title.ifBlank { a.language.ifBlank { "Track ${a.index}" } }
            val ch = if (a.channels > 0) " · ${a.channels}ch" else ""
            "${i + 1}. $name$ch"
        }.toTypedArray()

        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.audio)
            .setItems(labels) { d, idx ->
                selectAudioByLanguage(audioStreams[idx].language)
                d.dismiss()
            }
            .show()
    }

    private fun showSubtitlePicker() {
        val labels = mutableListOf(getString(R.string.off))
        labels.addAll(subtitleStreams.map { s ->
            val name = s.title.ifBlank { s.language.ifBlank { "Track ${s.index}" } }
            if (s.forced) "$name (forced)" else name
        })

        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.subtitles)
            .setItems(labels.toTypedArray()) { d, idx ->
                if (idx == 0) disableSubtitles() else selectSubtitleByLanguage(subtitleStreams[idx - 1].language)
                d.dismiss()
            }
            .show()
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
        upNextJob?.cancel()
        upNextJob = null
        syncJob?.cancel()
        syncJob = null
        progressTracker?.stop()
        progressTracker = null
        player?.release()
        player = null
        viewModel.stopActiveTranscode()
    }
}
