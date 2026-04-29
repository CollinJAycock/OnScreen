package tv.onscreen.android.ui.livetv

import android.os.Bundle
import android.view.View
import android.widget.Toast
import androidx.leanback.app.VerticalGridSupportFragment
import androidx.leanback.widget.ArrayObjectAdapter
import androidx.leanback.widget.FocusHighlight
import androidx.leanback.widget.VerticalGridPresenter
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.Recording
import tv.onscreen.android.ui.detail.DetailFragment

@AndroidEntryPoint
class RecordingsFragment : VerticalGridSupportFragment() {

    private lateinit var viewModel: RecordingsViewModel
    private lateinit var gridAdapter: ArrayObjectAdapter

    companion object {
        private const val NUM_COLUMNS = 4
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title = getString(R.string.recordings)
        gridPresenter = VerticalGridPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
            shadowEnabled = false
            numberOfColumns = NUM_COLUMNS
        }
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[RecordingsViewModel::class.java]
        gridAdapter = ArrayObjectAdapter(RecordingCardPresenter(requireContext()))
        adapter = gridAdapter

        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.uiState.collectLatest { state ->
                gridAdapter.clear()
                state.items.forEach { gridAdapter.add(it) }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            val r = item as? Recording ?: return@setOnItemViewClickedListener
            // Only completed recordings have an item_id and are
            // playable. Scheduled / in-flight / failed entries are
            // informational on the TV — schedule management lives in
            // the web settings UI.
            if (r.status == "completed" && r.item_id != null) {
                parentFragmentManager.beginTransaction()
                    .replace(R.id.main_container, DetailFragment.newInstance(r.item_id))
                    .addToBackStack(null)
                    .commit()
            } else {
                val msg = when (r.status) {
                    "scheduled" -> getString(R.string.recording_scheduled_msg)
                    "recording" -> getString(R.string.recording_in_progress_msg)
                    "failed" -> r.error ?: getString(R.string.recording_failed_msg)
                    else -> getString(R.string.recording_unavailable_msg)
                }
                Toast.makeText(requireContext(), msg, Toast.LENGTH_SHORT).show()
            }
        }
    }

    override fun onResume() {
        super.onResume()
        viewModel.load()
    }
}
