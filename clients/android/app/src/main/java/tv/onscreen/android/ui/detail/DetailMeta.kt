package tv.onscreen.android.ui.detail

import tv.onscreen.android.data.model.ItemDetail

/** What the detail screen shows under the title. */
object DetailMeta {

    /** Types whose clear logo (logo_path, v2.6) stands in for the title. */
    private val LOGO_TYPES = setOf("movie", "show")

    /**
     * The metadata line's parts, joined with " · ": year, content rating,
     * runtime, rating, up to three genres, and (v2.6) up to two studios — a
     * movie's production companies, a show's networks.
     */
    fun lineParts(item: ItemDetail): List<String> {
        val parts = mutableListOf<String>()
        item.year?.let { parts.add(it.toString()) }
        item.content_rating?.let { parts.add(it) }
        item.duration_ms?.let { ms ->
            val min = ms / 60_000
            parts.add(if (min >= 60) "${min / 60}h ${min % 60}m" else "${min}m")
        }
        item.rating?.let { parts.add("★ %.1f".format(it)) }
        if (item.genres.isNotEmpty()) parts.add(item.genres.take(3).joinToString(", "))
        val studios = item.studios.map { it.name.trim() }.filter { it.isNotEmpty() }.distinct()
        if (studios.isNotEmpty()) parts.add(studios.take(2).joinToString(", "))
        return parts
    }

    /** The clear logo to show in place of the title, when [item] (a movie or
     *  show) has one. */
    fun logoPath(item: ItemDetail): String? =
        item.logo_path?.takeIf { it.isNotBlank() && item.type in LOGO_TYPES }
}
