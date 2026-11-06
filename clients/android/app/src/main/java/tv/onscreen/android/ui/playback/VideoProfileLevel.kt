package tv.onscreen.android.ui.playback

import android.annotation.SuppressLint
import android.media.MediaCodecInfo.CodecProfileLevel

/**
 * The header's video profile / level keys (docs/capability-profiles.md,
 * "Video profile / level", server v2.7): `h264Profile` / `h264Level`,
 * `hevcProfile` / `hevcLevel`, `av1Profile` / `av1Level`, from the
 * (CodecProfileLevel profile, level) pairs this device's HARDWARE decoders
 * report. A source over the declared profile or level is transcoded and the
 * re-encode is sized to fit; a codec left out isn't gated (as before v2.7).
 *
 * Pure: the device probe lives in [PlaybackHelper]. The phone app
 * (clients/android_native, tv.onscreen.mobile.ui.player.VideoProfileLevel)
 * has the same object; keep the two in step.
 *
 * Erring low only costs a needless transcode; erring high is a black screen.
 * So:
 * - The server takes one profile and one level per codec, each rank
 *   decoding everything below it (internal/transcode/profilelevel.go). The
 *   pair sent is one the decoders actually reach: the level is the highest
 *   reported for that profile or a higher one ([keys]).
 * - A profile or level constant not mapped here is ignored, never guessed.
 * - H.264 Constrained High has no B-frames, so it doesn't decode Main: it
 *   counts as Baseline (it does decode Constrained Baseline). Extended
 *   isn't on the server's ladder and is skipped, as is HEVC Main Still.
 * - HEVC tiers: the server can't express one (the file side is the level
 *   alone), so a High-tier entry counts as its level, which is right for
 *   the Main-tier files nearly everything is (a High-tier decoder decodes
 *   Main tier at the same level); a High-tier file is not gated, as before.
 * - Android has no HEVC RExt or AV1 High / Professional constants, so
 *   neither is ever claimed.
 */
// The newer constants (Constrained profiles, level 6+, AV1) are compile-time
// ints: an older device never reports them, so mapping them is safe there.
@SuppressLint("InlinedApi")
internal object VideoProfileLevel {

    const val MIME_AVC = "video/avc"
    const val MIME_HEVC = "video/hevc"
    const val MIME_AV1 = "video/av01"

    /** The decoder MIME types gated, in header order. */
    val MIME_TYPES = listOf(MIME_AVC, MIME_HEVC, MIME_AV1)

    /**
     * The header keys for every codec, from each MIME type's reported
     * (profile, level) pairs ([keys]); a MIME type missing from [byMime]
     * (no hardware decoder) adds none.
     */
    fun headerKeys(byMime: Map<String, Collection<Pair<Int, Int>>>): List<String> =
        MIME_TYPES.flatMap { mime -> byMime[mime]?.let { keys(mime, it) } ?: emptyList() }

    /**
     * The profile and level keys for one decoder MIME type ([MIME_TYPES])
     * from its (CodecProfileLevel profile, level) [entries], or none when no
     * entry maps to a ranked profile and a known level.
     *
     * Each entry stands for its profile and every lower one, at its level
     * and below. The level a profile gets is the highest among the entries
     * of that profile or a higher one, so the pair sent is always reached by
     * a real entry. The profile sent is the highest whose level is the same
     * as the codec's usual profile's (H.264 High; HEVC Main 10; AV1 Main):
     * going higher never costs level (a decoder reporting High 10 at 4.1 but
     * High at 5.1 sends high + 5.1, and a 10-bit H.264 file is transcoded),
     * and going lower for level (a Baseline-only 5.1 entry) isn't done.
     */
    fun keys(mime: String, entries: Collection<Pair<Int, Int>>): List<String> {
        val codec = CODECS[mime] ?: return emptyList()
        val ranked = entries.mapNotNull { (profile, level) ->
            val rank = codec.profileRank[profile] ?: return@mapNotNull null
            val tenths = codec.levelTenths[level] ?: return@mapNotNull null
            rank to tenths
        }
        if (ranked.isEmpty()) return emptyList()
        // The highest level a decoder of [rank] or a higher profile reaches.
        fun levelAt(rank: Int): Int = ranked.filter { it.first >= rank }.maxOf { it.second }
        val top = ranked.maxOf { it.first }
        val base = minOf(top, codec.usualRank)
        val baseLevel = levelAt(base)
        val rank = (base..top).last { levelAt(it) == baseLevel }
        return listOf(
            "${codec.key}Profile=${codec.names[rank - 1]}",
            "${codec.key}Level=${formatLevel(baseLevel)}",
        )
    }

    /** Tenths as the server reads a level: 41 → "4.1", 50 → "5"
     *  (transcode.FormatLevel). */
    fun formatLevel(tenths: Int): String =
        if (tenths % 10 == 0) "${tenths / 10}" else "${tenths / 10}.${tenths % 10}"

    private class Codec(
        /** The header key prefix. */
        val key: String,
        /** The server's profile names, rank 1 first. */
        val names: List<String>,
        /** CodecProfileLevel profile → rank (1-based, into [names]). */
        val profileRank: Map<Int, Int>,
        /** CodecProfileLevel level → level in tenths (41 = 4.1). */
        val levelTenths: Map<Int, Int>,
        /** The rank of the profile nearly all of this codec's files use. */
        val usualRank: Int,
    )

    private val CODECS: Map<String, Codec> = mapOf(
        MIME_AVC to Codec(
            key = "h264",
            names = listOf("baseline", "main", "high", "high10", "high422", "high444"),
            profileRank = mapOf(
                CodecProfileLevel.AVCProfileBaseline to 1,
                CodecProfileLevel.AVCProfileConstrainedBaseline to 1,
                // No B-frames: decodes Constrained Baseline, not Main.
                CodecProfileLevel.AVCProfileConstrainedHigh to 1,
                CodecProfileLevel.AVCProfileMain to 2,
                CodecProfileLevel.AVCProfileHigh to 3,
                CodecProfileLevel.AVCProfileHigh10 to 4,
                CodecProfileLevel.AVCProfileHigh422 to 5,
                CodecProfileLevel.AVCProfileHigh444 to 6,
            ),
            levelTenths = mapOf(
                CodecProfileLevel.AVCLevel1 to 10,
                // 1b sits between 1 and 1.1; read as 1, as the server reads it.
                CodecProfileLevel.AVCLevel1b to 10,
                CodecProfileLevel.AVCLevel11 to 11,
                CodecProfileLevel.AVCLevel12 to 12,
                CodecProfileLevel.AVCLevel13 to 13,
                CodecProfileLevel.AVCLevel2 to 20,
                CodecProfileLevel.AVCLevel21 to 21,
                CodecProfileLevel.AVCLevel22 to 22,
                CodecProfileLevel.AVCLevel3 to 30,
                CodecProfileLevel.AVCLevel31 to 31,
                CodecProfileLevel.AVCLevel32 to 32,
                CodecProfileLevel.AVCLevel4 to 40,
                CodecProfileLevel.AVCLevel41 to 41,
                CodecProfileLevel.AVCLevel42 to 42,
                CodecProfileLevel.AVCLevel5 to 50,
                CodecProfileLevel.AVCLevel51 to 51,
                CodecProfileLevel.AVCLevel52 to 52,
                CodecProfileLevel.AVCLevel6 to 60,
                CodecProfileLevel.AVCLevel61 to 61,
                CodecProfileLevel.AVCLevel62 to 62,
            ),
            usualRank = 3,
        ),
        MIME_HEVC to Codec(
            key = "hevc",
            names = listOf("main", "main10"),
            profileRank = mapOf(
                CodecProfileLevel.HEVCProfileMain to 1,
                CodecProfileLevel.HEVCProfileMain10 to 2,
                CodecProfileLevel.HEVCProfileMain10HDR10 to 2,
                CodecProfileLevel.HEVCProfileMain10HDR10Plus to 2,
            ),
            levelTenths = mapOf(
                CodecProfileLevel.HEVCMainTierLevel1 to 10,
                CodecProfileLevel.HEVCHighTierLevel1 to 10,
                CodecProfileLevel.HEVCMainTierLevel2 to 20,
                CodecProfileLevel.HEVCHighTierLevel2 to 20,
                CodecProfileLevel.HEVCMainTierLevel21 to 21,
                CodecProfileLevel.HEVCHighTierLevel21 to 21,
                CodecProfileLevel.HEVCMainTierLevel3 to 30,
                CodecProfileLevel.HEVCHighTierLevel3 to 30,
                CodecProfileLevel.HEVCMainTierLevel31 to 31,
                CodecProfileLevel.HEVCHighTierLevel31 to 31,
                CodecProfileLevel.HEVCMainTierLevel4 to 40,
                CodecProfileLevel.HEVCHighTierLevel4 to 40,
                CodecProfileLevel.HEVCMainTierLevel41 to 41,
                CodecProfileLevel.HEVCHighTierLevel41 to 41,
                CodecProfileLevel.HEVCMainTierLevel5 to 50,
                CodecProfileLevel.HEVCHighTierLevel5 to 50,
                CodecProfileLevel.HEVCMainTierLevel51 to 51,
                CodecProfileLevel.HEVCHighTierLevel51 to 51,
                CodecProfileLevel.HEVCMainTierLevel52 to 52,
                CodecProfileLevel.HEVCHighTierLevel52 to 52,
                CodecProfileLevel.HEVCMainTierLevel6 to 60,
                CodecProfileLevel.HEVCHighTierLevel6 to 60,
                CodecProfileLevel.HEVCMainTierLevel61 to 61,
                CodecProfileLevel.HEVCHighTierLevel61 to 61,
                CodecProfileLevel.HEVCMainTierLevel62 to 62,
                CodecProfileLevel.HEVCHighTierLevel62 to 62,
            ),
            usualRank = 2,
        ),
        MIME_AV1 to Codec(
            key = "av1",
            // AV1 Main is 8- and 10-bit 4:2:0; Android names no High or
            // Professional profile.
            names = listOf("main"),
            profileRank = mapOf(
                CodecProfileLevel.AV1ProfileMain8 to 1,
                CodecProfileLevel.AV1ProfileMain10 to 1,
                CodecProfileLevel.AV1ProfileMain10HDR10 to 1,
                CodecProfileLevel.AV1ProfileMain10HDR10Plus to 1,
            ),
            levelTenths = mapOf(
                CodecProfileLevel.AV1Level2 to 20,
                CodecProfileLevel.AV1Level21 to 21,
                CodecProfileLevel.AV1Level22 to 22,
                CodecProfileLevel.AV1Level23 to 23,
                CodecProfileLevel.AV1Level3 to 30,
                CodecProfileLevel.AV1Level31 to 31,
                CodecProfileLevel.AV1Level32 to 32,
                CodecProfileLevel.AV1Level33 to 33,
                CodecProfileLevel.AV1Level4 to 40,
                CodecProfileLevel.AV1Level41 to 41,
                CodecProfileLevel.AV1Level42 to 42,
                CodecProfileLevel.AV1Level43 to 43,
                CodecProfileLevel.AV1Level5 to 50,
                CodecProfileLevel.AV1Level51 to 51,
                CodecProfileLevel.AV1Level52 to 52,
                CodecProfileLevel.AV1Level53 to 53,
                CodecProfileLevel.AV1Level6 to 60,
                CodecProfileLevel.AV1Level61 to 61,
                CodecProfileLevel.AV1Level62 to 62,
                CodecProfileLevel.AV1Level63 to 63,
                CodecProfileLevel.AV1Level7 to 70,
                CodecProfileLevel.AV1Level71 to 71,
                CodecProfileLevel.AV1Level72 to 72,
                CodecProfileLevel.AV1Level73 to 73,
            ),
            usualRank = 1,
        ),
    )
}
