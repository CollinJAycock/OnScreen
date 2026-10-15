package tv.onscreen.mobile.ui.item

import tv.onscreen.mobile.data.model.ItemFile

/**
 * The versions of a movie or episode, from its files (v2.6). Pure, so it is
 * unit-tested on the JVM (ItemVersionsTest).
 *
 *  - A file's [ItemFile.edition] ("Director's Cut") names its version; a file
 *    without one is described by its picture ("4K · HEVC · HDR").
 *  - The parts of a stacked movie ("cd1" + "cd2", [ItemFile.part_index]) are
 *    ONE version, labelled "… · 2 parts" — never one version per part. It
 *    plays from its first part's file_id: the server streams every part as
 *    one movie from there. Parts belong together when they share an edition
 *    and a container (the server also checks they sit in one folder, which
 *    the API doesn't expose — it only sends part_index for a real stack).
 *
 * Versions keep the server's order (best first); a stack sits where its
 * first file did.
 */
object ItemVersions {

    data class Version(
        /** The file to play (and download) the version by: a stack's part 1. */
        val fileId: String,
        val label: String,
        /** Every file of the version, a stack's in part order. */
        val files: List<ItemFile>,
    ) {
        val partCount: Int get() = files.size
        val stacked: Boolean get() = files.size > 1
    }

    fun of(files: List<ItemFile>): List<Version> {
        val stacks = files.filter { it.part_index != null }.groupBy { stackKey(it) }
        val out = mutableListOf<Version>()
        val placed = mutableSetOf<String>()
        for (f in files) {
            if (f.id in placed) continue
            val parts = if (f.part_index != null) {
                stacks[stackKey(f)].orEmpty().sortedBy { it.part_index }
            } else {
                listOf(f)
            }
            parts.forEach { placed += it.id }
            val first = parts.first()
            out += Version(fileId = first.id, label = label(first, parts.size), files = parts)
        }
        return out
    }

    /** The version [fileId] belongs to, else the first. Null with no files. */
    fun pick(versions: List<Version>, fileId: String?): Version? =
        versions.firstOrNull { v -> v.files.any { it.id == fileId } } ?: versions.firstOrNull()

    /** Whether [file] is one part of a stacked movie. */
    fun isPart(file: ItemFile?): Boolean = file?.part_index != null

    /** "Director's Cut", "1080p · H.264", "Extended · 2 parts". */
    fun label(file: ItemFile, parts: Int = 1): String {
        val name = file.edition?.trim()?.takeIf { it.isNotEmpty() } ?: describe(file)
        val base = name.ifEmpty { "Version" }
        return if (parts > 1) "$base · $parts parts" else base
    }

    /** What the picture is, for a file whose name gives no edition. */
    fun describe(file: ItemFile): String = listOfNotNull(
        resolutionLabel(file.resolution_w, file.resolution_h),
        codecLabel(file.video_codec),
        hdrLabel(file.hdr_type),
    ).joinToString(" · ")

    private fun stackKey(f: ItemFile): String =
        (f.edition?.trim()?.lowercase() ?: "") + "\u0000" + (f.container?.lowercase() ?: "")

    private fun resolutionLabel(w: Int?, h: Int?): String? {
        // Width first: a 1920×800 scope film is 1080p, not "800p".
        val width = w ?: 0
        val height = h ?: 0
        return when {
            width >= 3200 || height >= 2000 -> "4K"
            width >= 1800 || height >= 1000 -> "1080p"
            width >= 1200 || height >= 700 -> "720p"
            width > 0 || height > 0 -> "SD"
            else -> null
        }
    }

    private fun codecLabel(codec: String?): String? = when (codec?.lowercase()) {
        null, "" -> null
        "h264", "avc", "avc1" -> "H.264"
        "hevc", "h265", "hvc1" -> "HEVC"
        "av1" -> "AV1"
        "vp9" -> "VP9"
        else -> codec.uppercase()
    }

    private fun hdrLabel(hdr: String?): String? = when (hdr?.lowercase()) {
        null, "", "sdr" -> null
        "dolby_vision" -> "Dolby Vision"
        "hlg" -> "HLG"
        else -> "HDR"
    }
}
