package tv.onscreen.mobile.playback

import tv.onscreen.mobile.data.model.ItemFile
import kotlin.math.pow

/**
 * ReplayGain math for the phone's music player — a line-for-line port of the
 * web client's `web/src/lib/replaygain.ts` (and its test table, in
 * ReplayGainTest), so one setting means the same thing on every client.
 *
 * Pure: pick the tag pair for the mode, fold in the preamp, and cap against
 * the peak so the gain never pushes a sample past full scale. The audio stage
 * that applies it is [ReplayGainAudioProcessor].
 */
enum class ReplayGainMode(val wire: String) {
    OFF("off"),
    TRACK("track"),
    ALBUM("album"),
    ;

    companion object {
        /** Unknown / missing stored value → [OFF], the default. */
        fun fromWire(value: String?): ReplayGainMode =
            entries.firstOrNull { it.wire == value } ?: OFF
    }
}

/** ReplayGain tags for one file. Gains are in dB (REPLAYGAIN_*_GAIN), peaks
 *  are linear sample peaks (REPLAYGAIN_*_PEAK, 1.0 = full scale). Every field
 *  is optional: a file can carry track tags only, album tags only, or nothing
 *  at all — an all-null instance means "looked up, file has no tags", which
 *  plays at unity. */
data class ReplayGainInfo(
    val trackGain: Double? = null,
    val trackPeak: Double? = null,
    val albumGain: Double? = null,
    val albumPeak: Double? = null,
) {
    companion object {
        val NONE = ReplayGainInfo()
    }
}

/** The tag pair a mode resolved to. [peak] null = unknown. */
data class ReplayGainSelection(val gainDb: Double, val peak: Double? = null)

object ReplayGain {

    /** Preamp bounds of the gain math — the same ±15 dB clamp the web and
     *  desktop engines apply. The phone's settings UI offers a narrower
     *  [UI_PREAMP_MIN_DB]..[UI_PREAMP_MAX_DB] range inside it. */
    const val PREAMP_MIN_DB = -15.0
    const val PREAMP_MAX_DB = 15.0

    /** Settings slider range + step (Settings → Playback). */
    const val UI_PREAMP_MIN_DB = -6.0
    const val UI_PREAMP_MAX_DB = 6.0
    const val UI_PREAMP_STEP_DB = 0.5

    fun clampPreamp(db: Double): Double {
        if (!db.isFinite()) return 0.0
        return db.coerceIn(PREAMP_MIN_DB, PREAMP_MAX_DB)
    }

    /** Snap a stored / slider preamp onto the settings grid: clamp to the UI
     *  range and round to the nearest 0.5 dB. Non-finite → 0. */
    fun snapUiPreamp(db: Double): Double {
        if (!db.isFinite()) return 0.0
        val clamped = db.coerceIn(UI_PREAMP_MIN_DB, UI_PREAMP_MAX_DB)
        return Math.round(clamped / UI_PREAMP_STEP_DB) * UI_PREAMP_STEP_DB
    }

    private fun Double?.finiteOrNull(): Double? = this?.takeIf { it.isFinite() }

    /** Lift the replaygain_* fields off an [ItemFile] (the shape /items/{id}
     *  returns). Always non-null — [ReplayGainInfo.NONE] means "file has no
     *  tags". */
    fun fromFile(file: ItemFile?): ReplayGainInfo {
        if (file == null) return ReplayGainInfo.NONE
        return ReplayGainInfo(
            trackGain = file.replaygain_track_gain.finiteOrNull(),
            trackPeak = file.replaygain_track_peak.finiteOrNull(),
            albumGain = file.replaygain_album_gain.finiteOrNull(),
            albumPeak = file.replaygain_album_peak.finiteOrNull(),
        )
    }

    /**
     * The tag pair a mode resolves to, or null when the mode is off or the
     * file carries no usable gain.
     *
     *  - track: track gain + track peak.
     *  - album: album gain + album peak, falling back to the track pair when
     *    the file has no album gain (singles, partially-tagged rips). When the
     *    album gain is present but the album peak isn't, the track peak is the
     *    next-best clip guard (it's this file's real peak; the album peak is
     *    only ever >= it).
     */
    fun select(info: ReplayGainInfo?, mode: ReplayGainMode): ReplayGainSelection? {
        if (info == null || mode == ReplayGainMode.OFF) return null
        val tg = info.trackGain.finiteOrNull()
        val tp = info.trackPeak.finiteOrNull()
        val ag = info.albumGain.finiteOrNull()
        val ap = info.albumPeak.finiteOrNull()
        if (mode == ReplayGainMode.ALBUM && ag != null) {
            return ReplayGainSelection(ag, ap ?: tp)
        }
        if (tg != null) return ReplayGainSelection(tg, tp)
        return null
    }

    /**
     * Linear gain multiplier for a file under a mode + preamp.
     *
     *   gain = 10^((tagDb + preampDb) / 20), then capped so peak * gain <= 1.
     *
     *  - mode off → 1 (unity; samples pass untouched).
     *  - no usable tag → 1. Common practice (foobar2000, mpv): untagged files
     *    are left alone rather than getting the preamp on its own — otherwise
     *    a +6 dB preamp would make every untagged file 6 dB louder and clip.
     *  - peak unknown → treated as 1.0 (full scale), same default as mpv, so a
     *    tag + preamp can attenuate freely but never boost past unity: without
     *    a known peak there's no way to prove a boost is clip-free.
     */
    fun linear(info: ReplayGainInfo?, mode: ReplayGainMode, preampDb: Double): Double {
        val sel = select(info, mode) ?: return 1.0
        val db = sel.gainDb + clampPreamp(preampDb)
        var linear = 10.0.pow(db / 20.0)
        val peak = sel.peak?.takeIf { it > 0 } ?: 1.0
        if (linear * peak > 1) linear = 1 / peak
        return if (linear.isFinite() && linear >= 0) linear else 1.0
    }
}
