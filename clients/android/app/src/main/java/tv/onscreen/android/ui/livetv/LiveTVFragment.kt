package tv.onscreen.android.ui.livetv

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
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
import tv.onscreen.android.ui.common.ErrorOverlay

@AndroidEntryPoint
class LiveTVFragment : VerticalGridSupportFragment() {

    private lateinit var viewModel: LiveTVViewModel
    private lateinit var gridAdapter: ArrayObjectAdapter
    private var errorOverlay: ErrorOverlay? = null

    companion object {
        // Channel cards are wider than poster cards so 4 across reads
        // better than the standard 5 — keeps logos legible without
        // truncating the channel name on a 1080p TV.
        private const val NUM_COLUMNS = 4
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title = getString(R.string.live_tv)
        gridPresenter = VerticalGridPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
            shadowEnabled = false
            numberOfColumns = NUM_COLUMNS
        }
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View {
        val inner = super.onCreateView(inflater, container, savedInstanceState)
            ?: return super.onCreateView(inflater, container, savedInstanceState)!!
        val overlay = ErrorOverlay.wrap(inner)
        errorOverlay = overlay
        return overlay.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[LiveTVViewModel::class.java]
        gridAdapter = ArrayObjectAdapter(ChannelCardPresenter(requireContext()))
        adapter = gridAdapter

        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.uiState.collectLatest { state ->
                gridAdapter.clear()
                state.channels.forEach { gridAdapter.add(it) }
                when {
                    state.error != null -> errorOverlay?.show(state.error) { viewModel.load() }
                    !state.isLoading && state.channels.isEmpty() ->
                        errorOverlay?.showEmpty(R.string.empty_live_tv_title, R.string.empty_live_tv_message)
                    else -> errorOverlay?.hide()
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            val entry = item as? ChannelEntry ?: return@setOnItemViewClickedListener
            parentFragmentManager.beginTransaction()
                .replace(
                    R.id.main_container,
                    LiveChannelPlayerFragment.newInstance(entry.channel.id, entry.channel.name),
                )
                .addToBackStack(null)
                .commit()
        }
    }

    override fun onResume() {
        super.onResume()
        // Pull fresh now/next on every entry — programs change while
        // the user is browsing the rest of the app.
        viewModel.load()
    }
}
