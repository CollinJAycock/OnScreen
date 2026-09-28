package tv.onscreen.android.ui.common

import android.app.AlertDialog
import android.app.Dialog
import android.os.Bundle
import android.text.InputFilter
import android.util.TypedValue
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.View
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.core.view.isVisible
import androidx.fragment.app.DialogFragment
import androidx.fragment.app.FragmentManager
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import tv.onscreen.android.R

/**
 * "Report a problem" — a D-pad dialog that files a report
 * (POST /api/v1/items/{id}/issues) and shows the caller's earlier reports on
 * the item (GET same path), so a kind that is already open is disabled and
 * the dialog says "You reported … 2 days ago" instead of inviting a duplicate.
 *
 * Step 1 lists the five kinds as focusable buttons; step 2 takes an optional
 * note (the same EditText-in-a-dialog pattern Settings uses for the
 * ListenBrainz token) with "Send report" focused, so OK sends straight away
 * and UP reaches the note. BACK on step 2 returns to the list.
 *
 * A DialogFragment (not a bare AlertDialog) so it is torn down with its host
 * fragment — MainActivity's screen-off home reset replaces fragments under
 * whatever is on screen — and its state survives recreation.
 */
@AndroidEntryPoint
class ReportProblemDialog : DialogFragment() {

    private lateinit var viewModel: ReportProblemViewModel

    private var historyView: TextView? = null
    private var kindsPanel: LinearLayout? = null
    private var notePanel: View? = null
    private var noteKindView: TextView? = null
    private var noteInput: EditText? = null
    private var sendButton: Button? = null
    private var sentView: View? = null
    private val kindButtons = mutableMapOf<String, Button>()

    /** Which panel was last rendered — focus moves only when it changes. */
    private var renderedPhase: Phase? = null
    private var collectJob: Job? = null

    private enum class Phase { KINDS, NOTE, SENT }

    // The body is inflated against the dialog THEME's context (PlayerDialog), which
    // the fragment's own layoutInflater doesn't carry — and calling it from
    // onCreateDialog re-enters onGetLayoutInflater.
    @android.annotation.SuppressLint("UseGetLayoutInflater")
    override fun onCreateDialog(savedInstanceState: Bundle?): Dialog {
        viewModel = ViewModelProvider(this)[ReportProblemViewModel::class.java]
        val args = requireArguments()
        viewModel.start(args.getString(ARG_ITEM_ID).orEmpty(), args.getString(ARG_FILE_ID))

        val builder = AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
        val content = LayoutInflater.from(builder.context)
            .inflate(R.layout.dialog_report_problem, null, false)
        bindViews(content, args.getString(ARG_LABEL))

        val dialog = builder
            .setTitle(R.string.report_problem)
            .setView(content)
            // Cancel / Close. Null listener: an AlertDialog button dismisses.
            .setNegativeButton(R.string.cancel, null)
            .create()

        dialog.setOnShowListener {
            renderedPhase = null
            render(viewModel.uiState.value)
        }
        // BACK on the note step goes back a step instead of closing. The
        // manifest keeps legacy back dispatch (enableOnBackInvokedCallback=
        // false), so KEYCODE_BACK reaches this listener on Android 16 too.
        dialog.setOnKeyListener { _, keyCode, event ->
            val s = viewModel.uiState.value
            if (keyCode != KeyEvent.KEYCODE_BACK || s.selectedKind == null || s.sent) {
                return@setOnKeyListener false
            }
            if (event.action == KeyEvent.ACTION_UP && !event.isCanceled) viewModel.backToKinds()
            true
        }

        // onCreateDialog runs again if the fragment's view is re-created;
        // keep exactly one collector.
        collectJob?.cancel()
        collectJob = lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                viewModel.uiState.collect { render(it) }
            }
        }
        return dialog
    }

    private fun bindViews(root: View, label: String?) {
        root.findViewById<TextView>(R.id.report_subject).apply {
            text = label.orEmpty()
            isVisible = !label.isNullOrBlank()
        }
        historyView = root.findViewById(R.id.report_history)
        kindsPanel = root.findViewById(R.id.report_kinds)
        notePanel = root.findViewById(R.id.report_note)
        noteKindView = root.findViewById(R.id.report_note_kind)
        sentView = root.findViewById(R.id.report_sent)
        noteInput = root.findViewById<EditText>(R.id.report_note_input).apply {
            filters = arrayOf(InputFilter.LengthFilter(ReportProblem.NOTE_MAX))
            // Wrap visually while keeping the IME's Done action (a multi-line
            // input type would turn Done into a newline).
            setHorizontallyScrolling(false)
            maxLines = 3
            setOnEditorActionListener { _, actionId, _ ->
                // Done hides the keyboard and lands on Send — it does not send
                // by itself, so a stray ENTER never files a report.
                if (actionId == EditorInfo.IME_ACTION_DONE) sendButton?.requestFocus()
                false
            }
        }
        sendButton = root.findViewById<Button>(R.id.report_send).apply {
            setOnClickListener { viewModel.submit(noteInput?.text?.toString()) }
        }
        root.findViewById<Button>(R.id.report_back).setOnClickListener { viewModel.backToKinds() }

        val panel = kindsPanel ?: return
        kindButtons.clear()
        val ctx = root.context
        val height = TypedValue.applyDimension(
            TypedValue.COMPLEX_UNIT_DIP, 48f, ctx.resources.displayMetrics,
        ).toInt()
        val margin = TypedValue.applyDimension(
            TypedValue.COMPLEX_UNIT_DIP, 5f, ctx.resources.displayMetrics,
        ).toInt()
        val padH = TypedValue.applyDimension(
            TypedValue.COMPLEX_UNIT_DIP, 20f, ctx.resources.displayMetrics,
        ).toInt()
        ReportProblem.KIND_OPTIONS.forEach { opt ->
            val btn = Button(ctx, null, android.R.attr.borderlessButtonStyle).apply {
                text = opt.label
                isAllCaps = false
                textSize = 15f
                setTextColor(ctx.getColor(R.color.text_primary))
                background = ctx.getDrawable(R.drawable.btn_pill_secondary)
                gravity = android.view.Gravity.CENTER_VERTICAL or android.view.Gravity.START
                setPadding(padH, 0, padH, 0)
                isFocusable = true
                setOnClickListener { viewModel.pickKind(opt.value) }
            }
            val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, height)
            lp.topMargin = margin
            lp.bottomMargin = margin
            panel.addView(btn, lp)
            kindButtons[opt.value] = btn
        }
    }

    private fun render(state: ReportProblemUiState) {
        val dialog = dialog as? AlertDialog
        val now = System.currentTimeMillis()

        historyView?.apply {
            val shown = ReportProblem.visibleHistory(state.history, now)
            when {
                state.loadingHistory -> {
                    text = getString(R.string.report_problem_checking)
                    isVisible = true
                }
                shown.isNotEmpty() -> {
                    text = shown.joinToString("\n") { ReportProblem.describe(it, now) }
                    isVisible = true
                }
                else -> isVisible = false
            }
        }

        val taken = ReportProblem.openKinds(state.history)
        kindButtons.forEach { (kind, btn) ->
            val open = kind in taken
            val label = ReportProblem.kindLabel(kind)
            btn.text = if (open) getString(R.string.report_problem_already_reported, label) else label
            btn.isEnabled = !open
            btn.isFocusable = !open
            btn.alpha = if (open) 0.45f else 1f
        }

        val phase = when {
            state.sent -> Phase.SENT
            state.selectedKind != null -> Phase.NOTE
            else -> Phase.KINDS
        }
        kindsPanel?.isVisible = phase == Phase.KINDS
        notePanel?.isVisible = phase == Phase.NOTE
        sentView?.isVisible = phase == Phase.SENT
        state.selectedKind?.let { noteKindView?.text = ReportProblem.kindLabel(it) }
        sendButton?.apply {
            isEnabled = !state.submitting
            text = getString(if (state.submitting) R.string.report_problem_sending else R.string.report_problem_send)
        }
        dialog?.getButton(AlertDialog.BUTTON_NEGATIVE)?.setText(
            if (phase == Phase.SENT) R.string.report_problem_close else R.string.cancel,
        )

        if (phase != renderedPhase) {
            renderedPhase = phase
            if (phase == Phase.KINDS) noteInput?.setText("")
            focusFor(phase, dialog)
        } else if (phase == Phase.KINDS && kindButtons.values.none { it.hasFocus() } &&
            dialog?.getButton(AlertDialog.BUTTON_NEGATIVE)?.hasFocus() != true
        ) {
            // The history just disabled the focused kind — don't strand focus.
            focusFor(phase, dialog)
        }

        state.toast?.let { msg ->
            context?.let { Toast.makeText(it, msg, Toast.LENGTH_LONG).show() }
            viewModel.toastShown()
        }
    }

    private fun focusFor(phase: Phase, dialog: AlertDialog?) {
        val negative = dialog?.getButton(AlertDialog.BUTTON_NEGATIVE)
        val target: View? = when (phase) {
            Phase.KINDS -> kindButtons.values.firstOrNull { it.isEnabled } ?: negative
            Phase.NOTE -> sendButton
            Phase.SENT -> negative
        }
        target?.post { target.requestFocus() }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        historyView = null
        kindsPanel = null
        notePanel = null
        noteKindView = null
        noteInput = null
        sendButton = null
        sentView = null
        kindButtons.clear()
    }

    companion object {
        const val TAG = "report_problem"
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_FILE_ID = "file_id"
        private const val ARG_LABEL = "label"

        /** Show the dialog for [itemId] unless one is already up. [fileId] is
         *  the media file the report concerns, when known; [label] is shown
         *  under the title (e.g. the item's title). */
        fun show(fm: FragmentManager, itemId: String, fileId: String?, label: String?) {
            if (fm.isStateSaved || fm.findFragmentByTag(TAG) != null) return
            ReportProblemDialog().apply {
                arguments = Bundle().apply {
                    putString(ARG_ITEM_ID, itemId)
                    putString(ARG_FILE_ID, fileId)
                    putString(ARG_LABEL, label)
                }
            }.show(fm, TAG)
        }
    }
}
