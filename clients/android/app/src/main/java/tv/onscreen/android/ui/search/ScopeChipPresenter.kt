package tv.onscreen.android.ui.search

import android.animation.ObjectAnimator
import android.content.Context
import android.graphics.Color
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import androidx.leanback.widget.Presenter
import tv.onscreen.android.R

/**
 * A single focusable pill that opens the library-scope picker.
 *
 * The scope menu was previously reachable ONLY via KEYCODE_MENU / BUTTON_Y, which
 * cheap Fire TV remotes don't have — so on those remotes there was no way to
 * narrow the search to one library. This renders an on-screen affordance (in its
 * own row above the filter chips) so the picker is reachable with just the D-pad.
 */
class ScopeChipPresenter(private val context: Context) : Presenter() {

    data class ScopeChip(val label: String)

    override fun onCreateViewHolder(parent: ViewGroup): ViewHolder {
        val container = LinearLayout(context).apply {
            isFocusable = true
            isFocusableInTouchMode = true
            setBackgroundColor(Color.TRANSPARENT)
            clipChildren = false
            clipToPadding = false
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            )
        }
        val text = TextView(context).apply {
            gravity = Gravity.CENTER
            textSize = 14f
            setPadding(36, 16, 36, 16)
            tag = "label"
        }
        container.addView(text)
        container.setOnFocusChangeListener { v, hasFocus ->
            val s = if (hasFocus) FOCUS_SCALE else 1.0f
            ObjectAnimator.ofFloat(v, View.SCALE_X, s).setDuration(ANIM_MS).start()
            ObjectAnimator.ofFloat(v, View.SCALE_Y, s).setDuration(ANIM_MS).start()
            v.elevation = if (hasFocus) 8f else 0f
            (v as ViewGroup).findViewWithTag<TextView>("label")?.setBackgroundResource(
                if (hasFocus) R.drawable.filter_chip_off_focused else R.drawable.filter_chip_off,
            )
        }
        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val container = viewHolder.view as LinearLayout
        val text = container.findViewWithTag<TextView>("label") ?: return
        val chip = item as? ScopeChip ?: return
        text.text = chip.label
        text.setBackgroundResource(
            if (container.isFocused) R.drawable.filter_chip_off_focused else R.drawable.filter_chip_off,
        )
        text.setTextColor(context.getColor(R.color.text_primary))
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {}

    companion object {
        private const val FOCUS_SCALE = 1.06f
        private const val ANIM_MS = 150L
    }
}
