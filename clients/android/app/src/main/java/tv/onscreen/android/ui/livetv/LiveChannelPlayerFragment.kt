package tv.onscreen.android.ui.livetv

import android.net.Uri
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.ui.PlayerView
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.data.prefs.ServerPrefs
import javax.inject.Inject

/** Live-TV channel player. Standalone from PlaybackFragment because
 *  none of that fragment's machinery applies — no resume position,
 *  no Up Next, no chapters, no audio/subtitle pickers (the channel
 *  HLS is single-track from the tuner / proxy). The full-fat
 *  PlaybackFragment expects a media item id, which a live channel
 *  doesn't have, so we plug straight into ExoPlayer with the
 *  channel's HLS URL. */
@OptIn(UnstableApi::class)
@AndroidEntryPoint
class LiveChannelPlayerFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs

    private var player: ExoPlayer? = null
    private var playerView: PlayerView? = null

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
            // Live channel routes don't ship a per-stream token; they
            // accept the user's standard access token via ?token= the
            // same way direct-play files do.
            val token = prefs.accessToken.first() ?: ""
            val url = "$serverUrl/api/v1/tv/channels/$channelId/stream.m3u8?token=$token"

            val exo = ExoPlayer.Builder(requireContext()).build()
            val source = HlsMediaSource.Factory(DefaultHttpDataSource.Factory())
                .createMediaSource(MediaItem.fromUri(Uri.parse(url)))
            exo.setMediaSource(source)
            exo.prepare()
            exo.playWhenReady = true
            playerView?.player = exo
            player = exo
        }
    }

    override fun onPause() {
        super.onPause()
        player?.playWhenReady = false
    }

    override fun onResume() {
        super.onResume()
        player?.playWhenReady = true
    }

    override fun onDestroyView() {
        player?.release()
        player = null
        playerView?.player = null
        playerView = null
        super.onDestroyView()
    }
}
