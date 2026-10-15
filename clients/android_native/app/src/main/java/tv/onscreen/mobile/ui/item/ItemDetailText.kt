package tv.onscreen.mobile.ui.item

import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemStudio

/*
 * Pure pieces of the detail page's header, unit-tested in ItemDetailTextTest.
 */

/** The line under the title: the year, then the studios (a movie) or
 *  networks (a show) — "2019 · A24 · Plan B". Null when there is neither. */
fun detailMetaLine(year: Int?, studios: List<ItemStudio>): String? {
    val parts = mutableListOf<String>()
    if (year != null) parts += year.toString()
    studios.map { it.name.trim() }.filter { it.isNotEmpty() }.distinct().forEach { parts += it }
    return parts.takeIf { it.isNotEmpty() }?.joinToString(" · ")
}

/** Item types whose page shows the clear logo in place of the title text.
 *  Not a season or an episode: their title isn't the show's name. */
private val LOGO_TYPES = setOf("movie", "show", "anime")

/**
 * The clear logo to show in place of [item]'s title, or null (none, another
 * type, or no server yet). Requested at its original size: ?w= would make the
 * server re-encode the transparent PNG as a JPEG.
 */
fun detailLogoUrl(serverUrl: String, item: ItemDetail): String? {
    if (item.type !in LOGO_TYPES || serverUrl.isEmpty()) return null
    val path = item.logo_path?.takeIf { it.isNotBlank() } ?: return null
    return artworkUrl(serverUrl, path, width = null)
}
