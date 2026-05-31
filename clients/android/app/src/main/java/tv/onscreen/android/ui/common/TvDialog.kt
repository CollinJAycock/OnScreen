package tv.onscreen.android.ui.common

import android.app.AlertDialog

/**
 * Fire TV / D-pad fix for button-only [AlertDialog]s.
 *
 * A platform AlertDialog with ONLY buttons — no list (setItems /
 * setSingleChoiceItems) and no focusable setView content — opens with nothing
 * focused on a TV. With no touchscreen the D-pad can't reach the button bar, so
 * the dialog is dead: only Back dismisses it. Users report this as "the Log out
 * / Resume / Request button does nothing."
 *
 * Call between create() and show() on any button-only dialog:
 * ```
 * AlertDialog.Builder(ctx, R.style.PlayerDialog)
 *     .setTitle(...)
 *     .setMessage(...)
 *     .setPositiveButton(...) { ... }
 *     .setNegativeButton(...) { ... }
 *     .create()
 *     .focusableOnTv()
 *     .show()
 * ```
 *
 * Focuses the positive button (falling back to negative, then neutral) once the
 * views are laid out; from there LEFT/RIGHT crosses the button bar. Harmless on
 * touch devices, where focus isn't required to tap. List- and EditText-based
 * dialogs don't need this — that content already claims initial focus.
 */
fun AlertDialog.focusableOnTv(): AlertDialog = apply {
    setOnShowListener {
        (getButton(AlertDialog.BUTTON_POSITIVE)
            ?: getButton(AlertDialog.BUTTON_NEGATIVE)
            ?: getButton(AlertDialog.BUTTON_NEUTRAL))?.requestFocus()
    }
}
