package tv.onscreen.android.playback

import tv.onscreen.android.data.model.ItemFile

/**
 * An item's versions (server v2.6): each of its files is a version — a
 * different cut ([ItemFile.edition], "Director's Cut") or encode — except
 * the parts of a stacked movie (cd1 + cd2, [ItemFile.part_index]), which
 * together are ONE version. A part is never played on its own: playback goes
 * through the server's play decision / transcode start with the first part's
 * id, and the server streams every part as one movie (the item's duration
 * covers all of them; a part's own duration_ms is just that part).
 */
object MediaVersions {

    /** One version to choose: the file playback starts from ([fileId], a
     *  stack's first part) and how many files it spans. */
    data class Version(val fileId: String, val label: String, val parts: Int = 1)

    /** Whether [file] is one part of a stacked movie. */
    fun isPart(file: ItemFile?): Boolean = file?.part_index != null

    /** The parts of the stacked movie among [files], in part order. */
    fun parts(files: List<ItemFile>): List<ItemFile> =
        files.filter { it.part_index != null }.sortedBy { it.part_index }

    /**
     * The file to play: the one [fileId] names, else the first listed (the
     * server lists the best first). A part stands for its whole movie, so a
     * stacked movie always starts from its first part — whichever part was
     * named or listed first.
     */
    fun playFile(files: List<ItemFile>, fileId: String? = null): ItemFile? {
        val chosen = fileId?.let { id -> files.firstOrNull { it.id == id } } ?: files.firstOrNull()
        if (chosen?.part_index == null) return chosen
        return parts(files).firstOrNull() ?: chosen
    }

    /**
     * The versions of an item with [files], in the server's order (best
     * first); a stacked movie's parts collapse into one version where its
     * first-listed part stands. Labelled by edition when the file names one,
     * else by what it is ("4K · HEVC · HDR"); a stack adds its part count.
     */
    fun versions(files: List<ItemFile>): List<Version> {
        val parts = parts(files)
        var stackAdded = false
        val out = mutableListOf<Version>()
        for (f in files) {
            if (f.part_index == null) {
                out += Version(f.id, label(f))
            } else if (!stackAdded) {
                stackAdded = true
                val first = parts.first()
                val base = label(first)
                val count = "${parts.size} parts"
                out += Version(first.id, if (base.isEmpty()) count else "$base · $count", parts.size)
            }
        }
        // Two files with nothing to tell them apart keep their order.
        return out.mapIndexed { i, v -> if (v.label.isEmpty()) v.copy(label = "Version ${i + 1}") else v }
    }

    /** A file's own label: its edition, else resolution, codec and HDR. */
    fun label(file: ItemFile): String {
        file.edition?.trim()?.takeIf { it.isNotEmpty() }?.let { return it }
        val parts = mutableListOf<String>()
        resolutionLabel(file.resolution_h)?.let { parts += it }
        codecLabel(file.video_codec)?.let { parts += it }
        hdrLabel(file.hdr_type)?.let { parts += it }
        return parts.joinToString(" · ")
    }

    /**
     * The length the server lists for [file]'s playback, for progress and
     * Up Next (PlaybackHelper.listedDurationMs): the file's own, except a
     * part's, which is only that part — a stacked movie runs the item's
     * length.
     */
    fun listedDurationMs(file: ItemFile?, itemDurationMs: Long?): Long? {
        val fileMs = if (isPart(file)) null else file?.duration_ms
        return tv.onscreen.android.ui.playback.PlaybackHelper.listedDurationMs(fileMs, itemDurationMs)
    }

    private fun resolutionLabel(height: Int?): String? = when {
        height == null || height <= 0 -> null
        height >= 2000 -> "4K"
        height >= 1000 -> "1080p"
        height >= 700 -> "720p"
        height >= 560 -> "576p"
        else -> "${height}p"
    }

    private fun codecLabel(codec: String?): String? = when (codec?.lowercase()) {
        null, "" -> null
        "h264", "avc" -> "H.264"
        "hevc", "h265" -> "HEVC"
        "av1" -> "AV1"
        "vp9" -> "VP9"
        else -> codec.uppercase()
    }

    private fun hdrLabel(hdr: String?): String? = when (hdr?.lowercase()) {
        null, "", "sdr" -> null
        "hdr10" -> "HDR10"
        "hdr10plus", "hdr10+" -> "HDR10+"
        "hlg" -> "HLG"
        "dolby_vision" -> "Dolby Vision"
        else -> "HDR"
    }
}
