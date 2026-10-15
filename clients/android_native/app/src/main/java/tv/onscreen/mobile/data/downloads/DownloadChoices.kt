package tv.onscreen.mobile.data.downloads

import tv.onscreen.mobile.data.model.DownloadOptions

/**
 * What tapping Download offers (v2.6 converted downloads). Pure, so it is
 * unit-tested on the JVM (DownloadChoicesTest).
 *
 * The original file downloads as it always has. A server that can convert
 * offers smaller copies too ("720p · 4 Mbps"), each greyed out when it is
 * above the user's streaming limit. A stacked movie ("cd1" + "cd2") has no
 * original to offer — its first file is only part 1 — so only a converted copy
 * of the whole movie can be downloaded, and with none on offer Download says
 * why instead of fetching half a film.
 */
object DownloadChoices {

    /** The original file, by its own bytes. */
    const val ORIGINAL = "original"

    data class Choice(
        /** A converted quality's id, or [ORIGINAL]. */
        val quality: String,
        val label: String,
        /** Why a disabled choice can't be picked. */
        val note: String? = null,
        val enabled: Boolean = true,
    )

    /** What GET /items/{id}/downloads answered. */
    sealed interface Options {
        data class Offered(val options: DownloadOptions) : Options
        /** 403 DOWNLOADS_DISABLED: the server's download toggle is off. */
        data object Disabled : Options
        /** No answer: an older server (404), unreachable, or an error. */
        data object Unavailable : Options
    }

    sealed interface Plan {
        /** Download the original at once, as before (nothing to choose). */
        data object Original : Plan
        /** Let the user pick; [choices] has at least one enabled entry. */
        data class Choose(val choices: List<Choice>) : Plan
        /** Nothing can be downloaded; show [message]. */
        data class Blocked(val message: String) : Plan
    }

    const val DISABLED_MESSAGE = "Downloads are turned off on this server."
    const val STACKED_MESSAGE =
        "This movie is split into parts, and this server can’t make a single download of it."
    const val ABOVE_LIMIT_NOTE = "Above your streaming limit"
    const val STACKED_ABOVE_LIMIT_MESSAGE =
        "This movie is split into parts, and every size it can be downloaded in is above your streaming limit."

    fun plan(options: Options, stacked: Boolean): Plan {
        val offered = (options as? Options.Offered)?.options
        if (options is Options.Disabled) return Plan.Blocked(DISABLED_MESSAGE)
        val converted = offered?.qualities.orEmpty().map { q ->
            Choice(
                quality = q.id,
                label = q.label.ifBlank { q.id },
                note = if (q.allowed) null else ABOVE_LIMIT_NOTE,
                enabled = q.allowed,
            )
        }
        if (stacked) {
            return when {
                converted.isEmpty() -> Plan.Blocked(STACKED_MESSAGE)
                converted.none { it.enabled } -> Plan.Blocked(STACKED_ABOVE_LIMIT_MESSAGE)
                else -> Plan.Choose(converted)
            }
        }
        // Nothing smaller on offer (an older server, a file that can't be
        // converted): the original, straight away, as before.
        if (converted.none { it.enabled }) return Plan.Original
        return Plan.Choose(listOf(Choice(ORIGINAL, "Original")) + converted)
    }
}
