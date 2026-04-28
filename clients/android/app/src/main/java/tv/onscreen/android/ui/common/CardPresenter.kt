package tv.onscreen.android.ui.common

import android.animation.AnimatorSet
import android.animation.ObjectAnimator
import android.content.Context
import android.graphics.Color
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.leanback.widget.Presenter
import coil.load
import tv.onscreen.android.R
import tv.onscreen.android.data.artworkUrl
import tv.onscreen.android.data.model.*
import tv.onscreen.android.data.model.MediaCollection

class CardPresenter(private val context: Context, private val serverUrl: String = "") : Presenter() {

    companion object {
        private const val CARD_WIDTH = 240
        private const val CARD_HEIGHT = 360
        private const val FOCUS_SCALE = 1.2f
        private const val ANIM_DURATION = 200L
    }

    override fun onCreateViewHolder(parent: ViewGroup): ViewHolder {
        val container = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            isFocusable = true
            isFocusableInTouchMode = true
            setBackgroundColor(Color.TRANSPARENT)
            clipChildren = false
            clipToPadding = false
            layoutParams = ViewGroup.LayoutParams(CARD_WIDTH, ViewGroup.LayoutParams.WRAP_CONTENT)

            setOnFocusChangeListener { v, hasFocus ->
                val scale = if (hasFocus) FOCUS_SCALE else 1.0f
                val layout = v as? LinearLayout ?: return@setOnFocusChangeListener
                val titleView = layout.findViewWithTag<TextView>("title")
                    ?: return@setOnFocusChangeListener

                val scaleX = ObjectAnimator.ofFloat(layout, View.SCALE_X, scale)
                val scaleY = ObjectAnimator.ofFloat(layout, View.SCALE_Y, scale)
                val titleAlpha = ObjectAnimator.ofFloat(titleView, View.ALPHA, if (hasFocus) 1f else 0f)
                AnimatorSet().apply {
                    playTogether(scaleX, scaleY, titleAlpha)
                    duration = ANIM_DURATION
                    start()
                }

                layout.elevation = if (hasFocus) 8f else 0f
            }
        }

        val imageView = ImageView(context).apply {
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, CARD_HEIGHT)
            scaleType = ImageView.ScaleType.CENTER_CROP
            setBackgroundColor(context.getColor(R.color.bg_elevated))
            tag = "poster"
        }

        val titleView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = 12
            }
            gravity = Gravity.CENTER_HORIZONTAL
            maxLines = 2
            setTextColor(context.getColor(R.color.text_primary))
            textSize = 14f
            alpha = 0f
            tag = "title"
        }

        container.addView(imageView)
        container.addView(titleView)

        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val container = viewHolder.view as LinearLayout
        val imageView = container.findViewWithTag<ImageView>("poster")
        val titleView = container.findViewWithTag<TextView>("title")
        val data = extractCardData(item) ?: return

        titleView.text = data.title

        // Audiobooks (and some photos) come back without a
        // poster_path / thumb_path because the server stores their
        // image data in the source file itself rather than as an
        // /artwork/ asset. Fall back to the generic per-item image
        // endpoint, which is type-agnostic and re-encodes whatever
        // image the server has on hand.
        val url = when {
            data.posterPath != null && serverUrl.isNotEmpty() ->
                artworkUrl(serverUrl, data.posterPath)
            data.itemId != null && serverUrl.isNotEmpty() ->
                "$serverUrl/api/v1/items/${data.itemId}/image?w=500"
            else -> null
        }
        if (url != null) {
            imageView.load(url) {
                crossfade(true)
                placeholder(R.color.bg_elevated)
                error(R.color.bg_elevated)
            }
        } else {
            imageView.setImageDrawable(null)
            imageView.setBackgroundColor(context.getColor(R.color.bg_elevated))
        }
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {
        val container = viewHolder.view as LinearLayout
        val imageView = container.findViewWithTag<ImageView>("poster")
        imageView.setImageDrawable(null)
    }

    private data class CardData(
        val title: String,
        val posterPath: String?,
        /** Item id used to build the per-item /image fallback when
         *  the server didn't expose an /artwork/ path (audiobooks,
         *  some photos). Null for non-item types like collections. */
        val itemId: String?,
    )

    private fun extractCardData(item: Any): CardData? = when (item) {
        is HubItem -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is MediaItem -> CardData(item.title, item.poster_path, item.id)
        is ChildItem -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is SearchResult -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is MediaCollection -> CardData(item.name, item.poster_path, null)
        is CollectionItem -> CardData(item.title, item.poster_path, item.id)
        is FavoriteItem -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is HistoryItem -> CardData(item.title, item.thumb_path, item.id)
        else -> null
    }
}
