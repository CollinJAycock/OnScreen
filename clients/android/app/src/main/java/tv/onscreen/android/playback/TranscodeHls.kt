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

    /** Retry HTTP / IO failures up to 6 times with the default exponential
     *  backoff (1 s, 2 s, 4 s, 8 s, 8 s, 8 s ≈ 30 s total). Catches the case
     *  where the manifest/playlist isn't ready on the first poll because the
     *  transcoder is still warming up. */
    fun errorPolicy(): DefaultLoadErrorHandlingPolicy = DefaultLoadErrorHandlingPolicy(6)

    /** An HLS source for a session playlist with no side-loaded tracks (the
     *  audio case). [playlistUrl] is clean; the vault's resolver puts the
     *  playlist's `?token=` back on the wire, and segment URIs carry their
     *  own server-embedded token. */
    fun mediaSource(playlistUrl: String): HlsMediaSource =
        HlsMediaSource.Factory(StreamTokenVault.resolverFactory(httpFactory()))
            .setLoadErrorHandlingPolicy(errorPolicy())
            .createMediaSource(MediaItem.fromUri(playlistUrl.toUri()))
}
