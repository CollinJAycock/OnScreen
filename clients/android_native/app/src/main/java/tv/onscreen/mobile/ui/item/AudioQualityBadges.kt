package tv.onscreen.mobile.ui.item

import tv.onscreen.mobile.data.model.ItemFile

/**
 * Pure helpers for the audio-quality badge row on the item detail
 * page. Mirrors the v2.1 audiophile-metadata claim from the matrix —
 * bit-depth, sample-rate, lossless flag, and ReplayGain availability
 * are surfaced as small chips so the user can see at a glance
 * whether they're getting the bit-perfect path.
 *
 * Pure module — no Compose / Android imports — so the badge
 * computation is JVM-testable in isolation.
 */
object AudioQualityBadges {

    /** Build the visible badge list for a file. Order matters — the
     *  UI renders left-to-right and we want the marquee badge first
     *  (Hi-Res > Lossless > codec quality), then the detail chip
     *  (`24/96`), then ReplayGain availability.
     *
     *  Empty list = no badges to render (e.g. video file, MP3 with
     *  no extended metadata). The UI hides the row entirely. */
    fun badges(file: ItemFile?): List<String> {
        if (file == null) return emptyList()
        // Audio metadata is only meaningful for audio-bearing items.
        // A movie file with audio_codec=aac doesn't deserve a "Lossless"
        // chip — gate on no-video.
        if (!file.video_codec.isNullOrEmpty()) return emptyList()

        val out = mutableListOf<String>()

        // Hi-Res = lossless AND (>16 bit OR > 48 kHz). Marketing
        // alignment with the standard Hi-Res Audio definition. Lossless
        // alone (CD-quality 16/44.1) gets a "Lossless" chip but not
        // the Hi-Res one.
        val lossless = file.lossless == true
        val hiRes = lossless &&
            (((file.bit_depth ?: 0) > 16) || ((file.sample_rate ?: 0) > 48_000))
        when {
            hiRes -> out.add("Hi-Res")
            lossless -> out.add("Lossless")
        }

        // Detail chip: bit-depth / sample-rate-in-kHz when both are
        // present. e.g. `24/96` for 24-bit 96 kHz FLAC. We strip
        // trailing zeros on the kHz so 44100 → "44.1" not "44.100".
        val bd = file.bit_depth
        val sr = file.sample_rate
        if (bd != null && sr != null && sr > 0) {
            out.add("$bd/${formatKHz(sr)}")
        }

        // ReplayGain availability flag — useful for audiophiles
        // troubleshooting volume-normalisation. Either track or album
        // gain qualifies.
        if (file.replaygain_track_gain != null || file.replaygain_album_gain != null) {
            out.add("ReplayGain")
        }

        return out
    }

    /** Format a sample rate in Hz as a kHz string with at most one
     *  decimal place: 44100 → "44.1", 48000 → "48", 96000 → "96",
     *  192000 → "192". Edge values (8000 / 22050) format the same
     *  way without crashing. */
    internal fun formatKHz(sampleRateHz: Int): String {
        val khz = sampleRateHz / 1000.0
        return if (khz == khz.toInt().toDouble()) khz.toInt().toString()
        else "%.1f".format(khz)
    }
}
