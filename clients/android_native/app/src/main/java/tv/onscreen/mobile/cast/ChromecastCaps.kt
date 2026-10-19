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

    /** Model (or, as a fallback, friendly) names that get [UHD]: Chromecast
     *  Ultra, Chromecast with Google TV, Google TV Streamer, SHIELD, Sony
     *  BRAVIA and other Android / Google TV receivers. Case-insensitive.
     *  The same pattern as web/src/lib/cast.ts. */
    private val UHD_NAME = Regex("ultra|google tv|streamer|shield|android tv|bravia", RegexOption.IGNORE_CASE)

    /** Exact model names (lowercased) that are known 4K video receivers
     *  even though [UHD_NAME] misses them (mDNS `md=` values). */
    private val UHD_MODELS = setOf(
        "mitv-mssp2", // Xiaomi Mi Box S
    )

    /** Exact model names (lowercased) that never get [UHD]: 1080p
     *  Chromecasts, smart displays, and audio-only speakers, soundbars and
     *  groups (a soundbar named "… Ultra …" must not pass as a Chromecast
     *  Ultra). Plain "Chromecast" is NOT here: gen 1–3 and the 4K Chromecast
     *  with Google TV both report it, so it falls through to the friendly
     *  name and then to HD. */
    private val HD_MODELS = setOf(
        "chromecast hd",
        "chromecast audio",
        "eureka dongle",
        "google home",
        "google home mini",
        "google nest mini",
        "nest audio",
        "nest wifi point",
        "google nest hub",
        "google nest hub max",
        "google cast group",
        "lenovo smart display 7",
        "lenovo smart display 10",
        "bose smart ultra soundbar",
    )

    /** The user's choice in Settings → Playback → Chromecast quality. */
    enum class Quality(val wire: String) {
        AUTO("auto"),
        UHD("4k"),
        HD("hd"),
        ;

        companion object {
            fun fromWire(v: String?): Quality = entries.firstOrNull { it.wire == v } ?: AUTO
        }
    }

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

    /** The profile for a CastDevice's model name alone (null/blank: [Profile.HD]). */
    fun forModelName(modelName: String?): Profile = forDevice(modelName, null, null)

    /**
     * The profile for a Cast device, from what [com.google.android.gms.cast.CastDevice]
     * reports, in this order:
     *
     *  1. the user's [quality] override, when it isn't AUTO (a 4K receiver
     *     no rule recognises, such as a TV with Google TV built in that
     *     reports its panel model, can be set to 4K by hand);
     *  2. [videoOut] = `hasCapability(CAPABILITY_VIDEO_OUT)`: false means an
     *     audio-only receiver, which never gets the 4K profile;
     *  3. the model name: a known HD/audio model is HD, a known 4K model or
     *     one matching [UHD_NAME] is 4K;
     *  4. the friendly name against [UHD_NAME], for devices whose model
     *     name says nothing (plain "Chromecast");
     *  5. otherwise HD, which every Chromecast plays.
     *
     * The Cast SDK reports no resolution or codec support, so nothing
     * beyond these is available to the sender.
     */
    fun forDevice(
        modelName: String?,
        friendlyName: String?,
        videoOut: Boolean?,
        quality: Quality = Quality.AUTO,
    ): Profile {
        when (quality) {
            Quality.UHD -> return Profile.UHD
            Quality.HD -> return Profile.HD
            Quality.AUTO -> Unit
        }
        if (videoOut == false) return Profile.HD
        val model = modelName?.trim()?.lowercase().orEmpty()
        if (model.isNotEmpty()) {
            if (model in HD_MODELS) return Profile.HD
            if (model in UHD_MODELS || UHD_NAME.containsMatchIn(model)) return Profile.UHD
        }
        if (!friendlyName.isNullOrBlank() && UHD_NAME.containsMatchIn(friendlyName)) return Profile.UHD
        return Profile.HD
    }

    /** What to try after a load with [profile] failed on the receiver: a 4K
     *  load is retried once as HD; an HD failure is final (null). */
    fun fallbackAfterFailure(profile: Profile): Profile? = if (profile == Profile.UHD) Profile.HD else null
}
