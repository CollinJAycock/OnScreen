package tv.onscreen.android.ui.search

import android.app.AlertDialog
import android.os.Bundle
import android.view.KeyEvent
import android.view.View
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
import javax.inject.Inject

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

        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.results.collectLatest { results ->
                updateResults(results)
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.scope.collectLatest { updateResults(viewModel.results.value) }
        }

        view.isFocusableInTouchMode = true
        view.setOnKeyListener { _, keyCode, event ->
            if (event.action == KeyEvent.ACTION_DOWN &&
                (keyCode == KeyEvent.KEYCODE_MENU || keyCode == KeyEvent.KEYCODE_BUTTON_Y)) {
                showScopeMenu(); true
            } else false
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            if (item is SearchResult) {
                Navigator.open(parentFragmentManager, item.id, item.type, 0)
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

    private fun updateResults(results: List<SearchResult>) {
        val cardPresenter = CardPresenter(requireContext(), serverUrl)
        val listAdapter = ArrayObjectAdapter(cardPresenter)
        results.forEach { listAdapter.add(it) }

        rowsAdapter.clear()
        if (results.isNotEmpty()) {
            val label = viewModel.scope.value?.name ?: getString(R.string.app_name)
            rowsAdapter.add(ListRow(HeaderItem(label), listAdapter))
        }
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
