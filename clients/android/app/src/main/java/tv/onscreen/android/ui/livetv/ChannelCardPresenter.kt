package tv.onscreen.android.ui.livetv

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

/** Channel-list card. Wider than the standard 2:3 poster card —
 *  channels are landscape-shaped because the logo's typically
 *  wider than tall, and the "now playing" subtitle needs room. */
class ChannelCardPresenter(private val context: Context) : Presenter() {

    companion object {
        private const val CARD_WIDTH = 360
        private const val CARD_HEIGHT = 200
        private const val LOGO_HEIGHT = 110
    }

    override fun onCreateViewHolder(parent: ViewGroup): ViewHolder {
        val container = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            isFocusable = true
            isFocusableInTouchMode = true
            layoutParams = ViewGroup.LayoutParams(CARD_WIDTH, CARD_HEIGHT)
            setBackgroundColor(context.getColor(R.color.bg_elevated))
            foreground = context.getDrawable(R.drawable.card_focus_state)
        }

        val logoFrame = FrameLayout(context).apply {
            layoutParams = LinearLayout.LayoutParams(CARD_WIDTH, LOGO_HEIGHT)
            setBackgroundColor(Color.BLACK)
        }
        val logoView = ImageView(context).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            ).apply {
                val pad = (context.resources.displayMetrics.density * 16).toInt()
                marginStart = pad; marginEnd = pad
                topMargin = pad / 2; bottomMargin = pad / 2
            }
            scaleType = ImageView.ScaleType.FIT_CENTER
            tag = "logo"
        }
        val numberView = TextView(context).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
            ).apply { gravity = Gravity.START or Gravity.TOP }
            val pad = (context.resources.displayMetrics.density * 6).toInt()
            setPadding(pad * 2, pad, pad * 2, pad)
            setBackgroundColor(0xCC000000.toInt())
            setTextColor(Color.WHITE)
            textSize = 11f
            tag = "number"
        }
        logoFrame.addView(logoView)
        logoFrame.addView(numberView)

        val nameView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply {
                val pad = (context.resources.displayMetrics.density * 8).toInt()
                marginStart = pad; marginEnd = pad
                topMargin = pad
            }
            maxLines = 1
            ellipsize = android.text.TextUtils.TruncateAt.END
            setTextColor(context.getColor(R.color.text_primary))
            textSize = 14f
            tag = "name"
        }
        val nowView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply {
                val pad = (context.resources.displayMetrics.density * 8).toInt()
                marginStart = pad; marginEnd = pad
                topMargin = (context.resources.displayMetrics.density * 2).toInt()
            }
            maxLines = 1
            ellipsize = android.text.TextUtils.TruncateAt.END
            setTextColor(context.getColor(R.color.text_secondary))
            textSize = 12f
            tag = "now"
        }

        container.addView(logoFrame)
        container.addView(nameView)
        container.addView(nowView)
        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val entry = item as? ChannelEntry ?: return
        val container = viewHolder.view as LinearLayout
        val logo = container.findViewWithTag<ImageView>("logo")
        val number = container.findViewWithTag<TextView>("number")
        val name = container.findViewWithTag<TextView>("name")
        val now = container.findViewWithTag<TextView>("now")

        number.text = entry.channel.number
        name.text = entry.channel.name
        now.text = entry.current?.title ?: context.getString(R.string.live_tv_no_guide)

        val url = entry.channel.logo_url
        if (!url.isNullOrEmpty()) {
            logo.load(url) { crossfade(true) }
        } else {
            logo.setImageDrawable(null)
        }
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {
        val container = viewHolder.view as LinearLayout
        container.findViewWithTag<ImageView>("logo")?.setImageDrawable(null)
    }
}
