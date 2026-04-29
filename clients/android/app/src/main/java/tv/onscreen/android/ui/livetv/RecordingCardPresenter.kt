package tv.onscreen.android.ui.livetv

import android.content.Context
import android.text.TextUtils
import android.view.Gravity
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import androidx.leanback.widget.Presenter
import tv.onscreen.android.R
import tv.onscreen.android.data.model.Recording

/** Card for a recording row. Channels rarely have a poster for the
 *  recorded program (the EPG cover art is missing for most over-air
 *  feeds), so the card is text-first: title + S/E + channel + status. */
class RecordingCardPresenter(private val context: Context) : Presenter() {

    companion object {
        private const val CARD_WIDTH = 360
        private const val CARD_HEIGHT = 180
    }

    override fun onCreateViewHolder(parent: ViewGroup): ViewHolder {
        val container = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            isFocusable = true
            isFocusableInTouchMode = true
            layoutParams = ViewGroup.LayoutParams(CARD_WIDTH, CARD_HEIGHT)
            setBackgroundColor(context.getColor(R.color.bg_elevated))
            foreground = context.getDrawable(R.drawable.card_focus_state)
            val pad = (context.resources.displayMetrics.density * 14).toInt()
            setPadding(pad, pad, pad, pad)
        }

        val statusView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            )
            textSize = 10f
            setTextColor(context.getColor(R.color.text_secondary))
            isAllCaps = true
            tag = "status"
        }
        val titleView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply { topMargin = (context.resources.displayMetrics.density * 4).toInt() }
            maxLines = 2
            ellipsize = TextUtils.TruncateAt.END
            setTextColor(context.getColor(R.color.text_primary))
            textSize = 16f
            tag = "title"
        }
        val subtitleView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply { topMargin = (context.resources.displayMetrics.density * 2).toInt() }
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
            setTextColor(context.getColor(R.color.text_secondary))
            textSize = 12f
            tag = "subtitle"
        }
        val channelView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
                1f,
            ).apply { gravity = Gravity.BOTTOM }
            gravity = Gravity.BOTTOM
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
            setTextColor(context.getColor(R.color.text_secondary))
            textSize = 11f
            tag = "channel"
        }

        container.addView(statusView)
        container.addView(titleView)
        container.addView(subtitleView)
        container.addView(channelView)
        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val r = item as? Recording ?: return
        val container = viewHolder.view as LinearLayout
        val status = container.findViewWithTag<TextView>("status")
        val title = container.findViewWithTag<TextView>("title")
        val subtitle = container.findViewWithTag<TextView>("subtitle")
        val channel = container.findViewWithTag<TextView>("channel")

        status.text = r.status
        title.text = r.title
        val sub = buildString {
            if (r.season_num != null && r.episode_num != null) {
                append("S%02dE%02d".format(r.season_num, r.episode_num))
                if (!r.subtitle.isNullOrEmpty()) append(" · ${r.subtitle}")
            } else if (!r.subtitle.isNullOrEmpty()) {
                append(r.subtitle)
            }
        }
        subtitle.text = sub
        subtitle.visibility = if (sub.isEmpty()) android.view.View.GONE else android.view.View.VISIBLE
        channel.text = "${r.channel_number} · ${r.channel_name}"
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) { /* no resources to release */ }
}
