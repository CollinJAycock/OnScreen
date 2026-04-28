package tv.onscreen.android.ui.common

import android.animation.AnimatorSet
import android.animation.ObjectAnimator
import android.content.Context
import android.graphics.Color
import android.graphics.Outline
import android.graphics.Rect
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.ViewOutlineProvider
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.leanback.widget.Presenter
import coil.load
import tv.onscreen.android.R
import tv.onscreen.android.data.artworkUrl
import tv.onscreen.android.data.model.*
import tv.onscreen.android.data.model.MediaCollection

/**
 * Modernised TV card.
 *
 * Visual contract vs. the old presenter:
 *  - Rounded corners (12 dp) clipped via OutlineProvider on the poster
 *    frame so the artwork doesn't overflow the focus ring.
 *  - Focus indicator is a 3 dp accent stroke + lifted elevation + a
 *    light scale (1.05×, was 1.2×). The big-zoom feel of stock Leanback
 *    is what makes the app read as "old TV app" — keep just enough
 *    motion to make the active card pop.
 *  - Title is always visible. Unfocused: 60% alpha, regular weight.
 *    Focused: 100% alpha, bold. Eyes scan the row faster when names
 *    are readable at rest, and there's nowhere else for the title to
 *    surface in this layout.
 */
class CardPresenter(private val context: Context, private val serverUrl: String = "") : Presenter() {

    companion object {
        private const val CARD_WIDTH = 240
        private const val CARD_HEIGHT = 360
        private const val FOCUS_SCALE = 1.05f
        private const val ANIM_DURATION = 180L
        private const val CARD_RADIUS_DP = 12f
        private const val UNFOCUSED_TITLE_ALPHA = 0.6f
        private const val FOCUSED_ELEVATION_DP = 12f
    }

    override fun onCreateViewHolder(parent: ViewGroup): ViewHolder {
        val density = context.resources.displayMetrics.density
        val cornerPx = CARD_RADIUS_DP * density
        val focusedElevPx = FOCUSED_ELEVATION_DP * density

        val container = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            isFocusable = true
            isFocusableInTouchMode = true
            setBackgroundColor(Color.TRANSPARENT)
            clipChildren = false
            clipToPadding = false
            layoutParams = ViewGroup.LayoutParams(CARD_WIDTH, ViewGroup.LayoutParams.WRAP_CONTENT)
        }

        // Poster + focus ring stack. The ring is the FrameLayout's
        // foreground, so it draws on top of the image when focused
        // (state_focused selector) and is transparent otherwise.
        val posterFrame = FrameLayout(context).apply {
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, CARD_HEIGHT)
            clipToOutline = true
            outlineProvider = object : ViewOutlineProvider() {
                override fun getOutline(view: View, outline: Outline) {
                    outline.setRoundRect(Rect(0, 0, view.width, view.height), cornerPx)
                }
            }
            foreground = context.getDrawable(R.drawable.card_focus_state)
            tag = "frame"
        }

        val imageView = ImageView(context).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            )
            scaleType = ImageView.ScaleType.CENTER_CROP
            setBackgroundColor(context.getColor(R.color.bg_elevated))
            tag = "poster"
        }
        posterFrame.addView(imageView)

        val titleView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = (10 * density).toInt()
            }
            gravity = Gravity.CENTER_HORIZONTAL
            maxLines = 2
            setTextColor(context.getColor(R.color.text_primary))
            textSize = 13f
            alpha = UNFOCUSED_TITLE_ALPHA
            tag = "title"
        }

        container.addView(posterFrame)
        container.addView(titleView)

        // Focus animation: small scale, elevation lift, title pop.
        // ObjectAnimator on `elevation` is the cheapest way to drive
        // the system shadow (no manual blur work).
        container.setOnFocusChangeListener { v, hasFocus ->
            val layout = v as? LinearLayout ?: return@setOnFocusChangeListener
            val title = layout.findViewWithTag<TextView>("title") ?: return@setOnFocusChangeListener
            val frame = layout.findViewWithTag<FrameLayout>("frame") ?: return@setOnFocusChangeListener

            val scale = if (hasFocus) FOCUS_SCALE else 1.0f
            val titleAlpha = if (hasFocus) 1f else UNFOCUSED_TITLE_ALPHA
            val elev = if (hasFocus) focusedElevPx else 0f

            AnimatorSet().apply {
                playTogether(
                    ObjectAnimator.ofFloat(layout, View.SCALE_X, scale),
                    ObjectAnimator.ofFloat(layout, View.SCALE_Y, scale),
                    ObjectAnimator.ofFloat(title, View.ALPHA, titleAlpha),
                    ObjectAnimator.ofFloat(frame, "elevation", elev),
                )
                duration = ANIM_DURATION
                start()
            }
        }

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
