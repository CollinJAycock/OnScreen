package tv.onscreen.mobile.cast

/**
 * The capability profiles OnScreen sends ON BEHALF of a Chromecast running
 * Google's Default Media Receiver (DMR), as the `X-Client-Capabilities`
 * header of the playback-decision and transcode-start calls made for it —
 * in place of the phone's own profile (see AuthInterceptor, which leaves a
 * header the call already carries alone).
 *
 * The strings are a shared contract with the web sender and are documented
 * in docs/capability-profiles.md, section "Chromecast (Default Media
 * Receiver)". Change them there and in web/ together, never here alone.
 * `protocols` lists mp4 as well as hls: with hls alone no file container
 * matches, and even an MP4 the receiver plays as it is comes back
 * directStream.
 *
 * Pure Kotlin (no Android imports) so it runs in the JVM unit tests.
 */
object ChromecastCaps {

    /** Chromecast gen 1–3, Chromecast HD, and any device we can't place. */
    const val HD: String =
        "videoDecoder=h264,audioDecoder=aac:mp3,protocols=mp4:hls," +
            "maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0"

    /** Chromecast Ultra, Chromecast with Google TV, Google TV Streamer,
     *  Shield and other Android TV / Bravia cast targets. */
    const val UHD: String =
        "videoDecoder=h264:h265,audioDecoder=aac:mp3,protocols=mp4:hls," +
            "maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1"

    /** CastDevice model names that get [UHD]. Case-insensitive. */
    private val UHD_MODEL = Regex("ultra|google tv|streamer|shield|android tv|bravia", RegexOption.IGNORE_CASE)

    enum class Profile(
        val header: String,
        /** The tallest picture the profile decodes: the cap on the
         *  transcode height (CastHls.transcodeHeight). */
        val maxHeight: Int,
        /** The explicit `supports_hevc` of the transcode start. The body's
         *  boolean outranks the header on the server (it is tri-state there),
         *  so it must say the same as [header]. */
        val hevc: Boolean,
    ) {
        HD(ChromecastCaps.HD, 1080, hevc = false),
        UHD(ChromecastCaps.UHD, 2160, hevc = true),
    }

    /** The profile for a CastDevice's model name (null/blank: [Profile.HD]). */
    fun forModelName(modelName: String?): Profile =
        if (!modelName.isNullOrBlank() && UHD_MODEL.containsMatchIn(modelName)) Profile.UHD else Profile.HD

    /** What to try after a load with [profile] failed on the receiver: a 4K
     *  load is retried once as HD; an HD failure is final (null). */
    fun fallbackAfterFailure(profile: Profile): Profile? = if (profile == Profile.UHD) Profile.HD else null
}
