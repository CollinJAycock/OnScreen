package tv.onscreen.android.ui.common

import android.animation.ObjectAnimator
import android.content.Context
import android.graphics.Color
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.core.content.ContextCompat
import androidx.leanback.widget.Presenter
import tv.onscreen.android.R

class NavCardPresenter(private val context: Context) : Presenter() {

    companion object {
        private const val CARD_WIDTH = 240
        private const val CARD_HEIGHT = 160
        private const val FOCUS_SCALE = 1.15f
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
                val tile = (v as LinearLayout).findViewWithTag<LinearLayout>("tile") ?: return@setOnFocusChangeListener
                ObjectAnimator.ofFloat(tile, View.SCALE_X, scale).setDuration(ANIM_DURATION).start()
                ObjectAnimator.ofFloat(tile, View.SCALE_Y, scale).setDuration(ANIM_DURATION).start()
                tile.setBackgroundResource(if (hasFocus) R.drawable.nav_card_focused else R.drawable.nav_card_bg)
                v.elevation = if (hasFocus) 8f else 0f
            }
        }

        val tile = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, CARD_HEIGHT)
            setBackgroundResource(R.drawable.nav_card_bg)
            tag = "tile"
        }

        val iconWrap = android.widget.FrameLayout(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            )
            clipChildren = false
            clipToPadding = false
        }

        val icon = ImageView(context).apply {
            layoutParams = android.widget.FrameLayout.LayoutParams(64, 64)
            tag = "icon"
        }

        val badge = TextView(context).apply {
            val lp = android.widget.FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            )
            lp.gravity = Gravity.TOP or Gravity.END
            lp.topMargin = -6
            lp.rightMargin = -12
            layoutParams = lp
            setBackgroundResource(R.drawable.badge_bg)
            setTextColor(context.getColor(R.color.text_primary))
            textSize = 11f
            setPadding(10, 2, 10, 2)
            minWidth = 32
            gravity = Gravity.CENTER
            visibility = View.GONE
            tag = "badge"
        }

        iconWrap.addView(icon)
        iconWrap.addView(badge)

        val title = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply { topMargin = 12 }
            setTextColor(context.getColor(R.color.text_primary))
            textSize = 14f
            tag = "title"
        }

        tile.addView(iconWrap)
        tile.addView(title)
        container.addView(tile)
        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val card = item as? NavCard ?: return
        val container = viewHolder.view as LinearLayout
        val tile = container.findViewWithTag<LinearLayout>("tile")
        val icon = tile.findViewWithTag<ImageView>("icon")
        val title = tile.findViewWithTag<TextView>("title")
        val badge = tile.findViewWithTag<TextView>("badge")
        icon.setImageDrawable(ContextCompat.getDrawable(context, card.iconRes))
        title.text = card.title
        if (card.badgeCount > 0) {
            badge.text = if (card.badgeCount > 99) "99+" else card.badgeCount.toString()
            badge.visibility = View.VISIBLE
        } else {
            badge.visibility = View.GONE
        }
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {}
}
