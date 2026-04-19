package tv.onscreen.android.ui.common

import androidx.leanback.widget.AbstractDetailsDescriptionPresenter
import tv.onscreen.android.data.model.ItemDetail

class DetailsDescriptionPresenter : AbstractDetailsDescriptionPresenter() {

    override fun onBindDescription(vh: ViewHolder, item: Any) {
        val detail = item as? ItemDetail ?: return

        vh.title.text = detail.title

        val parts = mutableListOf<String>()
        detail.year?.let { parts.add(it.toString()) }
        detail.content_rating?.let { parts.add(it) }
        detail.duration_ms?.let { ms ->
            val min = ms / 60_000
            if (min >= 60) {
                parts.add("${min / 60}h ${min % 60}m")
            } else {
                parts.add("${min}m")
            }
        }
        detail.rating?.let { parts.add("★ %.1f".format(it)) }
        if (detail.genres.isNotEmpty()) {
            parts.add(detail.genres.take(3).joinToString(", "))
        }
        vh.subtitle.text = parts.joinToString(" · ")

        vh.body.text = detail.summary ?: ""
    }
}
