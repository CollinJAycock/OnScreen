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
 * Toggleable type-filter chip for the search row.
 *
 * Renders a pill-shaped TextView with a "✓ Movies" / "Movies" label.
 * Three visual states:
 *   - off, unfocused: dim outline, dim text
 *   - on, unfocused: accent background, white text
 *   - any state, focused: accent border + slight scale lift, so the
 *     active card stands out against neighboring chips
 *
 * The presenter doesn't own the toggle state — it just renders the
 * model. Click handling lives in SearchFragment, which calls back
 * into [SearchViewModel.toggleFilter] and lets the persisted state
 * flow back through visibleResults.
 */
class FilterChipPresenter(private val context: Context) : Presenter() {

    /** What the chip represents and whether it's on. Equals/hashCode
     *  so ArrayObjectAdapter.replace() correctly diffs by type. */
    data class Chip(
        val type: SearchViewModel.FilterType,
        val label: String,
        val checked: Boolean,
    )

    companion object {
        private const val FOCUS_SCALE = 1.06f
        private const val ANIM_MS = 150L
    }

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
            // Re-render via the existing tag so the focus border
            // toggles without needing the Chip model.
            val tv = (v as ViewGroup).findViewWithTag<TextView>("label") ?: return@setOnFocusChangeListener
            val chip = v.tag as? Chip ?: return@setOnFocusChangeListener
            applyStyle(tv, chip, hasFocus)
        }

        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val container = viewHolder.view as LinearLayout
        val text = container.findViewWithTag<TextView>("label") ?: return
        val chip = item as? Chip ?: return
        container.tag = chip
        text.text = if (chip.checked) "✓ ${chip.label}" else chip.label
        applyStyle(text, chip, container.isFocused)
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {
        viewHolder.view.tag = null
    }

    private fun applyStyle(text: TextView, chip: Chip, focused: Boolean) {
        val bg = when {
            chip.checked && focused -> R.drawable.filter_chip_on_focused
            chip.checked -> R.drawable.filter_chip_on
            focused -> R.drawable.filter_chip_off_focused
            else -> R.drawable.filter_chip_off
        }
        text.setBackgroundResource(bg)
        text.setTextColor(
            context.getColor(
                if (chip.checked) R.color.text_primary else R.color.text_secondary,
            ),
        )
    }
}
