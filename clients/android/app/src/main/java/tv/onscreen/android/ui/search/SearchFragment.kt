package tv.onscreen.android.ui.search

import android.app.AlertDialog
import android.os.Bundle
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

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setSearchResultProvider(this)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[SearchViewModel::class.java]
        rowsAdapter = ArrayObjectAdapter(ListRowPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
            shadowEnabled = false
            selectEffectEnabled = false
        })

        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""
        }

        // Re-render whenever any of the three input streams change.
        // collectLatest keeps the most recent state; rebuildRows is
        // idempotent (clears + repopulates the adapter).
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.results.collectLatest { rebuildRows() }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.discover.collectLatest { rebuildRows() }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.scope.collectLatest { rebuildRows() }
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
            }
        }
    }

    override fun getResultsAdapter(): ObjectAdapter = rowsAdapter

    override fun onQueryTextChange(query: String): Boolean {
        viewModel.search(query)
        return true
    }

    override fun onQueryTextSubmit(query: String): Boolean {
        viewModel.search(query)
        return true
    }

    private fun rebuildRows() {
        val library = viewModel.results.value
        val discover = viewModel.discover.value

        rowsAdapter.clear()

        if (library.isNotEmpty()) {
            val cardPresenter = CardPresenter(requireContext(), serverUrl)
            val listAdapter = ArrayObjectAdapter(cardPresenter)
            library.forEach { listAdapter.add(it) }
            val label = viewModel.scope.value?.name
                ?: getString(R.string.search_in_library)
            rowsAdapter.add(ListRow(HeaderItem(0L, label), listAdapter))
        }

        if (discover.isNotEmpty()) {
            val discoverPresenter = DiscoverCardPresenter(requireContext())
            val listAdapter = ArrayObjectAdapter(discoverPresenter)
            discover.forEach { listAdapter.add(it) }
            rowsAdapter.add(
                ListRow(HeaderItem(1L, getString(R.string.search_request_more)), listAdapter),
            )
        }
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
