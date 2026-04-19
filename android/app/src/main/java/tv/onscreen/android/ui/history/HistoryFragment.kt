package tv.onscreen.android.ui.history

import android.os.Bundle
import android.view.View
import androidx.leanback.app.VerticalGridSupportFragment
import androidx.leanback.widget.ArrayObjectAdapter
import androidx.leanback.widget.FocusHighlight
import androidx.leanback.widget.VerticalGridPresenter
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.HistoryItem
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.detail.DetailFragment
import javax.inject.Inject

@AndroidEntryPoint
class HistoryFragment : VerticalGridSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: HistoryViewModel
    private lateinit var gridAdapter: ArrayObjectAdapter

    companion object {
        private const val NUM_COLUMNS = 5
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title = getString(R.string.history)
        gridPresenter = VerticalGridPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
            shadowEnabled = false
            numberOfColumns = NUM_COLUMNS
        }
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[HistoryViewModel::class.java]

        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = prefs.serverUrl.first() ?: ""
            gridAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            adapter = gridAdapter

            viewModel.uiState.collectLatest { state ->
                gridAdapter.clear()
                // De-dupe by media_id so rewatching doesn't spam the grid.
                val seen = mutableSetOf<String>()
                state.items.forEach { item ->
                    if (seen.add(item.media_id)) gridAdapter.add(item)
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            if (item !is HistoryItem) return@setOnItemViewClickedListener
            // Always go via detail so the user sees resume controls.
            parentFragmentManager.beginTransaction()
                .replace(R.id.main_container, DetailFragment.newInstance(item.media_id))
                .addToBackStack(null)
                .commit()
        }
    }

    override fun onResume() {
        super.onResume()
        viewModel.load()
    }
}
