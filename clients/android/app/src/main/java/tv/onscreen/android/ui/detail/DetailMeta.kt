package tv.onscreen.android.ui.detail

import tv.onscreen.android.R
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.i18n.UiText

/** What the detail screen shows under the title. */
object DetailMeta {

    /** Types whose clear logo (logo_path, v2.6) stands in for the title. */
    private val LOGO_TYPES = setOf("movie", "show")

    /**
     * The metadata line's parts, joined with " · ": year, content rating,
     * runtime, rating, up to three genres, and (v2.6) up to two studios — a
     * movie's production companies, a show's networks.
     */
    fun lineParts(item: ItemDetail): List<UiText> {
        val parts = mutableListOf<UiText>()
        item.year?.let { parts.add(UiText.Raw(it.toString())) }
        item.content_rating?.let { parts.add(UiText.Raw(it)) }
        item.duration_ms?.let { parts.add(runtime(it)) }
        item.rating?.let { parts.add(UiText.Raw("★ %.1f".format(it))) }
        if (item.genres.isNotEmpty()) parts.add(UiText.Raw(item.genres.take(3).joinToString(", ")))
        val studios = item.studios.map { it.name.trim() }.filter { it.isNotEmpty() }.distinct()
        if (studios.isNotEmpty()) parts.add(UiText.Raw(studios.take(2).joinToString(", ")))
        return parts
    }

    /** A running time: "1h 30m", "45m". */
    fun runtime(ms: Long): UiText {
        val min = ms / 60_000
        return if (min >= 60) {
            UiText.Res(R.string.runtime_hours, min / 60, min % 60)
        } else {
            UiText.Res(R.string.runtime_minutes, min)
        }
    }

    /** The clear logo to show in place of the title, when [item] (a movie or
     *  show) has one. */
    fun logoPath(item: ItemDetail): String? =
        item.logo_path?.takeIf { it.isNotBlank() && item.type in LOGO_TYPES }
}
