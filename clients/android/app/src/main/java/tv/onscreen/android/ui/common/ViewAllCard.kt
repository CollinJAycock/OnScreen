package tv.onscreen.android.ui.common

import android.content.Context
import android.graphics.Color
import android.graphics.Outline
import android.graphics.Rect
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.ViewOutlineProvider
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import androidx.leanback.widget.Presenter
import tv.onscreen.android.R

/**
 * Tail tile on each library preview row that opens the full library
 * grid. Without this the home page is the only way to reach a
 * library — there's no row-header click affordance in
 * BrowseSupportFragment, and the library preview only shows the top
 * N items.
 *
 * @property libraryId target library
 * @property libraryName label rendered on the tile + carried into
 *   LibraryFragment as the screen title
 * @property libraryType drives library-type-specific behaviour in
 *   LibraryFragment (e.g. home_video defaults to created_at-DESC sort
 *   instead of title-ASC).
 */
data class ViewAllCard(
    val libraryId: String,
    val libraryName: String,
    val libraryType: String,
)

/**
 * Presenter for [ViewAllCard]. Renders a tile the same size as the
 * other library cards (so the row stays visually consistent) but
 * with a flat background + bold "View all <library>" label instead
 * of poster art.
 */
class ViewAllCardPresenter(private val context: Context) : Presenter() {

    companion object {
        private const val CARD_WIDTH = 240
        private const val CARD_HEIGHT = 360
        private const val CARD_RADIUS_DP = 12f
    }

    override fun onCreateViewHolder(parent: ViewGroup): ViewHolder {
        val density = context.resources.displayMetrics.density
        val cornerPx = CARD_RADIUS_DP * density

        val container = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            isFocusable = true
            isFocusableInTouchMode = true
            setBackgroundColor(Color.TRANSPARENT)
            clipChildren = false
            clipToPadding = false
            layoutParams = ViewGroup.LayoutParams(CARD_WIDTH, ViewGroup.LayoutParams.WRAP_CONTENT)
        }

        val tile = FrameLayout(context).apply {
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, CARD_HEIGHT)
            clipToOutline = true
            outlineProvider = object : ViewOutlineProvider() {
                override fun getOutline(view: View, outline: Outline) {
                    outline.setRoundRect(Rect(0, 0, view.width, view.height), cornerPx)
                }
            }
            setBackgroundColor(context.resources.getColor(R.color.bg_secondary, null))
            foreground = context.getDrawable(R.drawable.card_focus_state)
        }

        val label = TextView(context).apply {
            gravity = Gravity.CENTER
            setTextColor(context.resources.getColor(R.color.accent, null))
            textSize = 18f
            setTypeface(typeface, android.graphics.Typeface.BOLD)
            setPadding(24, 24, 24, 24)
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            )
        }
        tile.addView(label)
        container.addView(tile)

        // Footer label below the tile mirrors the title-area on the
        // standard CardPresenter so the row's vertical rhythm matches.
        val footer = TextView(context).apply {
            setTextColor(context.resources.getColor(R.color.text_primary, null))
            textSize = 13f
            maxLines = 1
            ellipsize = android.text.TextUtils.TruncateAt.END
            setPadding(8, 8, 8, 0)
            layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.WRAP_CONTENT,
            )
        }
        container.addView(footer)

        container.tag = ViewHolderRefs(label = label, footer = footer)
        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any?) {
        val card = item as? ViewAllCard ?: return
        val refs = (viewHolder.view.tag as? ViewHolderRefs) ?: return
        refs.label.text = viewHolder.view.context.getString(R.string.view_all)
        refs.footer.text = card.libraryName
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {
        // No-op — no resources to release (no image loaders).
    }

    private data class ViewHolderRefs(val label: TextView, val footer: TextView)
}
