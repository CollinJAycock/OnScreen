package tv.onscreen.android.ui.browse

import android.animation.ObjectAnimator
import android.content.Context
import android.graphics.Typeface
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.core.view.isVisible
import tv.onscreen.android.R
import tv.onscreen.android.data.model.LibraryLetter

/**
 * The A–Z jump column beside the library grid: one focusable letter per entry
 * of the server's letter index ("#" first, then A..Z, only letters that have
 * items). Selecting a letter asks the screen to jump; the rail itself knows
 * nothing about paging.
 *
 * D-pad inside the rail is handled here rather than left to focus search:
 * the library screen's BrowseFrameLayout sends every UP that bubbles to it to
 * the title bar, so a plain column would jump to the Sort orb from any
 * letter. UP/DOWN move between letters (UP from the first one still goes to
 * the title bar), RIGHT returns to the grid, LEFT stays put.
 */
class LetterRail(private val context: Context) {

    private val column = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER_HORIZONTAL
        clipChildren = false
        clipToPadding = false
    }

    /** The rail's root view, added by the fragment beside the grid. */
    val view: ScrollView = ScrollView(context).apply {
        isFocusable = false
        isVerticalScrollBarEnabled = false
        clipChildren = false
        clipToPadding = false
        visibility = View.GONE
        addView(column, ViewGroup.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
    }

    /** A letter was selected (OK / center). */
    var onPick: ((LibraryLetter) -> Unit)? = null

    /** RIGHT from a letter, or the rail went away while it held focus. */
    var onExit: (() -> Unit)? = null

    private var letters: List<LibraryLetter> = emptyList()

    /** Shown with at least one letter on it. */
    val isShowing: Boolean get() = letters.isNotEmpty() && view.isVisible

    /** Replace the letters; an empty list hides the rail. */
    fun setLetters(next: List<LibraryLetter>) {
        if (next == letters) return
        val hadFocus = view.hasFocus()
        letters = next
        column.removeAllViews()
        next.forEachIndexed { i, entry -> column.addView(letterView(i, entry)) }
        view.isVisible = next.isNotEmpty()
        // Rebuilt or hidden under the remote: give focus back to the grid
        // rather than leaving it on a removed view.
        if (hadFocus) onExit?.invoke()
    }

    /** Focus the letter whose items include listing row [position]
     *  (the grid's current card), or the first letter. */
    fun focusFor(position: Int): Boolean {
        if (!isShowing) return false
        val index = indexFor(letters, position)
        return column.getChildAt(index)?.requestFocus() == true
    }

    private fun letterView(index: Int, entry: LibraryLetter): TextView {
        val density = context.resources.displayMetrics.density
        return TextView(context).apply {
            text = entry.letter
            gravity = Gravity.CENTER
            textSize = 14f
            typeface = Typeface.DEFAULT_BOLD
            isFocusable = true
            isFocusableInTouchMode = true
            contentDescription = context.getString(R.string.letter_rail_jump_to, entry.letter)
            setTextColor(context.getColor(R.color.text_secondary))
            layoutParams = LinearLayout.LayoutParams((36 * density).toInt(), (24 * density).toInt()).apply {
                topMargin = (1 * density).toInt()
                bottomMargin = (1 * density).toInt()
            }
            setOnClickListener { onPick?.invoke(entry) }
            setOnFocusChangeListener { v, hasFocus ->
                val s = if (hasFocus) FOCUS_SCALE else 1.0f
                ObjectAnimator.ofFloat(v, View.SCALE_X, s).setDuration(ANIM_MS).start()
                ObjectAnimator.ofFloat(v, View.SCALE_Y, s).setDuration(ANIM_MS).start()
                (v as TextView).setTextColor(
                    context.getColor(if (hasFocus) R.color.text_primary else R.color.text_secondary),
                )
                if (hasFocus) v.setBackgroundResource(R.drawable.filter_chip_off_focused) else v.background = null
            }
            setOnKeyListener { _, keyCode, event ->
                if (event.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false
                when (keyCode) {
                    // First letter: let UP bubble on to the title bar.
                    KeyEvent.KEYCODE_DPAD_UP -> index > 0 && column.getChildAt(index - 1)?.requestFocus() == true
                    // Last letter: stay, rather than falling into the grid.
                    KeyEvent.KEYCODE_DPAD_DOWN -> {
                        column.getChildAt(index + 1)?.requestFocus()
                        true
                    }
                    KeyEvent.KEYCODE_DPAD_RIGHT -> {
                        onExit?.invoke()
                        true
                    }
                    KeyEvent.KEYCODE_DPAD_LEFT -> true
                    else -> false
                }
            }
        }
    }

    companion object {
        private const val FOCUS_SCALE = 1.15f
        private const val ANIM_MS = 120L

        /** Index of the letter whose run holds listing row [position]: the
         *  last letter starting at or before it. Offsets ascend in listing
         *  order (A..Z ascending, Z..A descending), so this is that letter
         *  for either direction. 0 when [position] precedes them all. */
        fun indexFor(letters: List<LibraryLetter>, position: Int): Int =
            letters.indexOfLast { it.offset <= position }.coerceAtLeast(0)
    }
}
