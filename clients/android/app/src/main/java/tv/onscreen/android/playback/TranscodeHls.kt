package tv.onscreen.android.playback

import androidx.core.net.toUri
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy

/**
 * How a player loads a server HLS session ([StreamSession]). Shared by
 * PlaybackFragment and the background service's chain to the next track, so
 * a transcoded track plays the same way on both.
 */
@UnstableApi
object TranscodeHls {

    /** Default DefaultHttpDataSource timeouts are 8 s connect / 8 s read. The
     *  first segment waits for the ffmpeg transcoder to spin up server-side,
     *  which on remote / Cloudflare-Tunnel deployments routinely takes
     *  10–20 s. Bump both, allow cross-protocol redirects (Tunnel's
     *  HTTP→HTTPS handling), and send an identifiable UA so requests look
     *  like a real client to any WAF rules in front of the server. */
    fun httpFactory(): DefaultHttpDataSource.Factory =
        DefaultHttpDataSource.Factory()
            .setConnectTimeoutMs(30_000)
            .setReadTimeoutMs(60_000)
            .setAllowCrossProtocolRedirects(true)
            .setUserAgent("OnScreen-Android/1.0 (ExoPlayer)")
            .setDefaultRequestProperties(mapOf())

    /** Retry HTTP / IO failures 6 times before the error reaches the player.
     *  DefaultLoadErrorHandlingPolicy's backoff is linear, not exponential:
     *  it waits min((errorCount - 1) × 1 s, 5 s) after each failure, so 0 s,
     *  1 s, 2 s, 3 s, 4 s, 5 s (≈ 15 s of waiting, plus the tries themselves),
     *  and the 7th failure in a row is fatal. Catches the case where the
     *  manifest/playlist isn't ready on the first poll because the transcoder
     *  is still warming up. */
    fun errorPolicy(): DefaultLoadErrorHandlingPolicy = DefaultLoadErrorHandlingPolicy(6)

    /**
     * The MediaItem for a session playlist: one that a start at 0 plays from
     * the playlist's head.
     *
     * Until its ENDLIST a session playlist is live to ExoPlayer, and a live
     * window's default start is near its end: the playlist's length less the
     * live target offset, three target durations when the item names none
     * (HlsMediaSource.getLiveWindowDefaultStartPositionUs). A start at 0
     * doesn't override that. The player masks the source until its first
     * playlist arrives, and MaskingMediaSource takes a start equal to the
     * placeholder's default position (0) for "the default position", so
     * seekTo(0) and setMediaSource(source, 0) both began several seconds in
     * whenever the server was a few segments ahead (a remux or an audio
     * encode, many times faster than real time, by the first playlist load).
     *
     * A target offset longer than any playlist moves that default to the
     * head: HlsMediaSource clamps the offset to the playlist's length, which
     * puts the default start in the first segment. It keeps the clamped
     * offset, though, so from the next playlist on the default trails the
     * live edge by the first one's length: anywhere in the stream, minutes
     * back for a remux far ahead by then. A seek to the default position
     * lands there, so the players the app hands out take one for the start
     * of the item or session (ContentTimeForwardingPlayer, SessionPlayer:
     * Play once an item has ended), and SessionPlayer offers no Next, which
     * with no next item goes to a live stream's default. One is still the
     * player's own: a re-prepare after an error (Play on the transport bar)
     * starts there when the player stood on the last playlist's default (at
     * 0, say, failing on the first segment before the playlist grew one),
     * which MaskingMediaSource takes for "the default position" too. Nothing
     * else acts on the offset: the live speed control runs only for a window
     * with a wall clock (EXT-X-PROGRAM-DATE-TIME), which the server's
     * playlists never carry
     * (ExoPlayerImplInternal.shouldUseLivePlaybackSpeedControl), and a
     * finished (VOD) playlist ignores it. A start past 0 is kept as asked.
     */
    fun mediaItem(playlistUrl: String): MediaItem =
        MediaItem.Builder()
            .setUri(playlistUrl.toUri())
            .setLiveConfiguration(
                MediaItem.LiveConfiguration.Builder().setTargetOffsetMs(HEAD_TARGET_OFFSET_MS).build(),
            )
            .build()

    /** The live target offset of [mediaItem]: longer than any playlist, and
     *  the most Media3's Util.msToUs takes without overflowing (it multiplies
     *  unchecked). */
    const val HEAD_TARGET_OFFSET_MS = Long.MAX_VALUE / 1_000

    /** An HLS source for a session playlist with no side-loaded tracks (the
     *  audio case). [playlistUrl] is clean; the vault's resolver puts the
     *  playlist's `?token=` back on the wire, and segment URIs carry their
     *  own server-embedded token. */
    fun mediaSource(playlistUrl: String): HlsMediaSource =
        HlsMediaSource.Factory(StreamTokenVault.resolverFactory(httpFactory()))
            .setLoadErrorHandlingPolicy(errorPolicy())
            .createMediaSource(mediaItem(playlistUrl))
}
