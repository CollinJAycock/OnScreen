package tv.onscreen.mobile.ui.player

import androidx.media3.common.C
import androidx.media3.common.Player
import tv.onscreen.mobile.data.model.AudioStream

/**
 * The audio picker's rows ([PlayerUiState.audioStreams], in the file's audio
 * order) against what is actually playing.
 *
 * A row is addressed by its POSITION in that list, and that position is also
 * what the server's `audio_stream_index` means: ffmpeg maps it as
 * `-map 0:a:N`, the Nth AUDIO stream. [AudioStream.index] is something else —
 * the ABSOLUTE ffprobe stream index, one or more ahead of the position since
 * the video is stream 0. Sending it switched to the track below the one
 * picked, and for the last track named a stream that doesn't exist: ffmpeg
 * wrote no playlist and the player gave up with a playback error.
 */
internal object AudioSelection {

    /**
     * The row playing now, or -1 when that can't be told.
     *
     * A remux / transcode ([hls]) carries only the stream its session was
     * started with, [sessionRow] — the server's default, the file's first
     * audio stream, until the viewer picks another. Its player sees that one
     * track, so the player's own selection says nothing about which row it is.
     * While a switch starts its replacement session, the caller passes the
     * row it is switching to (PlayerUiState.targetAudioRow): the latest pick.
     *
     * Direct play: the player's selected audio track, by its place among the
     * container's audio tracks — the order the server lists them in too, the
     * same pairing a pick uses. Should the counts differ, the track's language
     * stands in, when exactly one row has it.
     */
    fun selectedRow(player: Player, streams: List<AudioStream>, sessionRow: Int?, hls: Boolean): Int {
        if (hls) return sessionRow?.takeIf { it in streams.indices } ?: -1
        val groups = player.currentTracks.groups.filter { it.type == C.TRACK_TYPE_AUDIO }
        val at = groups.indexOfFirst { it.isSelected }
        if (at < 0) return -1
        if (groups.size == streams.size) return at
        val language = groups[at].mediaTrackGroup.getFormat(0).language
        val matches = streams.indices.filter { SubtitleTracks.languagesMatch(streams[it].language, language) }
        return matches.singleOrNull() ?: -1
    }
}
