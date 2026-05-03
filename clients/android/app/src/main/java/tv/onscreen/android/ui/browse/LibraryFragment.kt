package tv.onscreen.android.ui.browse

import android.app.AlertDialog
import android.os.Bundle
import android.view.KeyEvent
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
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.photo.PhotoViewFragment
import javax.inject.Inject

@AndroidEntryPoint
class LibraryFragment : VerticalGridSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: LibraryViewModel
    private lateinit var gridAdapter: ArrayObjectAdapter
    private var baseTitle: String = ""
    private var errorOverlay: ErrorOverlay? = null

    companion object {
        private const val ARG_LIBRARY_ID = "library_id"
        private const val ARG_LIBRARY_NAME = "library_name"
        private const val ARG_LIBRARY_TYPE = "library_type"
        private const val NUM_COLUMNS = 5

        // (sort key, sort_dir, label)
        private val SORT_OPTIONS = listOf(
            Triple("title", "asc", "Title (A–Z)"),
            Triple("title", "desc", "Title (Z–A)"),
            Triple("created_at", "desc", "Recently added"),
            Triple("year", "desc", "Newest year"),
            Triple("year", "asc", "Oldest year"),
            Triple("rating", "desc", "Highest rated"),
        )

        fun newInstance(
            libraryId: String,
            libraryName: String,
            libraryType: String = "",
        ): LibraryFragment {
            return LibraryFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_LIBRARY_ID, libraryId)
                    putString(ARG_LIBRARY_NAME, libraryName)
                    putString(ARG_LIBRARY_TYPE, libraryType)
                }
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        baseTitle = arguments?.getString(ARG_LIBRARY_NAME) ?: ""
        title = baseTitle

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
        viewModel = ViewModelProvider(this)[LibraryViewModel::class.java]

        // Visible Sort / Filter affordance for users on basic Android
        // TV remotes that have no MENU button (Google TV-DM quality
        // requirement: app must not depend on the remote's MENU
        // button to access UI controls). Reuses Leanback's title-bar
        // search-orb slot — focusable via D-pad UP from the grid —
        // but swaps the magnifying-glass icon for a sort glyph and
        // wires it to a 2-item Sort/Filter chooser. The original
        // MENU + Y / X + F2 key shortcuts still work for remotes
        // that have them.
        setOnSearchClickedListener { showSortFilterChooser() }
        view.findViewById<androidx.leanback.widget.SearchOrbView>(androidx.leanback.R.id.title_orb)
            ?.apply {
                setOrbIcon(androidx.core.content.ContextCompat.getDrawable(requireContext(), R.drawable.ic_sort))
                contentDescription = getString(R.string.sort_and_filter)
            }

        val libraryId = arguments?.getString(ARG_LIBRARY_ID) ?: return
        val libraryType = arguments?.getString(ARG_LIBRARY_TYPE) ?: ""

        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = prefs.serverUrl.first() ?: ""
            gridAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            adapter = gridAdapter

            viewModel.load(libraryId, libraryType)

            launch {
                viewModel.items.collectLatest { items ->
                    gridAdapter.clear()
                    items.forEach { gridAdapter.add(it) }
                }
            }
            launch {
                // Combined title updates whenever either sort or genre changes.
                viewModel.sort.collectLatest { updateTitle() }
            }
            launch {
                viewModel.genre.collectLatest { updateTitle() }
            }
            launch {
                viewModel.error.collectLatest { err ->
                    if (err != null) {
                        errorOverlay?.show(err) { viewModel.load(libraryId) }
                    } else {
                        errorOverlay?.hide()
                    }
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            if (item is MediaItem) {
                if (item.type == "photo") {
                    // Pass the surrounding photos as a sibling list so
                    // PhotoViewFragment's D-pad left/right cycles
                    // through them. Filtered to other photos in the
                    // current grid view (sort/genre filter applied) so
                    // the navigation matches what the user just saw.
                    val siblings = collectPhotoSiblings()
                    val startIndex = siblings.indexOf(item.id).coerceAtLeast(0)
                    parentFragmentManager.beginTransaction()
                        .replace(
                            R.id.main_container,
                            PhotoViewFragment.newInstance(item.id, siblings, startIndex),
                        )
                        .addToBackStack(null)
                        .commit()
                } else {
                    Navigator.open(parentFragmentManager, item.id, item.type, 0)
                }
            }
        }

        setOnItemViewSelectedListener { _, _, _, row ->
            // Load more when near the end.
            val pos = gridAdapter.indexOf(row)
            if (pos >= gridAdapter.size() - 10) {
                viewModel.loadMore()
            }
        }

        view.isFocusableInTouchMode = true
        view.setOnKeyListener { _, keyCode, event ->
            if (event.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false
            when (keyCode) {
                KeyEvent.KEYCODE_MENU, KeyEvent.KEYCODE_BUTTON_Y -> {
                    showSortMenu(); true
                }
                KeyEvent.KEYCODE_BUTTON_X, KeyEvent.KEYCODE_F2 -> {
                    showGenreMenu(); true
                }
                else -> false
            }
        }
    }

    /** Collect every photo currently in the grid as a flat id list,
     *  in their visible order. The library may be paginating —
     *  what's visible is what we have; loadMore-driven additions
     *  past this point won't be in the sibling list, but the
     *  PhotoViewFragment's wrap-around handles the boundary
     *  gracefully (right at the end goes back to the first
     *  visible photo). For the common case (small albums) the
     *  whole library is loaded by the time the user clicks. */
    private fun collectPhotoSiblings(): List<String> {
        val out = mutableListOf<String>()
        for (i in 0 until gridAdapter.size()) {
            val it = gridAdapter.get(i)
            if (it is MediaItem && it.type == "photo") {
                out.add(it.id)
            }
        }
        return out
    }

    private fun updateTitle() {
        val s = viewModel.sort.value
        val label = SORT_OPTIONS.firstOrNull {
            it.first == s.sort && it.second == s.sortDir
        }?.third ?: "Sort"
        val genre = viewModel.genre.value
        val genrePart = if (genre != null) "  ·  $genre" else ""
        title = "$baseTitle  ·  $label$genrePart  (MENU sort / X filter)"
    }

    /** Two-step chooser shown when the user activates the title-bar
     *  Sort/Filter orb: first pick "Sort by…" or "Filter by
     *  genre…", then drill into the corresponding existing menu.
     *  This is the no-MENU-button entry point for TV-DM
     *  compliance. */
    private fun showSortFilterChooser() {
        val labels = arrayOf(getString(R.string.sort_by), getString(R.string.filter_by_genre))
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.sort_and_filter)
            .setItems(labels) { d, idx ->
                d.dismiss()
                when (idx) {
                    0 -> showSortMenu()
                    1 -> showGenreMenu()
                }
            }
            .show()
    }

    private fun showGenreMenu() {
        val genres = viewModel.genres.value
        if (genres.isEmpty()) return
        val labels = listOf("All genres").plus(genres).toTypedArray()
        val current = viewModel.genre.value
        val checked = if (current == null) 0 else genres.indexOf(current) + 1
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle("Filter by genre")
            .setSingleChoiceItems(labels, checked.coerceAtLeast(0)) { d, idx ->
                viewModel.setGenre(if (idx == 0) null else genres[idx - 1])
                d.dismiss()
            }
            .show()
    }

    private fun showSortMenu() {
        val current = viewModel.sort.value
        val labels = SORT_OPTIONS.map { it.third }.toTypedArray()
        val checked = SORT_OPTIONS.indexOfFirst { it.first == current.sort && it.second == current.sortDir }
            .coerceAtLeast(0)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle("Sort by")
            .setSingleChoiceItems(labels, checked) { d, idx ->
                val opt = SORT_OPTIONS[idx]
                viewModel.setSort(opt.first, opt.second)
                d.dismiss()
            }
            .show()
    }
}
