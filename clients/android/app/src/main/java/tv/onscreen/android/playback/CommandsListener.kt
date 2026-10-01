package tv.onscreen.android.playback

import androidx.media3.common.AudioAttributes
import androidx.media3.common.DeviceInfo
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Metadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.Timeline
import androidx.media3.common.TrackSelectionParameters
import androidx.media3.common.Tracks
import androidx.media3.common.VideoSize
import androidx.media3.common.text.Cue
import androidx.media3.common.text.CueGroup
import androidx.media3.common.util.UnstableApi

/**
 * Hears a player's events for [listener], with the commands a wrapper of the
 * player offers ([commands]) in place of the player's own. A ForwardingPlayer
 * that narrows getAvailableCommands still hands its listeners the wrapped
 * player's commands event as it came, and a media session takes the commands
 * that event carries, not getAvailableCommands. Everything else passes
 * through; ForwardingPlayer already gives onEvents the wrapper, so reads made
 * there agree.
 *
 * Every method is forwarded by hand, as Media3's ForwardingPlayer does:
 * Kotlin's `by` delegation skips a Java interface's default methods, and all
 * of Player.Listener's are. CommandsListenerTest checks none is missed.
 */
@UnstableApi
class CommandsListener(
    private val listener: Player.Listener,
    private val commands: () -> Player.Commands,
) : Player.Listener {

    override fun onAvailableCommandsChanged(availableCommands: Player.Commands) =
        listener.onAvailableCommandsChanged(commands())

    override fun onEvents(player: Player, events: Player.Events) = listener.onEvents(player, events)

    override fun onTimelineChanged(timeline: Timeline, reason: Int) = listener.onTimelineChanged(timeline, reason)

    override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) =
        listener.onMediaItemTransition(mediaItem, reason)

    override fun onTracksChanged(tracks: Tracks) = listener.onTracksChanged(tracks)

    override fun onMediaMetadataChanged(mediaMetadata: MediaMetadata) = listener.onMediaMetadataChanged(mediaMetadata)

    override fun onPlaylistMetadataChanged(mediaMetadata: MediaMetadata) =
        listener.onPlaylistMetadataChanged(mediaMetadata)

    override fun onIsLoadingChanged(isLoading: Boolean) = listener.onIsLoadingChanged(isLoading)

    override fun onTrackSelectionParametersChanged(parameters: TrackSelectionParameters) =
        listener.onTrackSelectionParametersChanged(parameters)

    override fun onPlaybackStateChanged(playbackState: Int) = listener.onPlaybackStateChanged(playbackState)

    override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) =
        listener.onPlayWhenReadyChanged(playWhenReady, reason)

    override fun onPlaybackSuppressionReasonChanged(playbackSuppressionReason: Int) =
        listener.onPlaybackSuppressionReasonChanged(playbackSuppressionReason)

    override fun onIsPlayingChanged(isPlaying: Boolean) = listener.onIsPlayingChanged(isPlaying)

    override fun onRepeatModeChanged(repeatMode: Int) = listener.onRepeatModeChanged(repeatMode)

    override fun onShuffleModeEnabledChanged(shuffleModeEnabled: Boolean) =
        listener.onShuffleModeEnabledChanged(shuffleModeEnabled)

    override fun onPlayerError(error: PlaybackException) = listener.onPlayerError(error)

    override fun onPlayerErrorChanged(error: PlaybackException?) = listener.onPlayerErrorChanged(error)

    override fun onPositionDiscontinuity(
        oldPosition: Player.PositionInfo,
        newPosition: Player.PositionInfo,
        reason: Int,
    ) = listener.onPositionDiscontinuity(oldPosition, newPosition, reason)

    override fun onPlaybackParametersChanged(playbackParameters: PlaybackParameters) =
        listener.onPlaybackParametersChanged(playbackParameters)

    override fun onSeekBackIncrementChanged(seekBackIncrementMs: Long) =
        listener.onSeekBackIncrementChanged(seekBackIncrementMs)

    override fun onSeekForwardIncrementChanged(seekForwardIncrementMs: Long) =
        listener.onSeekForwardIncrementChanged(seekForwardIncrementMs)

    override fun onMaxSeekToPreviousPositionChanged(maxSeekToPreviousPositionMs: Long) =
        listener.onMaxSeekToPreviousPositionChanged(maxSeekToPreviousPositionMs)

    override fun onAudioSessionIdChanged(audioSessionId: Int) = listener.onAudioSessionIdChanged(audioSessionId)

    override fun onAudioAttributesChanged(audioAttributes: AudioAttributes) =
        listener.onAudioAttributesChanged(audioAttributes)

    override fun onVolumeChanged(volume: Float) = listener.onVolumeChanged(volume)

    override fun onSkipSilenceEnabledChanged(skipSilenceEnabled: Boolean) =
        listener.onSkipSilenceEnabledChanged(skipSilenceEnabled)

    override fun onDeviceInfoChanged(deviceInfo: DeviceInfo) = listener.onDeviceInfoChanged(deviceInfo)

    override fun onDeviceVolumeChanged(volume: Int, muted: Boolean) = listener.onDeviceVolumeChanged(volume, muted)

    override fun onVideoSizeChanged(videoSize: VideoSize) = listener.onVideoSizeChanged(videoSize)

    override fun onSurfaceSizeChanged(width: Int, height: Int) = listener.onSurfaceSizeChanged(width, height)

    override fun onRenderedFirstFrame() = listener.onRenderedFirstFrame()

    override fun onCues(cueGroup: CueGroup) = listener.onCues(cueGroup)

    override fun onMetadata(metadata: Metadata) = listener.onMetadata(metadata)

    // Deprecated, and still called by ExoPlayer beside their replacements.

    @Deprecated("Deprecated in Java")
    @Suppress("DEPRECATION")
    override fun onLoadingChanged(isLoading: Boolean) = listener.onLoadingChanged(isLoading)

    @Deprecated("Deprecated in Java")
    @Suppress("DEPRECATION")
    override fun onPlayerStateChanged(playWhenReady: Boolean, playbackState: Int) =
        listener.onPlayerStateChanged(playWhenReady, playbackState)

    @Deprecated("Deprecated in Java")
    @Suppress("DEPRECATION")
    override fun onPositionDiscontinuity(reason: Int) = listener.onPositionDiscontinuity(reason)

    @Deprecated("Deprecated in Java")
    @Suppress("DEPRECATION")
    override fun onCues(cues: List<Cue>) = listener.onCues(cues)
}
