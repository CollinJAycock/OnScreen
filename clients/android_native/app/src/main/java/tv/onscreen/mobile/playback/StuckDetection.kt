package tv.onscreen.mobile.playback

import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer

/**
 * Turns off the stuck-player detectors Media3 1.9 added. They are on by
 * default, and 1.3.1, which the app's players were built and tested on, had
 * none. Each one ends playback with ERROR_CODE_TIMEOUT:
 *  - playing more than 60 s past the declared duration: a VBR MP3 with no
 *    Xing header, or a recording with a clock jump, under-reports its length,
 *    so the rest of the file would be cut off;
 *  - playback suppressed for 10 minutes: a phone call longer than that (a
 *    transient audio-focus loss) would end the book or album instead of
 *    letting it resume when the call ends;
 *  - playing for 10 s with no progress, and buffering for 10 minutes.
 * Integer.MAX_VALUE is the value Media3 itself uses for "off". Same helper as
 * the TV app's.
 */
@UnstableApi
fun ExoPlayer.Builder.withoutStuckDetection(): ExoPlayer.Builder =
    setStuckPlayingDetectionTimeoutMs(Int.MAX_VALUE)
        .setStuckPlayingNotEndingTimeoutMs(Int.MAX_VALUE)
        .setStuckSuppressedDetectionTimeoutMs(Int.MAX_VALUE)
        .setStuckBufferingDetectionTimeoutMs(Int.MAX_VALUE)
