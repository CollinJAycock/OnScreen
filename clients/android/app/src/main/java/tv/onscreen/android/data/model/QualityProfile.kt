package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass
import com.squareup.moshi.Moshi
import tv.onscreen.android.R
import tv.onscreen.android.i18n.UiText
import java.util.Locale

/**
 * Body of PUT /api/v1/users/me/quality-profile (server v2.6). The endpoint
 * REPLACES the whole profile: a null (or missing) limit is cleared, so every
 * save sends all five, the ones not being changed echoed from a fresh GET of
 * the preferences — forced_subtitles_only included, which this screen
 * doesn't edit and must never reset. Serialized with nulls
 * ([QualityProfile.toJson]).
 */
@JsonClass(generateAdapter = true)
data class QualityProfileBody(
    val max_video_height: Int?,
    val max_video_bitrate_kbps: Int?,
    val max_audio_bitrate_kbps: Int?,
    val preferred_video_codec: String?,
    val forced_subtitles_only: Boolean,
)

/**
 * The "Playback quality" setting: the user's server-side quality profile,
 * which the play decision and transcode start apply on every device. Options
 * match the web app's (web/src/lib/components/PlaybackQuality.svelte); a value
 * set elsewhere that no option lists keeps an option of its own instead of
 * reading as "no limit" ([withStored]).
 */
object QualityProfile {

    /** One choice: [value] null = no limit / automatic. The words are string
     *  resources; resolutions, bitrates and codec names are shown as they
     *  are. */
    data class Option<T>(val value: T?, val label: UiText)

    private fun <T> option(value: T?, text: String) = Option(value, UiText.Raw(text))

    val HEIGHTS: List<Option<Int>> = listOf(
        Option(null, UiText.Res(R.string.quality_original)),
        option(2160, "4K"),
        option(1080, "1080p"),
        option(720, "720p"),
        option(480, "480p"),
    )

    val VIDEO_KBPS: List<Option<Int>> = listOf(
        Option(null, UiText.Res(R.string.no_limit)),
        option(40_000, "40 Mbps"),
        option(20_000, "20 Mbps"),
        option(12_000, "12 Mbps"),
        option(8_000, "8 Mbps"),
        option(4_000, "4 Mbps"),
        option(2_000, "2 Mbps"),
        option(1_000, "1 Mbps"),
    )

    val AUDIO_KBPS: List<Option<Int>> = listOf(
        Option(null, UiText.Res(R.string.quality_original)),
        option(320, "320 kbps"),
        option(256, "256 kbps"),
        option(192, "192 kbps"),
        option(128, "128 kbps"),
        option(96, "96 kbps"),
    )

    val CODECS: List<Option<String>> = listOf(
        Option(null, UiText.Res(R.string.quality_automatic)),
        Option("h264", UiText.Res(R.string.quality_codec_h264)),
        Option("hevc", UiText.Res(R.string.quality_codec_hevc)),
    )

    fun heightLabel(v: Int): String = "${v}p"

    fun videoKbpsLabel(v: Int): String {
        val mbps = v / 1000.0
        val text = if (mbps == Math.floor(mbps)) mbps.toLong().toString() else String.format(Locale.US, "%.1f", mbps)
        return "$text Mbps"
    }

    fun audioKbpsLabel(v: Int): String = "$v kbps"

    /** [options] plus one for [stored] when it is set and none lists it. */
    fun <T> withStored(options: List<Option<T>>, stored: T?, label: (T) -> String): List<Option<T>> =
        if (stored == null || options.any { it.value == stored }) options else options + option(stored, label(stored))

    /** The label of [value] among [options] (after [withStored]). */
    fun <T> labelOf(options: List<Option<T>>, value: T?): UiText =
        options.firstOrNull { it.value == value }?.label ?: options.first().label

    /**
     * The profile as [prefs] (a fresh GET) holds it — the body a save starts
     * from. A zero limit is no limit, and a codec the server doesn't take
     * reads as automatic.
     */
    fun fromPreferences(prefs: UserPreferences): QualityProfileBody = QualityProfileBody(
        max_video_height = prefs.max_video_height?.takeIf { it > 0 },
        max_video_bitrate_kbps = prefs.max_video_bitrate_kbps?.takeIf { it > 0 },
        max_audio_bitrate_kbps = prefs.max_audio_bitrate_kbps?.takeIf { it > 0 },
        preferred_video_codec = prefs.preferred_video_codec?.lowercase()?.takeIf { it == "h264" || it == "hevc" },
        forced_subtitles_only = prefs.forced_subtitles_only,
    )

    private val moshi: Moshi by lazy { Moshi.Builder().build() }

    /** [body] as JSON with every field present, nulls included. */
    fun toJson(body: QualityProfileBody): String =
        moshi.adapter(QualityProfileBody::class.java).serializeNulls().toJson(body)
}
