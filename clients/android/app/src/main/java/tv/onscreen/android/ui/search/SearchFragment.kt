package tv.onscreen.android.ui.search

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.os.Bundle
import android.os.SystemClock
import android.speech.RecognizerIntent
import android.view.KeyEvent
import android.view.View
import android.widget.Toast
import androidx.leanback.app.SearchSupportFragment
import androidx.leanback.widget.*
import androidx.leanback.widget.FocusHighlight
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.SearchResult
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.KeyEventHandler
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.common.focusableOnTv
import javax.inject.Inject

/**
 * Search screen — TV equivalent of the web `/search` page.
 *
 * Two rows render under the search field:
 *   - "In your library": local matches (SearchResult), routes via
 *     Navigator on click.
 * Search covers the user's OWN library only. The TMDB-backed request
 * row was removed on 2026-08-06: the Amazon Appstore read "titles you
 * do not own, with a button to have them acquired" as facilitating
 * third-party acquisition, and rejected three builds over it. The
 * request flow remains on the web app.
 *
 * Library-scoped searches (the Y / menu key opens a picker) narrow
 * the results to that one library.
 */
@AndroidEntryPoint
class SearchFragment : SearchSupportFragment(), SearchSupportFragment.SearchResultProvider, KeyEventHandler {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: SearchViewModel
    private lateinit var rowsAdapter: ArrayObjectAdapter
    private var serverUrl: String = ""
    // The last query the user typed, so rebuildRows can tell "nothing searched
    // yet" from "searched and found nothing" and show a No-results state.
    private var lastQuery: String = ""

    /** Row index to re-focus after returning from a detail screen. The
     *  view (and rowsAdapter) is destroyed on navigation; the rebuilt rows
     *  claim no focus on their own, so BACK used to land the user on a
     *  screen where the D-pad was dead until they hunted for focus. */
    private var pendingFocusRow: Int = -1

    /** The result opened, so focus comes back to that card rather than to
     *  the first one in its row. */
    private var pendingFocusItemId: String? = null

    /** How long (uptime) a restore waits for the opened card's row to come
     *  back; set when the view is rebuilt on the way back. */
    private var pendingFocusUntil = 0L

    /** Persistent top-row adapters, mutated in place across rebuilds so
     *  chip focus survives a filter toggle. Null until first build; reset
     *  in onDestroyView with the rows they live in. */
    private var scopeAdapter: ArrayObjectAdapter? = null
    private var chipAdapter: ArrayObjectAdapter? = null

    /** Voice input is offered here (see [VoiceSearch]): the orb shows and
     *  starts the recognizer. Decided once, in onCreate. */
    private var voiceEnabled = false

    /** When the recognizer was started, to tell one that came straight
     *  back (never listened) from a viewer backing out. */
    private var speechStartedAt = 0L

    /** Leanback starts recognition by itself once, when the screen first
     *  opens. That start wasn't the viewer's doing, so if it fails the orb
     *  goes away without a message about it. */
    private var autoStartPending = false
    private var speechFromAutoStart = false

    /** The query on screen when voice started. Leanback blanks the field
     *  before calling us and its text watcher pushes that blank through
     *  as a search, so a cancelled recognition would otherwise come back
     *  to an empty field with the results gone. */
    private var queryBeforeVoice = ""

    /** When focus last left the search field. A BACK that closes the
     *  keyboard also moves focus off the field (Leanback, before the key
     *  reaches us); see [onActivityKeyEvent]. */
    private var editorFocusLostAt = 0L
    private val editorFocusWatcher = android.view.ViewTreeObserver.OnGlobalFocusChangeListener { old, _ ->
        if (old?.id == androidx.leanback.R.id.lb_search_text_editor) editorFocusLostAt = SystemClock.uptimeMillis()
    }

    private fun scopeLabel(): String =
        "${getString(R.string.search_in)}: ${viewModel.scope.value?.name ?: getString(R.string.all_libraries)}"

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setSearchResultProvider(this)
        voiceEnabled = !VoiceSearch.provenUnavailable &&
            VoiceSearch.recognizerInstalled(requireContext().packageManager)
        autoStartPending = savedInstanceState == null
        // A callback is set either way. Without one Leanback makes its own
        // SpeechRecognizer and asks for RECORD_AUDIO, which the app doesn't
        // hold: the orb would be dead again. It also starts recognition by
        // itself when the screen opens, so where voice isn't offered the
        // callback only puts the search bar back to its typing state.
        @Suppress("DEPRECATION")
        setSpeechRecognitionCallback {
            val auto = autoStartPending
            autoStartPending = false
            // Leanback's delayed auto-start isn't cancelled when Search is
            // left within its 300 ms. A screen that's gone has nothing to
            // start, and its failure says nothing about the recognizer.
            if (!isAdded || isRemoving || view == null) return@setSpeechRecognitionCallback
            if (!voiceEnabled) {
                endRecognitionUi()
                return@setSpeechRecognitionCallback
            }
            queryBeforeVoice = lastQuery
            speechFromAutoStart = auto
            try {
                // RecognizerIntent: the recognizer app owns the mic, so no
                // RECORD_AUDIO here. Deprecated API, but the supported
                // no-permission path on Leanback 1.0.0.
                speechStartedAt = SystemClock.elapsedRealtime()
                startActivityForResult(VoiceSearch.recognizeIntent(), REQUEST_SPEECH)
            } catch (e: android.content.ActivityNotFoundException) {
                // Resolved but wouldn't start: never a silent orb.
                voiceUnavailable(announce = !auto)
            } catch (e: SecurityException) {
                voiceUnavailable(announce = !auto)
            } catch (e: Exception) {
                // Anything else is about this screen's state, not the
                // recognizer: back to typing, the orb stays.
                endRecognitionUi()
            }
        }
    }

    /** The search bar out of its "listening" state (hint back, orb idle),
     *  with the query that was there before voice started put back. */
    private fun endRecognitionUi() {
        val restore = queryBeforeVoice
        queryBeforeVoice = ""
        if (restore.isNotBlank()) {
            // Through the fragment, so the bar's text, its query and the
            // results all come back (SearchBar.setSearchQuery also stops
            // recognition). Posted: Leanback marks the bar "recognizing"
            // only after our callback returns.
            view?.post { if (isAdded) setSearchQuery(restore, false) }
            return
        }
        val bar = view?.findViewById<SearchBar>(androidx.leanback.R.id.lb_search_bar) ?: return
        bar.post { bar.stopRecognition() }
    }

    /** This device's recognizer doesn't work: back to typing, the orb gone
     *  for good (the rest of the process), and, when the viewer pressed it,
     *  a message saying why. */
    private fun voiceUnavailable(announce: Boolean) {
        VoiceSearch.provenUnavailable = true
        voiceEnabled = false
        endRecognitionUi()
        hideOrb()
        if (announce) context?.let {
            Toast.makeText(it, R.string.voice_search_unavailable, Toast.LENGTH_LONG).show()
        }
    }

    /** Takes the orb off screen. If it held focus, the search field takes
     *  it (and with it the keyboard), so the press still leads somewhere. */
    private fun hideOrb() {
        val root = view ?: return
        val orb = root.findViewById<View>(androidx.leanback.R.id.lb_search_bar_speech_orb) ?: return
        val hadFocus = orb.hasFocus()
        orb.visibility = View.GONE
        if (hadFocus) root.findViewById<View>(androidx.leanback.R.id.lb_search_text_editor)?.requestFocus()
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == REQUEST_SPEECH) {
            val outcome = VoiceSearch.outcome(
                resultCode = resultCode,
                matches = data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS),
                elapsedMs = SystemClock.elapsedRealtime() - speechStartedAt,
            )
            // Anything but a query puts the bar back to typing; the
            // outcomes that would look like a dead orb say why.
            when (outcome) {
                is VoiceSearch.Outcome.Query -> {
                    queryBeforeVoice = ""
                    setSearchQuery(outcome.text, true)
                }
                VoiceSearch.Outcome.Unavailable -> voiceUnavailable(announce = !speechFromAutoStart)
                VoiceSearch.Outcome.NothingHeard -> {
                    endRecognitionUi()
                    context?.let {
                        Toast.makeText(it, R.string.voice_search_nothing_heard, Toast.LENGTH_LONG).show()
                    }
                }
                VoiceSearch.Outcome.Failed -> {
                    endRecognitionUi()
                    context?.let {
                        Toast.makeText(it, R.string.voice_search_failed, Toast.LENGTH_LONG).show()
                    }
                }
                VoiceSearch.Outcome.Cancelled -> endRecognitionUi()
            }
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    /**
     * BACK from the search field (keyboard up or not) leaves the field for
     * the results and nothing else; BACK from the results leaves Search.
     * The Fire TV keyboard hides itself on the key-down but lets the key
     * through, so one press both closed the keyboard and left Search for
     * Home. By the time the key reaches us, Leanback's pre-IME handling has
     * already moved focus off the field, so "the field had focus at the
     * moment of this press" is the test. The key-down is consumed here; with
     * no tracked key-down, the key-up doesn't go back either.
     */
    override fun onActivityKeyEvent(event: KeyEvent): Boolean {
        // MENU / Y opens the library picker from anywhere on the screen. It
        // used to be an OnKeyListener on the root view, which only hears keys
        // while the root itself has focus, and on Search a child always does.
        if ((event.keyCode == KeyEvent.KEYCODE_MENU || event.keyCode == KeyEvent.KEYCODE_BUTTON_Y) &&
            event.repeatCount == 0) {
            showScopeMenu()
            return true
        }
        if (event.keyCode != KeyEvent.KEYCODE_BACK || event.repeatCount != 0) return false
        val root = view ?: return false
        val editor = root.findViewById<View>(androidx.leanback.R.id.lb_search_text_editor) ?: return false
        val editorFocused = editor.hasFocus()
        if (!editorFocused && editorFocusLostAt < event.downTime) return false
        val imm = requireContext().getSystemService(android.view.inputmethod.InputMethodManager::class.java)
        imm?.hideSoftInputFromWindow(editor.windowToken, 0)
        // Still on the field: move to the results, so the next BACK leaves.
        if (editor.hasFocus()) rowsSupportFragment?.view?.requestFocus()
        return true
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        // No working recognizer on this device: no orb to press. The
        // remote's own voice button (Alexa on Fire TV) is the system's
        // either way.
        if (!voiceEnabled) hideOrb()
        if (pendingFocusRow >= 0) pendingFocusUntil = SystemClock.uptimeMillis() + RESTORE_WAIT_MS
        // On the window's observer, which outlives this view: one added to the
        // view's own is merged into the window's on attach, and by
        // onDestroyView the view is detached, so removing it there missed and
        // every Search open leaked a listener (and this fragment with it).
        requireActivity().window.decorView.viewTreeObserver.addOnGlobalFocusChangeListener(editorFocusWatcher)
        viewModel = ViewModelProvider(this)[SearchViewModel::class.java]
        rowsAdapter = ArrayObjectAdapter(ListRowPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
            shadowEnabled = false
            selectEffectEnabled = false
        })
        // Fresh rowsAdapter → the persistent top-row adapters belong to the
        // OLD view's rows and must be rebuilt into this one.
        scopeAdapter = null
        chipAdapter = null

        // Re-render whenever any of the input streams change.
        // collectLatest keeps the most recent state; rebuildRows is
        // idempotent (clears + repopulates the adapter).
        //
        // serverUrl is read inside each collector so the read completes
        // BEFORE the first rebuildRows. The previous structure launched
        // the prefs read as a separate coroutine that raced against the
        // state collectors — same race that made hub posters render as
        // flat colour tiles on Fire TV.
        // ONE combined collector, ONE rebuild per state change. This used
        // to be six separate collectors, and on every view recreation each
        // replayed its current value — six full rebuilds back to back, each
        // removing and re-adding the result rows. Leanback's own
        // focus-to-results handoff (and our post-detail restore) kept
        // firing into that churn and losing: focus fell through to the
        // search frame and the D-pad went dead.
        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""
            kotlinx.coroutines.flow.combine(
                viewModel.visibleResults,
                viewModel.searchError,
                viewModel.scope,
                viewModel.filters,
            ) { _ -> Unit }.collectLatest { rebuildRows() }
        }

        // Focus backstop. Leanback's SearchSupportFragment hands focus to
        // the results on submit / resume via its internal focusOnResults(),
        // but when that runs while our rows are still (re)binding — six
        // collectors replay on every view recreation — the request finds no
        // laid-out child and focus falls back to lb_search_frame, a plain
        // full-screen FrameLayout. Nothing ever moves it again: no highlight,
        // D-pad dead. Diagnosed live on the Firestick (dumpsys showed the
        // frame focused at 0,0-1920,1080 after submit AND after a detail
        // round-trip). Whenever the frame ends up holding focus, hand it to
        // the rows once they exist.
        view.findViewById<View>(androidx.leanback.R.id.lb_search_frame)
            ?.setOnFocusChangeListener { _, hasFocus ->
                if (hasFocus) rescueFocusFromFrame(0)
            }

        setOnItemViewClickedListener { _, item, _, _ ->
            when (item) {
                is SearchResult -> {
                    // Remember where we were BEFORE the view is torn down.
                    pendingFocusRow = rowsSupportFragment?.selectedPosition ?: -1
                    pendingFocusItemId = item.id
                    Navigator.open(parentFragmentManager, item.id, item.type, 0)
                }
                is FilterChipPresenter.Chip ->
                    viewModel.toggleFilter(item.type)
                is ScopeChipPresenter.ScopeChip ->
                    showScopeMenu()
            }
        }
    }

    override fun onDestroyView() {
        activity?.window?.decorView?.viewTreeObserver?.let {
            if (it.isAlive) it.removeOnGlobalFocusChangeListener(editorFocusWatcher)
        }
        super.onDestroyView()
    }

    /** Retry loop for the frame backstop: the rows may still be binding
     *  when the frame grabs focus, so poll briefly until requestFocus on
     *  the rows sticks (or something else legitimately takes focus). */
    private fun rescueFocusFromFrame(attempt: Int) {
        if (attempt > 6) return
        view?.postDelayed({
            if (!isAdded) return@postDelayed
            val focused = view?.findFocus()
            if (focused != null && focused.id != androidx.leanback.R.id.lb_search_frame) {
                return@postDelayed // something real has focus — done
            }
            val rowsView = rowsSupportFragment?.view
            if (rowsAdapter.size() == 0 || rowsView?.requestFocus() != true) {
                rescueFocusFromFrame(attempt + 1)
            }
        }, 150)
    }

    /** Re-apply [pendingFocusRow] once rows exist again after a detail
     *  round-trip. Called at the end of rebuildRows. */
    private fun restoreFocusIfPending() {
        if (pendingFocusRow < 0 || rowsAdapter.size() == 0) return
        val target = pendingFocusRow.coerceAtMost(rowsAdapter.size() - 1)
        val itemId = pendingFocusItemId
        // The opened card's place in the rebuilt row, if it is there.
        val itemIndex = ((rowsAdapter.get(target) as? ListRow)?.adapter as? ArrayObjectAdapter)
            ?.let { a -> (0 until a.size()).firstOrNull { (a.get(it) as? SearchResult)?.id == itemId } }
            ?: -1
        // The scope and filter rows are always there, so a rebuild can come
        // before the results do (the query replayed on the way back can
        // clear them for a moment). Restoring then spent the target on a
        // row without the card, the results landed after, and focus fell to
        // the first card or to nothing. Wait for a rebuild that holds the
        // card, for a while: past that it has gone (the library changed),
        // and the row alone is restored.
        if (itemId != null && itemIndex < 0 && SystemClock.uptimeMillis() < pendingFocusUntil) return
        pendingFocusRow = -1
        pendingFocusItemId = null
        val rowsFrag = rowsSupportFragment ?: return
        // The selection is a pending op Leanback honors at layout, but
        // requestFocus() needs an ALREADY-laid-out focusable child — called
        // synchronously here the rows exist only as adapter items, so the
        // request found nothing and focus stayed lost (the first fix's
        // mistake). Defer past the layout pass.
        if (itemIndex >= 0) {
            rowsFrag.setSelectedPosition(target, false, ListRowPresenter.SelectItemViewHolderTask(itemIndex))
        } else {
            rowsFrag.setSelectedPosition(target, false)
        }
        rowsFrag.view?.postDelayed({
            if (isAdded) rowsFrag.view?.requestFocus()
        }, 200)
    }

    override fun getResultsAdapter(): ObjectAdapter = rowsAdapter

    override fun onQueryTextChange(query: String): Boolean {
        lastQuery = query
        viewModel.search(query)
        return true
    }

    override fun onQueryTextSubmit(query: String): Boolean {
        lastQuery = query
        // Asked for: searched again even when it's the query on screen.
        viewModel.search(query, force = true)
        return true
    }

    private fun rebuildRows() {
        val library = viewModel.visibleResults.value
        val rawCount = viewModel.results.value.size
        val searchError = viewModel.searchError.value
        val filters = viewModel.filters.value

        // The scope + chip rows are built ONCE and mutated in place from
        // then on. rebuildRows used to rowsAdapter.clear() and recreate
        // everything, which destroyed the focused chip on every toggle —
        // the user's D-pad position reset to the top of the screen per
        // click. Data-class equality makes replace() rebind only the chip
        // whose checked state actually changed, preserving focus; the
        // result rows below are still rebuilt wholesale (their content
        // legitimately changed).
        // The scope row is built first, before the libraries arrive. It used
        // to wait for them and was then inserted at row 0 on the first
        // results, mid-typing. That shifted the grid's selected row from 0
        // to 1, and Leanback hides the search bar whenever the selection is
        // past row 0: the field lost focus and the keyboard closed under the
        // viewer's typing, focus jumped to the Movies chip, and this row was
        // drawn under the bar.
        if (scopeAdapter == null) {
            val a = ArrayObjectAdapter(ScopeChipPresenter(requireContext()))
            a.add(ScopeChipPresenter.ScopeChip(scopeLabel()))
            scopeAdapter = a
            rowsAdapter.add(0, ListRow(HeaderItem(SCOPE_HEADER_ID, getString(R.string.search_in)), a))
        } else {
            scopeAdapter?.replace(0, ScopeChipPresenter.ScopeChip(scopeLabel()))
        }

        val chips = listOf(
            FilterChipPresenter.Chip(SearchViewModel.FilterType.MOVIE,
                getString(R.string.filter_movies), filters.movie),
            FilterChipPresenter.Chip(SearchViewModel.FilterType.SHOW,
                getString(R.string.filter_shows), filters.show),
            FilterChipPresenter.Chip(SearchViewModel.FilterType.EPISODE,
                getString(R.string.filter_episodes), filters.episode),
            FilterChipPresenter.Chip(SearchViewModel.FilterType.TRACK,
                getString(R.string.filter_tracks), filters.track),
        )
        val existingChips = chipAdapter
        if (existingChips == null) {
            val a = ArrayObjectAdapter(FilterChipPresenter(requireContext()))
            chips.forEach { a.add(it) }
            chipAdapter = a
            rowsAdapter.add(ListRow(HeaderItem(FILTER_HEADER_ID, getString(R.string.filter_label)), a))
        } else {
            chips.forEachIndexed { i, c ->
                if (existingChips.get(i) != c) existingChips.replace(i, c)
            }
        }

        // Rebuild only the rows below the persistent scope/chip rows.
        val stableRows = (if (scopeAdapter != null) 1 else 0) + 1
        if (rowsAdapter.size() > stableRows) {
            rowsAdapter.removeItems(stableRows, rowsAdapter.size() - stableRows)
        }

        if (library.isNotEmpty()) {
            val cardPresenter = CardPresenter(requireContext(), serverUrl)
            val listAdapter = ArrayObjectAdapter(cardPresenter)
            library.forEach { listAdapter.add(it) }
            val label = viewModel.scope.value?.name
                ?: getString(R.string.search_in_library)
            rowsAdapter.add(ListRow(HeaderItem(LIBRARY_HEADER_ID, label), listAdapter))
        } else if (rawCount > 0) {
            // Server returned matches but every one was hidden by the
            // filter checkboxes. Surface that in a header so the user
            // doesn't think their query failed — same affordance the
            // web /search page shows.
            val hiddenAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            val msg = getString(R.string.filter_all_hidden, rawCount)
            rowsAdapter.add(ListRow(HeaderItem(HIDDEN_HEADER_ID, msg), hiddenAdapter))
        }

        // The library search itself failed — say so, in the row label, rather
        // than letting the "No results" header below claim the user's library
        // does not contain what they asked for.
        if (searchError != null) {
            val emptyAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            rowsAdapter.add(
                ListRow(HeaderItem(SEARCH_ERROR_HEADER_ID, searchError), emptyAdapter),
            )
        }

        // Nothing matched anywhere after a real query — show an explicit
        // "No results" header so the screen doesn't look idle or hung (only the
        // filter chips would otherwise render). Suppressed before the first query.
        // searchError included: saying "No results found" when the search
        // actually FAILED asserts something false about the user's own library.
        val nothing = library.isEmpty() && rawCount == 0 && searchError == null
        if (nothing && lastQuery.isNotBlank()) {
            val emptyAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            rowsAdapter.add(ListRow(HeaderItem(NO_RESULTS_HEADER_ID, getString(R.string.no_results)), emptyAdapter))
        }

        restoreFocusIfPending()
    }

    companion object {
        private const val REQUEST_SPEECH = 1001
        private const val RESTORE_WAIT_MS = 5_000L
        private const val SCOPE_HEADER_ID = 5L
        private const val SEARCH_ERROR_HEADER_ID = 6L
        private const val FILTER_HEADER_ID = 0L
        private const val LIBRARY_HEADER_ID = 1L
        private const val HIDDEN_HEADER_ID = 2L
        private const val NO_RESULTS_HEADER_ID = 6L
    }

    private fun showScopeMenu() {
        // Opens even before the libraries have loaded (just "All libraries"
        // then): the chip is on screen from the start, so a press must
        // always answer.
        val libs = viewModel.libraries.value
        val labels = listOf(getString(R.string.all_libraries)).plus(libs.map { it.name }).toTypedArray()
        val current = viewModel.scope.value
        val checked = if (current == null) 0 else libs.indexOfFirst { it.id == current.id } + 1
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.search_in)
            .setSingleChoiceItems(labels, checked.coerceAtLeast(0)) { d, idx ->
                viewModel.setScope(if (idx == 0) null else libs[idx - 1])
                d.dismiss()
            }
            .show()
    }
}
