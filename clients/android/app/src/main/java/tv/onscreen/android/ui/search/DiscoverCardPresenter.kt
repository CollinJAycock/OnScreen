package tv.onscreen.android.ui.search

import android.animation.AnimatorSet
import android.animation.ObjectAnimator
import android.content.Context
import android.graphics.Color
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.leanback.widget.Presenter
import coil.load
import tv.onscreen.android.R
import tv.onscreen.android.data.model.DiscoverItem

/**
 * Presenter for TMDB-discover cards in the search results. Same
 * shape as CardPresenter (poster + title) but with two extras:
 *
 * 1. A status chip overlay in the top-right of the poster — the
 *    user reads it at a glance to know whether the title is
 *    already requested ("Pending"), available ("Available"), or
 *    free to request ("+ Request").
 *
 * 2. The poster URL points at a TMDB image CDN URL the server
 *    pre-rendered into the DiscoverItem. We don't go through
 *    /artwork/ here because the title isn't in our library yet.
 */
class DiscoverCardPresenter(private val context: Context) : Presenter() {

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

                val sx = ObjectAnimator.ofFloat(layout, View.SCALE_X, scale)
                val sy = ObjectAnimator.ofFloat(layout, View.SCALE_Y, scale)
                val ta = ObjectAnimator.ofFloat(titleView, View.ALPHA, if (hasFocus) 1f else 0f)
                AnimatorSet().apply {
                    playTogether(sx, sy, ta)
                    duration = ANIM_DURATION
                    start()
                }
                layout.elevation = if (hasFocus) 8f else 0f
            }
        }

        // Poster + status-chip overlay sit in a FrameLayout so the
        // chip can pin to the top-right of the poster.
        val posterFrame = FrameLayout(context).apply {
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, CARD_HEIGHT)
        }
        val poster = ImageView(context).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            )
            scaleType = ImageView.ScaleType.CENTER_CROP
            setBackgroundColor(context.getColor(R.color.bg_elevated))
            tag = "poster"
        }
        val chip = TextView(context).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
            ).apply {
                gravity = Gravity.TOP or Gravity.END
                topMargin = 12
                rightMargin = 12
            }
            setPadding(16, 6, 16, 6)
            textSize = 11f
            setBackgroundResource(R.drawable.badge_bg)
            setTextColor(Color.WHITE)
            tag = "chip"
        }
        posterFrame.addView(poster)
        posterFrame.addView(chip)

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

        container.addView(posterFrame)
        container.addView(titleView)

        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val container = viewHolder.view as LinearLayout
        val poster = container.findViewWithTag<ImageView>("poster")
        val titleView = container.findViewWithTag<TextView>("title")
        val chip = container.findViewWithTag<TextView>("chip")
        val data = item as? DiscoverItem ?: return

        titleView.text = if (data.year != null) "${data.title} (${data.year})" else data.title

        // Status chip: render the request state in plain English so
        // the user knows what selecting the card will do.
        val (chipText, chipVisible) = when {
            data.has_active_request -> {
                val s = data.active_request_status?.replaceFirstChar { it.uppercase() }
                    ?: "Pending"
                s to true
            }
            else -> "+ Request" to true
        }
        chip.text = chipText
        chip.visibility = if (chipVisible) View.VISIBLE else View.GONE

        if (!data.poster_url.isNullOrEmpty()) {
            poster.load(data.poster_url) {
                crossfade(true)
                placeholder(R.color.bg_elevated)
                error(R.color.bg_elevated)
            }
        } else {
            poster.setImageDrawable(null)
            poster.setBackgroundColor(context.getColor(R.color.bg_elevated))
        }
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {
        val container = viewHolder.view as LinearLayout
        val poster = container.findViewWithTag<ImageView>("poster")
        poster.setImageDrawable(null)
    }
}
