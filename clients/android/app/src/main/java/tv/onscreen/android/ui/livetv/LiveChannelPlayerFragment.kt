package tv.onscreen.android.ui.livetv

import android.annotation.SuppressLint
import android.app.AlertDialog
import android.net.Uri
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.ui.PlayerView
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.focusableOnTv
import tv.onscreen.android.ui.playback.PlaybackHelper
import javax.inject.Inject

/** Live-TV channel player. Standalone from PlaybackFragment because
 *  none of that fragment's machinery applies — no resume position,
 *  no Up Next, no chapters, no audio/subtitle pickers (the channel
 *  HLS is single-track from the tuner / proxy). The full-fat
 *  PlaybackFragment expects a media item id, which a live channel
 *  doesn't have, so we plug straight into ExoPlayer with the
 *  channel's HLS URL. */
// `@SuppressLint("UnsafeOptInUsageError")`: media3's PlayerView setter
// chain + HlsMediaSource.Factory + DefaultHttpDataSource are tagged
// `@UnstableApi` library-wide, but the official Android TV samples
// (developer.android.com/training/tv/playback/exoplayer) consistently
// use them. Suppressing the lint here keeps the opt-in confined to
// this class — Kotlin's `@OptIn` style would propagate to every
// fragment that just *references* us, which cascades up to
// MainActivity for no real reason.
@SuppressLint("UnsafeOptInUsageError")
@AndroidEntryPoint
class LiveChannelPlayerFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs

    private var player: ExoPlayer? = null
    private var playerView: PlayerView? = null
    /** The stream-error dialog while showing. Held so the fragment can take it
     *  down: it is attached to the ACTIVITY window and therefore outlives this
     *  fragment unless dismissed explicitly. */
    private var errorDialog: android.app.AlertDialog? = null
    /** Resolved stream URL — built once on first start, reused when the
     *  player is rebuilt after an onStop release so we don't re-resolve
     *  the server URL / token on every foreground cycle. */
    private var streamUrl: String? = null

    companion object {
        private const val ARG_CHANNEL_ID = "channel_id"
        private const val ARG_CHANNEL_NAME = "channel_name"

        fun newInstance(channelId: String, channelName: String): LiveChannelPlayerFragment {
            return LiveChannelPlayerFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_CHANNEL_ID, channelId)
                    putString(ARG_CHANNEL_NAME, channelName)
                }
            }
        }
    }

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val view = PlayerView(requireContext()).apply {
            useController = true
            controllerHideOnTouch = false
            // Live streams have no scrubber — controller still gives
            // pause/play + audio routing affordances.
            setShowFastForwardButton(false)
            setShowRewindButton(false)
            setShowNextButton(false)
            setShowPreviousButton(false)
            setBackgroundColor(android.graphics.Color.BLACK)
        }
        playerView = view
        return view
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val channelId = arguments?.getString(ARG_CHANNEL_ID) ?: return

        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = (prefs.serverUrl.first() ?: "").trimEnd('/')
            // Live channel routes don't ship a per-stream token; pass the
            // purpose=asset token via ?token= (never the general access
            // token, which the server rejects in a URL). NOTE: the live
            // stream route must be mounted under RequiredAllowQueryToken
            // for query-token auth to apply — see the live-stream-auth
            // follow-up.
            val token = prefs.assetToken.first() ?: ""
            // Guard the empty-token case: the live route is behind
            // RequiredAllowQueryToken, so an empty `?token=` 401s and the
            // user just stares at a black PlayerView. Surface it instead.
            if (token.isEmpty()) {
                showError(getString(R.string.live_channel_error))
                return@launch
            }
            streamUrl = "$serverUrl/api/v1/tv/channels/$channelId/stream.m3u8?token=$token"
            startPlayer()
        }
    }

    /** Build the ExoPlayer and start playback of [streamUrl]. Idempotent
     *  per-foreground: releases any existing player first so onStart
     *  after an onStop park rebuilds cleanly. Mirrors PlaybackFragment's
     *  longer transcode timeouts + retry policy (default 8 s connect/read
     *  with no retries is too tight for a tuner / proxy warming up). */
    private fun startPlayer() {
        val url = streamUrl ?: return
        releasePlayer()

        val exo = ExoPlayer.Builder(requireContext()).build()
        // DefaultHttpDataSource's 8 s connect / 8 s read defaults are too
        // tight for the tuner / proxy spinning up the first segment, and
        // the default load-error policy gives up after one try. Mirror the
        // main player's 30 s/60 s + 6-retry profile so a slow first
        // segment doesn't fail the channel.
        val httpFactory = DefaultHttpDataSource.Factory()
            .setConnectTimeoutMs(30_000)
            .setReadTimeoutMs(60_000)
            .setAllowCrossProtocolRedirects(true)
            .setUserAgent("OnScreen-Android/1.0 (ExoPlayer)")
        val errorPolicy =
            androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy(6)
        val source = HlsMediaSource.Factory(httpFactory)
            .setLoadErrorHandlingPolicy(errorPolicy)
            .createMediaSource(MediaItem.fromUri(Uri.parse(url)))
        exo.addListener(playerListener)
        exo.setMediaSource(source)
        exo.prepare()
        exo.playWhenReady = true
        playerView?.player = exo
        player = exo
    }

    /** Screen-on flag + error surfacing, mirroring PlaybackFragment's
     *  createPlayerListener. Toggling FLAG_KEEP_SCREEN_ON on isPlaying
     *  keeps the TV screensaver from kicking in mid-broadcast, and
     *  releases it the moment playback stops. */
    private val playerListener = object : Player.Listener {
        override fun onIsPlayingChanged(isPlaying: Boolean) {
            val window = activity?.window ?: return
            if (isPlaying) {
                window.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            } else {
                window.clearFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            }
        }

        override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
            // Surface the failure with a Retry instead of leaving the user
            // on a silent black screen. Retry rebuilds the player from the
            // already-resolved stream URL — minus its ?token= credential,
            // which doesn't belong in a dialog on a TV screen.
            val cause = error.cause
            val urlPart = if (cause is androidx.media3.datasource.HttpDataSource.HttpDataSourceException) {
                "\n${PlaybackHelper.sanitizeUriForDisplay(cause.dataSpec.uri)}"
            } else ""
            showError("${error.errorCodeName}: ${error.message}$urlPart")
        }
    }

    private fun showError(message: String) {
        if (!isAdded) return
        errorDialog?.dismiss()
        errorDialog = AlertDialog.Builder(requireContext())
            .setTitle(R.string.live_channel_error)
            .setMessage(message)
            .setPositiveButton(R.string.retry) { d, _ ->
                d.dismiss()
                // The dialog belongs to the ACTIVITY window, so it outlives this
                // fragment. MainActivity's SCREEN_ON home-reset replaces us with
                // HomeFragment without touching it, leaving the dialog on screen
                // over Home — and Retry then ran ExoPlayer.Builder(requireContext())
                // on a detached fragment, which throws.
                if (isAdded) startPlayer()
            }
            .setNegativeButton(android.R.string.cancel) { d, _ ->
                d.dismiss()
                if (isAdded) parentFragmentManager.popBackStack()
            }
            .create()
            // Button-only dialog: without this neither Retry nor Cancel can take
            // focus on a TV, so Retry is unreachable and only BACK (= Cancel) works.
            .focusableOnTv()
            .also { it.show() }
    }

    private fun dismissErrorDialog() {
        errorDialog?.dismiss()
        errorDialog = null
    }

    private fun releasePlayer() {
        // Drop the screen-on flag whenever we tear the player down so a
        // backgrounded channel doesn't hold the screen awake.
        activity?.window?.clearFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        player?.run {
            removeListener(playerListener)
            release()
        }
        player = null
        playerView?.player = null
    }

    override fun onStart() {
        super.onStart()
        // Rebuild the player when returning to the foreground after onStop
        // released it. Skipped on the very first start — onViewCreated
        // resolves the URL asynchronously and calls startPlayer() itself
        // (streamUrl is still null here on the cold path).
        if (player == null && streamUrl != null) {
            startPlayer()
        }
    }

    override fun onStop() {
        super.onStop()
        // Take the dialog down with us. It is an activity-window dialog, so
        // nothing else does.
        dismissErrorDialog()
        // Release the decoder / tuner the moment the activity stops
        // (HOME / app backgrounded) instead of holding them until
        // onDestroyView — a live tuner is a scarce resource and the
        // decoder shouldn't keep running behind the launcher. onStart
        // rebuilds from the cached streamUrl. Mirrors PlaybackFragment's
        // video onStop teardown.
        releasePlayer()
    }

    override fun onDestroyView() {
        dismissErrorDialog()
        releasePlayer()
        playerView = null
        super.onDestroyView()
    }
}
