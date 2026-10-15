package tv.onscreen.android.ui.common

import androidx.leanback.widget.AbstractDetailsDescriptionPresenter
import tv.onscreen.android.data.model.ItemDetail

class DetailsDescriptionPresenter : AbstractDetailsDescriptionPresenter() {

    override fun onBindDescription(vh: ViewHolder, item: Any) {
        val detail = item as? ItemDetail ?: return

        vh.title.text = detail.title

        // The detail screen's own line (studios / networks included).
        vh.subtitle.text = tv.onscreen.android.ui.detail.DetailMeta.lineParts(detail).joinToString(" · ")

        vh.body.text = detail.summary ?: ""
    }
}
