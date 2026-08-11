package tv.onscreen.android.ui.search

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.os.Bundle
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
 * Library-scoped searches (the Y / menu key opens a picker) skip
 * the TMDB row — the user has narrowed to a specific shelf and
 * cross-library suggestions would be confusing.
 */
@AndroidEntryPoint
class SearchFragment : SearchSupportFragment(), SearchSupportFragment.SearchResultProvider {

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

    /** Persistent top-row adapters, mutated in place across rebuilds so
     *  chip focus survives a filter toggle. Null until first build; reset
     *  in onDestroyView with the rows they live in. */
    private var scopeAdapter: ArrayObjectAdapter? = null
    private var chipAdapter: ArrayObjectAdapter? = null

    private fun scopeLabel(): String =
        "${getString(R.string.search_in)}: ${viewModel.scope.value?.name ?: getString(R.string.all_libraries)}"

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setSearchResultProvider(this)
        // Wire the mic orb to the system speech recognizer. Using the callback +
        // RecognizerIntent means we DON'T need the RECORD_AUDIO permission (the
        // recognizer app owns the mic) — and where no recognizer exists (some Fire
        // remotes), the catch falls back to the on-screen keyboard instead of a
        // dead orb. Deprecated API, but the supported no-permission path on
        // Leanback 1.0.0.
        @Suppress("DEPRECATION")
        setSpeechRecognitionCallback {
            try {
                startActivityForResult(
                    Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
                        putExtra(
                            RecognizerIntent.EXTRA_LANGUAGE_MODEL,
                            RecognizerIntent.LANGUAGE_MODEL_FREE_FORM,
                        )
                    },
                    REQUEST_SPEECH,
                )
            } catch (e: Exception) {
                // No recognizer installed — leave the user on the keyboard.
            }
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == REQUEST_SPEECH && resultCode == Activity.RESULT_OK) {
            data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)
                ?.firstOrNull()
                ?.let { setSearchQuery(it, true) }
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
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

        view.isFocusableInTouchMode = true
        view.setOnKeyListener { _, keyCode, event ->
            if (event.action == KeyEvent.ACTION_DOWN &&
                (keyCode == KeyEvent.KEYCODE_MENU || keyCode == KeyEvent.KEYCODE_BUTTON_Y)) {
                showScopeMenu(); true
            } else false
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            when (item) {
                is SearchResult -> {
                    // Remember where we were BEFORE the view is torn down.
                    pendingFocusRow = rowsSupportFragment?.selectedPosition ?: -1
                    Navigator.open(parentFragmentManager, item.id, item.type, 0)
                }
                is FilterChipPresenter.Chip ->
                    viewModel.toggleFilter(item.type)
                is ScopeChipPresenter.ScopeChip ->
                    showScopeMenu()
            }
        }
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
        pendingFocusRow = -1
        val rowsFrag = rowsSupportFragment ?: return
        // The selection is a pending op Leanback honors at layout, but
        // requestFocus() needs an ALREADY-laid-out focusable child — called
        // synchronously here the rows exist only as adapter items, so the
        // request found nothing and focus stayed lost (the first fix's
        // mistake). Defer past the layout pass.
        rowsFrag.setSelectedPosition(target, false)
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
        viewModel.search(query)
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
        val libs = viewModel.libraries.value
        if (scopeAdapter == null && libs.isNotEmpty()) {
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
        private const val SCOPE_HEADER_ID = 5L
        private const val SEARCH_ERROR_HEADER_ID = 6L
        private const val FILTER_HEADER_ID = 0L
        private const val LIBRARY_HEADER_ID = 1L
        private const val HIDDEN_HEADER_ID = 2L
        private const val NO_RESULTS_HEADER_ID = 6L
    }

    private fun showScopeMenu() {
        val libs = viewModel.libraries.value
        if (libs.isEmpty()) return
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
