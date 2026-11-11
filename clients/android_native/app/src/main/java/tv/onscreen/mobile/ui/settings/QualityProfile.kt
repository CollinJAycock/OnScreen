package tv.onscreen.mobile.ui.settings

import androidx.annotation.StringRes
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.QualityProfileRequest
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.i18n.UiText

/**
 * "Playback quality": the signed-in user's own quality profile, which the
 * server enforces on every device (the play decision transcodes anything over
 * it). Same options as the web app's PlaybackQuality.svelte. Pure, so it is
 * unit-tested on the JVM (QualityProfileTest).
 *
 * Values are strings as on the web — a number of pixels / kbps, a codec name —
 * and null means no limit (or "Automatic").
 */
object QualityProfile {

    enum class Field(@param:StringRes val title: Int) {
        HEIGHT(R.string.quality_max_resolution),
        VIDEO_BITRATE(R.string.quality_max_video_bitrate),
        AUDIO_BITRATE(R.string.quality_max_audio_bitrate),
        CODEC(R.string.quality_codec),
    }

    /** [label] is a resource for words ("Original") and plain text for
     *  values that read the same in every language ("1080p", "8 Mbps"). */
    data class Option(val value: String?, val label: UiText)

    private fun opt(value: String?, @StringRes label: Int) = Option(value, UiText.res(label))
    private fun opt(value: String?, label: String) = Option(value, UiText.raw(label))

    private val HEIGHTS = listOf(
        opt(null, R.string.quality_original), opt("2160", "4K"), opt("1080", "1080p"),
        opt("720", "720p"), opt("480", "480p"),
    )
    private val VIDEO_KBPS = listOf(
        opt(null, R.string.quality_no_limit), opt("40000", "40 Mbps"), opt("20000", "20 Mbps"),
        opt("12000", "12 Mbps"), opt("8000", "8 Mbps"), opt("4000", "4 Mbps"),
        opt("2000", "2 Mbps"), opt("1000", "1 Mbps"),
    )
    private val AUDIO_KBPS = listOf(
        opt(null, R.string.quality_original), opt("320", "320 kbps"), opt("256", "256 kbps"),
        opt("192", "192 kbps"), opt("128", "128 kbps"), opt("96", "96 kbps"),
    )
    private val CODECS = listOf(
        opt(null, R.string.quality_automatic), opt("h264", R.string.quality_codec_h264),
        opt("hevc", R.string.quality_codec_hevc),
    )

    /** The field's value in [prefs]: null = no limit / automatic (a 0 is no
     *  limit too, as on the server). */
    fun valueOf(prefs: UserPreferences, field: Field): String? = when (field) {
        Field.HEIGHT -> prefs.max_video_height.positive()
        Field.VIDEO_BITRATE -> prefs.max_video_bitrate_kbps.positive()
        Field.AUDIO_BITRATE -> prefs.max_audio_bitrate_kbps.positive()
        Field.CODEC -> prefs.preferred_video_codec?.takeIf { it == "h264" || it == "hevc" }
    }

    /**
     * The choices for [field]. A stored value no option covers (set through
     * the API or another client) is listed as its own option, so it shows as
     * itself rather than reading as "no limit" — and saving another field
     * keeps it.
     */
    fun options(field: Field, current: String?): List<Option> {
        val base = when (field) {
            Field.HEIGHT -> HEIGHTS
            Field.VIDEO_BITRATE -> VIDEO_KBPS
            Field.AUDIO_BITRATE -> AUDIO_KBPS
            Field.CODEC -> CODECS
        }
        if (current == null || base.any { it.value == current }) return base
        return base + Option(current, storedLabel(field, current))
    }

    /** What [value] reads as for [field]. */
    fun label(field: Field, value: String?): UiText =
        options(field, value).firstOrNull { it.value == value }?.label ?: storedLabel(field, value.orEmpty())

    private fun storedLabel(field: Field, value: String): UiText = UiText.raw(storedText(field, value))

    private fun storedText(field: Field, value: String): String = when (field) {

        Field.HEIGHT -> "${value}p"
        Field.VIDEO_BITRATE -> value.toIntOrNull()?.let { formatMbps(it) } ?: value
        Field.AUDIO_BITRATE -> "$value kbps"
        Field.CODEC -> value
    }

    private fun formatMbps(kbps: Int): String {
        val mbps = kbps / 1000.0
        val text = if (mbps % 1.0 == 0.0) mbps.toInt().toString() else mbps.toString()
        return "$text Mbps"
    }

    /**
     * The full body to save [field] = [value]: the other three limits and
     * forced_subtitles_only (set from the TV apps' subtitle settings) echoed
     * from [fresh], a read made just before the save — the endpoint replaces
     * the whole profile, and anything left out would be cleared.
     */
    fun request(fresh: UserPreferences, field: Field, value: String?): QualityProfileRequest {
        fun pick(f: Field): String? = if (f == field) value else valueOf(fresh, f)
        return QualityProfileRequest(
            max_video_height = pick(Field.HEIGHT)?.toIntOrNull(),
            max_video_bitrate_kbps = pick(Field.VIDEO_BITRATE)?.toIntOrNull(),
            max_audio_bitrate_kbps = pick(Field.AUDIO_BITRATE)?.toIntOrNull(),
            preferred_video_codec = pick(Field.CODEC),
            forced_subtitles_only = fresh.forced_subtitles_only,
        )
    }

    private fun Int?.positive(): String? = this?.takeIf { it > 0 }?.toString()
}
