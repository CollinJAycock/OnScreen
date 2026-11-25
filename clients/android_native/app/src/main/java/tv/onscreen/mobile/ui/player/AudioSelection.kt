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
     * started with, [sessionRow] — the preferred language's ([preferredRow]),
     * else the server's default, the file's first audio stream — until the
     * viewer picks another. Its player sees that one
     * track, so the player's own selection says nothing about which row it is.
     * While a switch starts its replacement session, the caller passes the
     * row it is switching to (PlayerUiState.targetAudioRow): the latest pick.
     *
     * Direct play: the player's selected audio track, by its place among the
     * container's audio tracks — the order the server lists them in too, the
     * same pairing a pick uses. Should the counts differ, the track's language
     * stands in, when exactly one row has it.
     */
    /**
     * The row the preferred language names: the first in [lang] — what a
     * remux / transcode starts with, since it carries one track — or null
     * (no preference, or no track in it) for the server's default, the
     * first. Among same-language tracks (5.1 + stereo, commentary) the
     * file's order wins. The TV app's pickPreferredAudio.
     */
    fun preferredRow(streams: List<AudioStream>, lang: String?): Int? {
        if (lang.isNullOrBlank()) return null
        return streams.indexOfFirst { SubtitleTracks.languagesMatch(it.language, lang) }.takeIf { it >= 0 }
    }

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

/**
 * What says which audio track plays, for the subtitle preference (whose
 * "foreign" mode follows its language): the file's audio [streams], a
 * remux / transcode's [sessionRow], the saved [preferredLang], and an
 * optimized version's [optimizedLanguage] (it carries the source's first
 * track alone, and [streams] is empty then).
 */
internal data class PlayingAudio(
    val streams: List<AudioStream>,
    val sessionRow: Int?,
    val preferredLang: String?,
    val optimizedLanguage: String?,
) {
    /** The playing track's language: an optimized version's one track; a
     *  session's row; on direct play the player's selected track, else —
     *  its tracks not loaded yet — the preferred language's row (the
     *  player's own choice from that preference), else the first. */
    fun language(player: Player, hls: Boolean, optimized: Boolean): String? {
        if (optimized) return optimizedLanguage
        val row = AudioSelection.selectedRow(player, streams, sessionRow, hls).takeIf { it >= 0 }
            ?: AudioSelection.preferredRow(streams, preferredLang)
            ?: 0
        return streams.getOrNull(row)?.language
    }
}
