package tv.onscreen.mobile.ui.player

import android.content.Context
import android.net.Uri
import android.os.Looper
import androidx.core.net.toUri
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.util.ExperimentalApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.Renderer
import androidx.media3.exoplayer.RenderersFactory
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.MergingMediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.exoplayer.source.SingleSampleMediaSource
import androidx.media3.exoplayer.text.TextOutput
import androidx.media3.exoplayer.text.TextRenderer
import androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy
import tv.onscreen.mobile.playback.StreamTokenVault

/**
 * The media source a screen-owned player plays [source] from, with [sideLoads]
 * (see [SubtitleTracks.sideLoads]) merged in next to it.
 *
 * Merged by hand rather than through MediaItem.setSubtitleConfigurations: only
 * DefaultMediaSourceFactory honours that field, and both sources here are
 * built directly — HlsMediaSource and ProgressiveMediaSource ignore it, so it
 * would silently load nothing.
 *
 * [containerMimeType]: a MIME hint for direct play when the container is
 * known — an offline file:// source has no Content-Type header to go by.
 */
internal fun playerMediaSource(
    context: Context,
    source: PlaybackSource,
    containerMimeType: String?,
    sideLoads: List<SubtitleTrack>,
): MediaSource {
    // DefaultDataSource dispatches by URI scheme — file:// to FileDataSource,
    // http(s):// to the wrapped DefaultHttpDataSource. (A bare HTTP factory
    // crashed with ClassCastException the first time an offline file:// url
    // reached it.) Wrapped in the vault resolver so direct-play urls — built
    // WITHOUT their `?token=` (see StreamTokenVault) — regain the credential
    // as the request leaves; file:// sources carry none and pass through.
    val dsFactory = StreamTokenVault.resolverFactory(
        DefaultDataSource.Factory(context, DefaultHttpDataSource.Factory()),
    )
    val media: MediaSource = when (source) {
        is PlaybackSource.DirectPlay -> {
            val mediaItem = MediaItem.Builder()
                .setUri(source.url.toUri())
                .apply { containerMimeType?.let { setMimeType(it) } }
                .build()
            ProgressiveMediaSource.Factory(dsFactory).createMediaSource(mediaItem)
        }
        is PlaybackSource.Hls ->
            HlsMediaSource.Factory(dsFactory).createMediaSource(hlsMediaItem(source.playlistUrl.toUri()))
    }
    if (sideLoads.isEmpty()) return media

    // The first request for an embedded stream makes the server demux the
    // whole source file to extract it (then it caches the result) — 25–60 s
    // for a 4K remux, long past the 8 s default read timeout; a load that
    // keeps timing out ends as an empty track. The TV client loads its
    // side-loads with these same timeouts.
    val subtitleFactory = StreamTokenVault.resolverFactory(
        DefaultDataSource.Factory(
            context,
            DefaultHttpDataSource.Factory()
                .setConnectTimeoutMs(SUBTITLE_CONNECT_TIMEOUT_MS)
                .setReadTimeoutMs(SUBTITLE_READ_TIMEOUT_MS),
        ),
    )
    // Cue times are content time; a resumed session's timeline starts at its
    // offset (see ShiftedVttDataSource). Direct play is already content time.
    val offsetMs = (source as? PlaybackSource.Hls)?.offsetMs ?: 0L
    val vttFactory = if (offsetMs > 0) {
        DataSource.Factory { ShiftedVttDataSource(subtitleFactory.createDataSource(), offsetMs) }
    } else {
        subtitleFactory
    }
    // SingleSampleMediaSource hands the text renderer the raw WebVTT file,
    // which needs render-time decoding (subtitleRenderersFactory). Kept over
    // the ProgressiveMediaSource that DefaultMediaSourceFactory builds for a
    // side-load now: a hand-built one makes the whole source wait on every VTT
    // before it prepares and fails it when one fails, while this prepares at
    // once, loads a track only when it is selected, and — with
    // setTreatLoadErrorsAsEndOfStream — turns a failed VTT into an empty track
    // instead of a playback error. Media3 has deprecated it and the decoding
    // switch together; they go together when it removes them.
    //
    // More tries than Media3's 3 before that empty track. A first request
    // that times out leaves the server extracting and caching the stream
    // anyway, so a later try gets it at once; a 504 SUBTITLE_PREPARING asks
    // for the retry outright. Media3's default gave up at the third failure,
    // after waits of 0 and 1 s; eight wait 0, 1, 2, 3, 4, 5 and 5 s between
    // tries (on top of each try's read timeout) — room for a 4K remux's
    // extraction to finish.
    @Suppress("DEPRECATION")
    val subtitles = sideLoads.mapNotNull { track ->
        val url = track.url ?: return@mapNotNull null
        val config = MediaItem.SubtitleConfiguration.Builder(url.toUri())
            .setMimeType(MimeTypes.TEXT_VTT)
            .setLanguage(track.language.ifBlank { null })
            .setLabel(SubtitleTracks.label(track))
            // Surfaces as the track's Format id (prefixed by the merge — see
            // SubtitleTracks.sideLoadIdOf), so the picker can select exactly
            // this track: same-language tracks (full, SDH, forced) were
            // unreachable when selecting by language.
            .setId(track.trackId)
            .setSelectionFlags(if (track.forced) C.SELECTION_FLAG_FORCED else 0)
            .build()
        SingleSampleMediaSource.Factory(vttFactory)
            .setLoadErrorHandlingPolicy(DefaultLoadErrorHandlingPolicy(SUBTITLE_LOAD_ATTEMPTS))
            .setTreatLoadErrorsAsEndOfStream(true)
            .createMediaSource(config, C.TIME_UNSET)
    }
    if (subtitles.isEmpty()) return media
    return MergingMediaSource(media, *subtitles.toTypedArray())
}

/**
 * The MediaItem a remux / transcode session's playlist plays from, set up so
 * a start at the stream's 0:00 really starts there.
 *
 * The server's session playlist has no #EXT-X-ENDLIST while ffmpeg is still
 * writing it (a remux's is an EVENT playlist, a transcode's has no type), so
 * Media3 plays it as live, and a live window's default position is near the
 * live edge: the window's end less a target offset, three target durations
 * by default. The player's one start seek (PlayerScreen) can't pin a start of
 * 0 against that. ExoPlayer wraps every source in a MaskingMediaSource, which
 * reads a prepare position equal to its placeholder window's default, 0, as
 * "no position asked for" and moves it to the real window's default once the
 * playlist loads. So a session that opens where playback is to start (a fresh
 * play from 0:00; a transcode resume, which opens exactly at the request; an
 * audio-track switch on one) started about three segments short of wherever
 * the server had got by the first playlist load: seconds late, or tens of
 * seconds into a transcode's initial burst. A start past 0 is a real position
 * and was never moved.
 *
 * A target offset longer than any playlist fixes it: HlsMediaSource clamps
 * the offset to the window's duration, which puts the default position at
 * the window's start, the first segment. That holds for the first playlist
 * load, the one the start is settled on. The source keeps the clamped offset
 * (that playlist's length) for later loads, so after that the default trails
 * the growing window's end by that much. Media3 still goes to it for Play on
 * an ended stream, Next on a live window and a prepare after an error, which
 * the screen's HlsSessionPlayer takes to the stream's start instead. Nothing
 * else acts on the offset here. ExoPlayer's live speed control, which would
 * steer towards it, only runs on a window with a known wall-clock start,
 * which HLS takes from EXT-X-PROGRAM-DATE-TIME, and the server writes none
 * (and HlsMediaSource pins the speed to 1 anyway with no speed range set here
 * and no EXT-X-SERVER-CONTROL hold-back). A finished or ABR (VOD) playlist
 * isn't live, so the setting goes unused there.
 */
internal fun hlsMediaItem(playlistUri: Uri): MediaItem =
    MediaItem.Builder()
        .setUri(playlistUri)
        .setLiveConfiguration(
            MediaItem.LiveConfiguration.Builder()
                .setTargetOffsetMs(HLS_WINDOW_START_TARGET_OFFSET_MS)
                .build(),
        )
        .build()

/** A year: longer than any session's playlist, so the clamp to the window
 *  always applies, and small enough for Media3's unchecked ms-to-µs multiply.
 *  Long.MAX_VALUE would wrap negative there, clamp to 0 and start the stream
 *  AT its live edge. */
internal const val HLS_WINDOW_START_TARGET_OFFSET_MS = 365L * 24 * 60 * 60 * 1000

private const val SUBTITLE_CONNECT_TIMEOUT_MS = 30_000
private const val SUBTITLE_READ_TIMEOUT_MS = 60_000

/** Loads of one side-loaded subtitle before it counts as empty (Media3's
 *  minimum-retry count, which SingleSampleMediaPeriod gives up at). */
private const val SUBTITLE_LOAD_ATTEMPTS = 8

/**
 * The renderers ExoPlayer.Builder builds by default, with render-time
 * ("legacy") subtitle decoding kept on in the text renderer — for the
 * side-loads in [playerMediaSource]. Those are SingleSampleMediaSources, which
 * pass the renderer the raw WebVTT file; since Media3 1.4 the renderer only
 * takes subtitles parsed during extraction unless this is on, and fails
 * playback as soon as such a track is selected. Tracks parsed during
 * extraction (the container's own) take the new path either way. Video player
 * only: the background audio service side-loads nothing. Same as the TV
 * client's.
 */
@Suppress("DEPRECATION")
@androidx.annotation.OptIn(ExperimentalApi::class)
internal fun subtitleRenderersFactory(context: Context): RenderersFactory =
    object : DefaultRenderersFactory(context) {
        override fun buildTextRenderers(
            context: Context,
            output: TextOutput,
            outputLooper: Looper,
            extensionRendererMode: Int,
            out: ArrayList<Renderer>,
        ) {
            super.buildTextRenderers(context, output, outputLooper, extensionRendererMode, out)
            out.filterIsInstance<TextRenderer>().forEach { it.experimentalSetLegacyDecodingEnabled(true) }
        }
    }

/** The viewer's explicit pick in the subtitle picker. */
sealed class SubtitleChoice {
    data object Off : SubtitleChoice()
    data class Track(val trackId: String) : SubtitleChoice()
}

/**
 * Reads and sets the text track of a player against the picker's rows
 * ([SubtitleTracks.rows]). Works on the player's ACTUAL tracks and selection,
 * never on the preferred-language parameter: Media3 normalises that ("eng"
 * comes back "en"), so comparing it with a stream's language showed no row
 * selected after a pick, and a language can't tell two same-language tracks
 * apart anyway.
 */
internal object SubtitleSelection {

    /** [trackId] of the row whose track is showing; null when subtitles are
     *  off (or the showing track is none of the rows). */
    fun selectedTrackId(player: Player, rows: List<SubtitleTrack>): String? {
        val groups = textGroups(player)
        val active = groups.firstOrNull { it.isSelected } ?: return null
        SubtitleTracks.sideLoadIdOf(active.mediaTrackGroup.getFormat(0).id)?.let { id ->
            return rows.firstOrNull { it.trackId == id }?.trackId
        }
        val container = groups.filter { SubtitleTracks.sideLoadIdOf(it.mediaTrackGroup.getFormat(0).id) == null }
        val ordinal = container.indexOfFirst { it.mediaTrackGroup == active.mediaTrackGroup }
        val embedded = rows.filter { !it.external }
        val paired = SubtitleTracks.alignContainer(
            embedded.map { it.language },
            container.map { it.mediaTrackGroup.getFormat(0).language },
        )
        val at = paired.indexOf(ordinal)
        return if (at >= 0) embedded[at].trackId else null
    }

    /**
     * Put [choice] into effect. False when the chosen track isn't among the
     * player's tracks (yet — they load after prepare; a rebuilt player has new
     * ones): the caller retries on the next tracks change. Does nothing when
     * the choice is already in effect, so calling it from onTracksChanged
     * settles instead of looping.
     */
    fun apply(player: Player, choice: SubtitleChoice, rows: List<SubtitleTrack>): Boolean {
        val params = player.trackSelectionParameters
        val textOff = params.disabledTrackTypes.contains(C.TRACK_TYPE_TEXT)
        when (choice) {
            SubtitleChoice.Off -> {
                if (textOff && params.overrides.values.none { it.type == C.TRACK_TYPE_TEXT }) return true
                player.trackSelectionParameters = params.buildUpon()
                    .clearOverridesOfType(C.TRACK_TYPE_TEXT)
                    .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
                    .build()
            }
            is SubtitleChoice.Track -> {
                val group = groupFor(player, choice.trackId, rows) ?: return false
                if (!textOff && params.overrides[group.mediaTrackGroup]?.trackIndices == listOf(0)) return true
                player.trackSelectionParameters = params.buildUpon()
                    .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
                    .setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, 0))
                    .build()
            }
        }
        return true
    }

    /** The player's text track for the row [trackId]: a side-load by its id,
     *  a container track by its place among the file's embedded streams. */
    private fun groupFor(player: Player, trackId: String, rows: List<SubtitleTrack>): Tracks.Group? {
        val groups = textGroups(player)
        groups.firstOrNull { SubtitleTracks.sideLoadIdOf(it.mediaTrackGroup.getFormat(0).id) == trackId }
            ?.let { return it }
        val embedded = rows.filter { !it.external }
        val at = embedded.indexOfFirst { it.trackId == trackId }
        if (at < 0) return null
        val container = groups.filter { SubtitleTracks.sideLoadIdOf(it.mediaTrackGroup.getFormat(0).id) == null }
        val paired = SubtitleTracks.alignContainer(
            embedded.map { it.language },
            container.map { it.mediaTrackGroup.getFormat(0).language },
        )
        return paired.getOrNull(at)?.let { container.getOrNull(it) }
    }

    private fun textGroups(player: Player): List<Tracks.Group> =
        player.currentTracks.groups.filter { it.type == C.TRACK_TYPE_TEXT }
}
