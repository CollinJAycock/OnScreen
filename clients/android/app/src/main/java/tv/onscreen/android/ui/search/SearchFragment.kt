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
import tv.onscreen.android.data.model.DiscoverItem
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
 *   - "Request": TMDB-discover matches (DiscoverItem) for titles
 *     not yet in the library. Click prompts a confirm dialog;
 *     accepting fires POST /api/v1/requests so an admin (or auto-
 *     fulfilment via the configured Sonarr/Radarr) can pull it in.
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

        // Re-render whenever any of the input streams change.
        // collectLatest keeps the most recent state; rebuildRows is
        // idempotent (clears + repopulates the adapter).
        //
        // serverUrl is read inside each collector so the read completes
        // BEFORE the first rebuildRows. The previous structure launched
        // the prefs read as a separate coroutine that raced against the
        // state collectors — same race that made hub posters render as
        // flat colour tiles on Fire TV.
        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""
            viewModel.visibleResults.collectLatest { rebuildRows() }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.discover.collectLatest { rebuildRows() }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.discoverError.collectLatest { rebuildRows() }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.scope.collectLatest { rebuildRows() }
        }
        // Filter changes flow through visibleResults too, but binding
        // here as well so the chip row repaints with the new check
        // marks immediately (visibleResults only changes if the
        // filter's mask differs against an existing result set).
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.filters.collectLatest { rebuildRows() }
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
                is SearchResult ->
                    Navigator.open(parentFragmentManager, item.id, item.type, 0)
                is DiscoverItem ->
                    onDiscoverClicked(item)
                is FilterChipPresenter.Chip ->
                    viewModel.toggleFilter(item.type)
                is ScopeChipPresenter.ScopeChip ->
                    showScopeMenu()
            }
        }
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
        val discover = viewModel.discover.value
        val discoverError = viewModel.discoverError.value
        val filters = viewModel.filters.value

        rowsAdapter.clear()

        // Scope row first — a focusable on-screen affordance to open the library
        // picker, so users on basic Fire TV remotes (no MENU / Y key) can still
        // narrow the search. Only when there are libraries to scope to.
        val libs = viewModel.libraries.value
        if (libs.isNotEmpty()) {
            val scopeName = viewModel.scope.value?.name ?: getString(R.string.all_libraries)
            val scopeAdapter = ArrayObjectAdapter(ScopeChipPresenter(requireContext()))
            scopeAdapter.add(ScopeChipPresenter.ScopeChip("${getString(R.string.search_in)}: $scopeName"))
            rowsAdapter.add(ListRow(HeaderItem(SCOPE_HEADER_ID, getString(R.string.search_in)), scopeAdapter))
        }

        // Filter chip row first — always visible so the user can
        // toggle types regardless of whether anything matched. Order
        // (Movies, TV Shows, Episodes, Tracks) matches the web
        // /search page so the visual mental model carries across
        // surfaces.
        val chipPresenter = FilterChipPresenter(requireContext())
        val chipAdapter = ArrayObjectAdapter(chipPresenter)
        chipAdapter.add(FilterChipPresenter.Chip(SearchViewModel.FilterType.MOVIE,
            getString(R.string.filter_movies), filters.movie))
        chipAdapter.add(FilterChipPresenter.Chip(SearchViewModel.FilterType.SHOW,
            getString(R.string.filter_shows), filters.show))
        chipAdapter.add(FilterChipPresenter.Chip(SearchViewModel.FilterType.EPISODE,
            getString(R.string.filter_episodes), filters.episode))
        chipAdapter.add(FilterChipPresenter.Chip(SearchViewModel.FilterType.TRACK,
            getString(R.string.filter_tracks), filters.track))
        rowsAdapter.add(ListRow(HeaderItem(FILTER_HEADER_ID, getString(R.string.filter_label)), chipAdapter))

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

        if (discover.isNotEmpty()) {
            val discoverPresenter = DiscoverCardPresenter(requireContext())
            val listAdapter = ArrayObjectAdapter(discoverPresenter)
            discover.forEach { listAdapter.add(it) }
            rowsAdapter.add(
                ListRow(HeaderItem(DISCOVER_HEADER_ID, getString(R.string.search_request_more)), listAdapter),
            )
        } else if (discoverError != null) {
            // Surface the discover failure in the row label itself so
            // the user can see why the Request row is empty without
            // needing to dig into logcat. Hidden when discover
            // succeeded with no results (the "no TMDB matches"
            // case shouldn't render any chrome at all).
            val emptyAdapter = ArrayObjectAdapter(DiscoverCardPresenter(requireContext()))
            rowsAdapter.add(
                ListRow(HeaderItem(DISCOVER_ERROR_HEADER_ID, discoverError), emptyAdapter),
            )
        }

        // Nothing matched anywhere after a real query — show an explicit
        // "No results" header so the screen doesn't look idle or hung (only the
        // filter chips would otherwise render). Suppressed before the first query.
        val nothing = library.isEmpty() && rawCount == 0 && discover.isEmpty() && discoverError == null
        if (nothing && lastQuery.isNotBlank()) {
            val emptyAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            rowsAdapter.add(ListRow(HeaderItem(NO_RESULTS_HEADER_ID, getString(R.string.no_results)), emptyAdapter))
        }
    }

    companion object {
        private const val REQUEST_SPEECH = 1001
        private const val SCOPE_HEADER_ID = 5L
        private const val FILTER_HEADER_ID = 0L
        private const val LIBRARY_HEADER_ID = 1L
        private const val HIDDEN_HEADER_ID = 2L
        private const val DISCOVER_HEADER_ID = 3L
        private const val DISCOVER_ERROR_HEADER_ID = 4L
        private const val NO_RESULTS_HEADER_ID = 6L
    }

    private fun onDiscoverClicked(item: DiscoverItem) {
        if (item.has_active_request) {
            // Already requested — show a status dialog so the user
            // sees where it is in the pipeline (pending / approved /
            // downloading / available / failed).
            val status = item.active_request_status?.replaceFirstChar { it.uppercase() }
                ?: getString(R.string.request_status_pending)
            AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
                .setTitle(item.title)
                .setMessage(getString(R.string.request_status_already, status))
                .setPositiveButton(android.R.string.ok, null)
                .create()
                .focusableOnTv()
                .show()
            return
        }

        // Confirm before firing the POST so a stray D-pad press on
        // a discover row doesn't accidentally request a movie.
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(getString(R.string.request_confirm_title))
            .setMessage(getString(R.string.request_confirm_message, item.title))
            .setPositiveButton(R.string.request_confirm_yes) { _, _ ->
                viewModel.request(item) { result ->
                    val ctx = context ?: return@request
                    val msg = result.fold(
                        onSuccess = { ctx.getString(R.string.request_success, item.title) },
                        onFailure = { e -> e.message ?: ctx.getString(R.string.request_failed) },
                    )
                    Toast.makeText(ctx, msg, Toast.LENGTH_LONG).show()
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .create()
            .focusableOnTv()
            .show()
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
