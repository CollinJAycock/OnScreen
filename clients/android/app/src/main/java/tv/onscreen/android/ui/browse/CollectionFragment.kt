package tv.onscreen.android.ui.browse

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
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.CollectionItem
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.GridScrollMemory
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.common.syncItems
import javax.inject.Inject

@AndroidEntryPoint
class CollectionFragment : VerticalGridSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: CollectionViewModel
    private lateinit var gridAdapter: ArrayObjectAdapter
    private var errorOverlay: ErrorOverlay? = null
    private val scroll = GridScrollMemory() // keep grid position across detail/back

    companion object {
        private const val ARG_COLLECTION_ID = "collection_id"
        private const val ARG_COLLECTION_NAME = "collection_name"
        private const val NUM_COLUMNS = 5

        fun newInstance(collectionId: String, collectionName: String): CollectionFragment {
            return CollectionFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_COLLECTION_ID, collectionId)
                    putString(ARG_COLLECTION_NAME, collectionName)
                }
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title = arguments?.getString(ARG_COLLECTION_NAME) ?: ""

        val presenter = VerticalGridPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
            shadowEnabled = false
        }
        presenter.numberOfColumns = NUM_COLUMNS
        gridPresenter = presenter
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
        viewModel = ViewModelProvider(this)[CollectionViewModel::class.java]

        val collectionId = arguments?.getString(ARG_COLLECTION_ID) ?: return

        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = prefs.serverUrl.first() ?: ""
            gridAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            adapter = gridAdapter
            scroll.onViewRecreated()

            viewModel.load(collectionId)

            // Combine so a failed/empty load shows a focusable overlay instead of a
            // blank, unfocusable grid the remote can't escape (only BACK worked).
            combine(viewModel.items, viewModel.error, viewModel.loaded) { items, error, loaded ->
                Triple(items, error, loaded)
            }.collectLatest { (items, error, loaded) ->
                gridAdapter.syncItems(items) { it.id }
                scroll.restoreIfPending(gridAdapter.size(), ::setSelectedPosition)
                when {
                    error != null -> errorOverlay?.show(error) { viewModel.load(collectionId) }
                    loaded && items.isEmpty() ->
                        errorOverlay?.showEmpty(R.string.empty_collection_title, R.string.empty_collection_message)
                    else -> errorOverlay?.hide()
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            if (item is CollectionItem) {
                Navigator.open(parentFragmentManager, item.id, item.type, 0)
            }
        }

        // Prefetch the next page as the selection nears the end — without this a
        // collection with >50 items only ever showed its first page. Uses the
        // selected `item` (a VerticalGrid's 4th `row` param is null); syncItems
        // appends the page so focus/scroll is preserved.
        setOnItemViewSelectedListener { _, item, _, _ ->
            val pos = gridAdapter.indexOf(item)
            scroll.record(pos) // remember position for a detail/back restore
            if (pos >= 0 && pos >= gridAdapter.size() - 10) {
                viewModel.loadMore()
            }
        }
    }
}
