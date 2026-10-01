package tv.onscreen.android.playback

import android.app.ActivityManager
import android.content.Context
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.ExoPlayer

/**
 * How much media the players buffer, and how much they wait for before
 * playing.
 *
 * Media3 1.9 lowered DefaultLoadControl's start thresholds: playback now
 * starts with 1 s buffered (was 2.5 s) and resumes after a rebuffer with 2 s
 * (was 5 s). 1.3.1, which the app's players were built and tested on, had the
 * old ones. A server remux or transcode is an HLS playlist the server writes
 * as it goes, so after a stall the buffer only refills as fast as the server
 * produces; the lower bar restarted playback on a buffer too thin to keep it
 * going. On a Fire TV Stick the player flapped BUFFERING / READY every ~0.7 s
 * over a frozen picture for 5 s with ~4 s buffered. The streaming thresholds
 * go back to 1.3.1's. The min / max buffer are Media3's defaults (50 s, as in
 * 1.3.1), set explicitly so a later Media3 can't move them unnoticed. Local
 * playback (file://, content://) keeps Media3's own values: the app streams
 * everything from the server.
 *
 * Low-RAM devices (1 GB and similar — ActivityManager.isLowRamDevice) keep
 * their tighter profile: ~halved buffer durations and a lower target byte
 * cap. Without it the 50 s buffer pulls 30-60 MB of video per session, which
 * on a 1 GB box leaves the rest of the app fighting the OOM killer for what's
 * left (Google's TV-ME quality guideline for memory on low-RAM devices).
 * Pure, JVM-tested in BufferProfileTest.
 */
@UnstableApi
object BufferProfile {

    /** Media buffered before playback starts, as Media3 1.3.1 had it. */
    const val BUFFER_FOR_PLAYBACK_MS = 2_500

    /** Media buffered before playback resumes after a rebuffer, as 1.3.1
     *  had it. */
    const val BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS = 5_000

    fun loadControl(lowRamDevice: Boolean): DefaultLoadControl =
        if (lowRamDevice) {
            DefaultLoadControl.Builder()
                .setBufferDurationsMs(
                    /* minBufferMs */ 15_000,
                    /* maxBufferMs */ 30_000,
                    /* bufferForPlaybackMs */ 1_500,
                    /* bufferForPlaybackAfterRebufferMs */ 3_000,
                )
                .setTargetBufferBytes(16 * 1024 * 1024) // 16 MB cap (default ~64 MB)
                .setPrioritizeTimeOverSizeThresholds(true)
                .build()
        } else {
            DefaultLoadControl.Builder()
                .setBufferDurationsMsForStreaming(
                    DefaultLoadControl.DEFAULT_MIN_BUFFER_MS,
                    DefaultLoadControl.DEFAULT_MAX_BUFFER_MS,
                    BUFFER_FOR_PLAYBACK_MS,
                    BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS,
                )
                .build()
        }
}

/** The [BufferProfile] for this device. */
@UnstableApi
fun ExoPlayer.Builder.withBufferProfile(context: Context): ExoPlayer.Builder {
    val am = context.getSystemService(Context.ACTIVITY_SERVICE) as? ActivityManager
    return setLoadControl(BufferProfile.loadControl(lowRamDevice = am?.isLowRamDevice == true))
}
