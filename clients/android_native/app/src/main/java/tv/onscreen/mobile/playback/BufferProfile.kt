package tv.onscreen.mobile.playback

import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.ExoPlayer

/**
 * How much media the screen-owned player waits for before it plays.
 *
 * Media3 1.9 lowered DefaultLoadControl's start thresholds for a streamed
 * item: playback now starts with 1 s buffered (was 2.5 s) and resumes after a
 * rebuffer with 2 s (was 5 s). 1.3.1, which the app's players were built and
 * tested on, had the old ones. A server remux or transcode is an HLS playlist
 * the server writes as it goes, so after a stall the buffer only refills as
 * fast as the server produces; the lower bar restarts playback on a buffer
 * too thin to keep it going. On a Fire TV Stick the TV app's player flapped
 * BUFFERING / READY every ~0.7 s over a frozen picture with ~4 s buffered.
 * The streaming thresholds go back to 1.3.1's. The min / max buffer are
 * Media3's defaults (50 s, as in 1.3.1), set explicitly so a later Media3
 * can't move them unnoticed. A download (file://) keeps Media3's own local
 * values: it never waits on the server.
 *
 * The TV app's streaming thresholds; its low-RAM profile isn't carried over.
 * The background audio service keeps Media3's defaults: it plays files
 * directly, never a growing server playlist. JVM-tested in BufferProfileTest.
 */
@UnstableApi
object BufferProfile {

    /** Media buffered before playback starts, as Media3 1.3.1 had it. */
    const val BUFFER_FOR_PLAYBACK_MS = 2_500

    /** Media buffered before playback resumes after a rebuffer, as 1.3.1
     *  had it. */
    const val BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS = 5_000

    fun loadControl(): DefaultLoadControl =
        DefaultLoadControl.Builder()
            .setBufferDurationsMsForStreaming(
                DefaultLoadControl.DEFAULT_MIN_BUFFER_MS,
                DefaultLoadControl.DEFAULT_MAX_BUFFER_MS,
                BUFFER_FOR_PLAYBACK_MS,
                BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS,
            )
            .build()
}

/** The [BufferProfile] for a screen-owned player. */
@UnstableApi
fun ExoPlayer.Builder.withBufferProfile(): ExoPlayer.Builder =
    setLoadControl(BufferProfile.loadControl())
