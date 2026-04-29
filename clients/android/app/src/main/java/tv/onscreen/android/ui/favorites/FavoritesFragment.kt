package tv.onscreen.android.ui.favorites

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
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.FavoriteItem
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.Navigator
import javax.inject.Inject

@AndroidEntryPoint
class FavoritesFragment : VerticalGridSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: FavoritesViewModel
    private lateinit var gridAdapter: ArrayObjectAdapter
    private var errorOverlay: ErrorOverlay? = null

    companion object {
        private const val NUM_COLUMNS = 5
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title = getString(R.string.favorites)
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
        viewModel = ViewModelProvider(this)[FavoritesViewModel::class.java]

        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = prefs.serverUrl.first() ?: ""
            gridAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            adapter = gridAdapter

            viewModel.uiState.collectLatest { state ->
                gridAdapter.clear()
                state.items.forEach { gridAdapter.add(it) }
                when {
                    state.error != null -> errorOverlay?.show(state.error) { viewModel.load() }
                    !state.isLoading && state.items.isEmpty() ->
                        errorOverlay?.showEmpty(R.string.empty_favorites_title, R.string.empty_favorites_message)
                    else -> errorOverlay?.hide()
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            if (item !is FavoriteItem) return@setOnItemViewClickedListener
            Navigator.open(parentFragmentManager, item.id, item.type, 0)
        }
    }

    override fun onResume() {
        super.onResume()
        viewModel.load()
    }
}
