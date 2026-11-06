package tv.onscreen.android.ui.browse

import android.app.AlertDialog
import android.os.Bundle
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.View
import android.view.Gravity
import android.view.ViewGroup
import android.widget.FrameLayout
import androidx.core.view.doOnLayout
import androidx.leanback.app.VerticalGridSupportFragment
import androidx.leanback.widget.ArrayObjectAdapter
import androidx.leanback.widget.FocusHighlight
import androidx.leanback.widget.SearchOrbView
import androidx.leanback.widget.VerticalGridPresenter
import androidx.leanback.widget.VerticalGridView
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.LibraryLetter
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.model.WatchFilter
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.GridScrollMemory
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.common.WatchStateUi
import tv.onscreen.android.ui.common.syncItems
import tv.onscreen.android.ui.photo.PhotoViewFragment
import tv.onscreen.android.ui.search.SearchFragment
import javax.inject.Inject

@AndroidEntryPoint
class LibraryFragment : VerticalGridSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: LibraryViewModel
    private lateinit var gridAdapter: ArrayObjectAdapter
    private var baseTitle: String = ""
    private var libraryType: String = ""
    private var errorOverlay: ErrorOverlay? = null
    // Restores the grid position across the view recreation a detail/back round-trip
    // causes (see GridScrollMemory) — else the grid snaps back to the top on return.
    private val scroll = GridScrollMemory()
    // The card last opened from this grid. On return its page is re-read so a
    // watched mark made on the detail screen (or a finished playback) shows on
    // the card; survives the view teardown the round-trip causes.
    private var lastOpenedId: String? = null
    // Set when a Sort / genre / Watch pick changes the query. The reload empties
    // the grid, which knocks focus off the orb onto the focusable wrapper root —
    // where the D-pad finds nothing and the remote goes dead until BACK. The
    // first page of the new query takes focus instead.
    private var focusGridOnReload = false
    // The A–Z jump column; null outside the view's lifetime.
    private var letterRail: LetterRail? = null

    companion object {
        private const val ARG_LIBRARY_ID = "library_id"
        private const val ARG_LIBRARY_NAME = "library_name"
        private const val ARG_LIBRARY_TYPE = "library_type"
        private const val NUM_COLUMNS = 5
        // Below the 27dp + 60dp title bar, clear of the centred grid's cards.
        private const val RAIL_MARGIN_START_DP = 20
        private const val RAIL_MARGIN_TOP_DP = 100
        private const val RAIL_MARGIN_BOTTOM_DP = 32

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
        libraryType = arguments?.getString(ARG_LIBRARY_TYPE) ?: ""
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
        // The A–Z rail sits in the grid's dock, at the start edge below the
        // title bar. The 5-column grid is centred in the dock, so the rail
        // lives in the margin to its left instead of pushing it over. Added
        // after the grid, so DOWN from the title bar still lands on the grid.
        inner.findViewById<ViewGroup>(androidx.leanback.R.id.browse_grid_dock)?.let { dock ->
            val density = resources.displayMetrics.density
            val rail = LetterRail(requireContext())
            dock.addView(
                rail.view,
                FrameLayout.LayoutParams(
                    FrameLayout.LayoutParams.WRAP_CONTENT,
                    FrameLayout.LayoutParams.MATCH_PARENT,
                    Gravity.START,
                ).apply {
                    marginStart = (RAIL_MARGIN_START_DP * density).toInt()
                    topMargin = (RAIL_MARGIN_TOP_DP * density).toInt()
                    bottomMargin = (RAIL_MARGIN_BOTTOM_DP * density).toInt()
                },
            )
            letterRail = rail
        }
        val overlay = ErrorOverlay.wrap(inner)
        errorOverlay = overlay
        return overlay.root
    }

    override fun onDestroyView() {
        letterRail = null
        super.onDestroyView()
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
        val sortOrb = view.findViewById<SearchOrbView>(androidx.leanback.R.id.title_orb)
            ?.apply {
                setOrbIcon(androidx.core.content.ContextCompat.getDrawable(requireContext(), R.drawable.ic_sort))
                contentDescription = getString(R.string.sort_and_filter)
            }
        val titleView = view.findViewById<ViewGroup>(androidx.leanback.R.id.browse_title_group)
        if (sortOrb != null && titleView != null) addSearchOrb(titleView, sortOrb)

        val libraryId = arguments?.getString(ARG_LIBRARY_ID) ?: return
        val libraryType = arguments?.getString(ARG_LIBRARY_TYPE) ?: ""

        // A–Z rail: LEFT from the grid's first column moves onto it, at the
        // letter of the card that was focused; RIGHT (or a pick) goes back.
        val grid = view.findViewById<VerticalGridView>(androidx.leanback.R.id.browse_grid)
        letterRail?.apply {
            onPick = ::jumpToLetter
            onExit = { grid?.requestFocus() }
        }
        grid?.setOnKeyInterceptListener { event ->
            val rail = letterRail
            if (event.action == KeyEvent.ACTION_DOWN && event.keyCode == KeyEvent.KEYCODE_DPAD_LEFT &&
                rail != null && rail.isShowing && grid.selectedPosition % NUM_COLUMNS == 0
            ) {
                rail.focusFor(viewModel.listingPosition(grid.selectedPosition.coerceAtLeast(0)))
            } else {
                false
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = prefs.serverUrl.first() ?: ""
            gridAdapter = ArrayObjectAdapter(CardPresenter(requireContext(), serverUrl))
            adapter = gridAdapter
            scroll.onViewRecreated() // arm position restore for a detail/back return

            viewModel.load(libraryId, libraryType)
            lastOpenedId?.let { opened ->
                lastOpenedId = null
                // Only libraries whose items carry a watch state can change.
                if (WatchStateUi.supportsWatchFilter(libraryType)) viewModel.refreshAround(opened)
            }

            launch {
                viewModel.items.collectLatest { items ->
                    // Append-or-rebuild preserving focus/scroll — critical for the
                    // pagination path: a new page must stream in WITHOUT yanking the
                    // user back to the top mid-scroll.
                    // updateChanged: a refreshed page (refreshAround, after a
                    // detail round-trip) swaps changed cards in place so new
                    // watch badges show without disturbing focus.
                    gridAdapter.syncItems(items, updateChanged = true) { it.id }
                    // Restore the pre-navigation position once the recreated grid has
                    // repopulated (the view is destroyed on a detail/back round-trip).
                    scroll.restoreIfPending(gridAdapter.size(), ::setSelectedPosition)
                    if (focusGridOnReload && items.isNotEmpty()) {
                        focusGridOnReload = false
                        setSelectedPosition(0)
                        view.findViewById<View>(androidx.leanback.R.id.browse_grid)?.requestFocus()
                    }
                    // An A–Z jump's page just replaced the grid: focus the
                    // letter's first card.
                    viewModel.takeFocusTarget()?.let(::focusGridAt)
                }
            }
            launch {
                // The rail only makes sense over a populated grid: hidden
                // behind the error / empty overlay, and empty (so hidden)
                // unless sorted by title on a server with the index.
                combine(viewModel.letters, viewModel.items, viewModel.error) { letters, items, err ->
                    if (err == null && items.isNotEmpty()) letters else emptyList()
                }.distinctUntilChanged().collectLatest { letterRail?.setLetters(it) }
            }
            launch {
                // Combined title updates whenever either sort or genre changes.
                viewModel.sort.collectLatest { updateTitle() }
            }
            launch {
                viewModel.genre.collectLatest { updateTitle() }
            }
            launch {
                viewModel.watch.collectLatest { updateTitle() }
            }
            launch {
                viewModel.events.collect { event ->
                    when (event) {
                        is LibraryEvent.Open -> {
                            lastOpenedId = event.id
                            Navigator.open(parentFragmentManager, event.id, event.type, 0)
                        }
                        LibraryEvent.NothingToPick -> android.widget.Toast.makeText(
                            requireContext(), R.string.surprise_nothing, android.widget.Toast.LENGTH_SHORT,
                        ).show()
                        LibraryEvent.PickFailed -> android.widget.Toast.makeText(
                            requireContext(), R.string.surprise_failed, android.widget.Toast.LENGTH_SHORT,
                        ).show()
                        LibraryEvent.JumpFailed -> android.widget.Toast.makeText(
                            requireContext(), R.string.letter_jump_failed, android.widget.Toast.LENGTH_SHORT,
                        ).show()
                    }
                }
            }
            launch {
                // Error wins; otherwise a loaded-but-empty library/zero-match filter
                // shows the empty state instead of a blank grid with no feedback.
                combine(viewModel.items, viewModel.error, viewModel.loaded) { items, err, loaded ->
                    Triple(items, err, loaded)
                }.collectLatest { (items, err, loaded) ->
                    when {
                        err != null -> {
                            // Pass libraryType so a retry keeps the per-type default
                            // sort (e.g. home_video/photo = newest-first) instead of
                            // silently falling back to Title A–Z.
                            errorOverlay?.show(err) { viewModel.load(libraryId, libraryType) }
                        }
                        loaded && items.isEmpty() ->
                            errorOverlay?.showEmpty(R.string.empty_library_title, R.string.empty_library_message)
                        else -> errorOverlay?.hide()
                    }
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            if (item is MediaItem) {
                lastOpenedId = item.id
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

        setOnItemViewSelectedListener { _, item, _, _ ->
            // On a VerticalGrid the selected object is `item` (the 4th `row` param is
            // null), so indexOf(item) is the current position.
            val pos = gridAdapter.indexOf(item)
            // Remember where we are so a detail/back round-trip can restore it.
            scroll.record(pos)
            // Prefetch the next page as the selection nears the end (old indexOf(row)
            // was always -1, so loadMore() never fired — the grid stuck on page 1).
            if (pos >= 0 && pos >= gridAdapter.size() - 10) {
                viewModel.loadMore()
            }
            // After an A–Z jump the grid starts mid-listing: page in what's
            // above as the selection nears the top (a no-op from row 0).
            if (pos in 0 until NUM_COLUMNS * 2) {
                viewModel.loadPrevious()
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
                KeyEvent.KEYCODE_SEARCH -> {
                    openLibrarySearch(); true
                }
                else -> false
            }
        }
    }

    /**
     * "Search this library": a second orb beside the Sort / Filter one, with
     * Leanback's own magnifier icon. Leanback's title bar has a single orb
     * slot, so this one is added to the TitleView by hand and placed after
     * the first once that is laid out.
     *
     * Its D-pad links are explicit: the title bar's focus-search hook sends
     * RIGHT from anything in the title bar down to the grid, so without the
     * key listeners RIGHT from Sort would skip this orb entirely.
     */
    private fun addSearchOrb(titleView: ViewGroup, sortOrb: SearchOrbView) {
        val gap = (8 * resources.displayMetrics.density).toInt()
        val orb = SearchOrbView(requireContext()).apply {
            contentDescription = getString(R.string.search_this_library)
            setOnOrbClickedListener { openLibrarySearch() }
        }
        val lp = FrameLayout.LayoutParams(
            FrameLayout.LayoutParams.WRAP_CONTENT,
            FrameLayout.LayoutParams.WRAP_CONTENT,
            Gravity.START or Gravity.CENTER_VERTICAL,
        )
        titleView.addView(orb, lp)
        sortOrb.doOnLayout {
            lp.marginStart = it.right - titleView.paddingStart + gap
            orb.layoutParams = lp
            orb.orbColors = sortOrb.orbColors
        }
        // DOWN from either orb goes to the grid by hand too. Leanback's own
        // title-bar DOWN hands focus to the grid's scene root, and here that
        // lands on the focusable wrapper root, which draws no highlight and
        // finds nothing in any direction: the remote seemed dead until BACK.
        fun focusGrid(): Boolean =
            view?.findViewById<View>(androidx.leanback.R.id.browse_grid)?.requestFocus() == true
        sortOrb.setOnKeyListener { _, keyCode, event ->
            if (event.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false
            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_RIGHT -> orb.requestFocus()
                KeyEvent.KEYCODE_DPAD_DOWN -> focusGrid()
                else -> false
            }
        }
        orb.setOnKeyListener { _, keyCode, event ->
            if (event.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false
            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_LEFT -> sortOrb.requestFocus()
                KeyEvent.KEYCODE_DPAD_DOWN -> focusGrid()
                // The last thing in the title bar: RIGHT stays here. Left to
                // focus search it landed on the focusable wrapper root, which
                // draws no highlight, so the remote seemed to stop working.
                KeyEvent.KEYCODE_DPAD_RIGHT -> true
                else -> false
            }
        }
    }

    /** The search screen, scoped to this library (BACK returns here). */
    private fun openLibrarySearch() {
        val id = arguments?.getString(ARG_LIBRARY_ID) ?: return
        parentFragmentManager.beginTransaction()
            .replace(R.id.main_container, SearchFragment.forLibrary(id, baseTitle, libraryType))
            .addToBackStack(null)
            .commit()
    }

    /** An A–Z rail pick: focus the letter's first card now if it's loaded,
     *  else once the page holding it arrives (see the items collector). */
    private fun jumpToLetter(entry: LibraryLetter) {
        viewModel.jumpTo(entry)?.let(::focusGridAt)
    }

    private fun focusGridAt(position: Int) {
        setSelectedPosition(position)
        view?.findViewById<View>(androidx.leanback.R.id.browse_grid)?.requestFocus()
        // Leanback shows the title only while the first row is selected, but
        // it decides that when the selected card is already laid out. A jump's
        // card usually isn't yet, so the check is skipped and the title stays
        // up over the scrolled grid. Set it from the row here.
        showTitle(position < NUM_COLUMNS)
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
        val watchPart = viewModel.watch.value?.let { "  ·  ${watchFilterLabel(it)}" } ?: ""
        // No key hint here: the singleLine 44sp TitleView ellipsized it (and
        // on Fire TV the named keys don't even exist on the remote). The
        // Sort/Filter orb is the discoverable entry point.
        title = "$baseTitle  ·  $label$genrePart$watchPart"
    }

    private fun watchFilterLabel(filter: String?): String = getString(
        when (filter) {
            WatchFilter.Unwatched -> R.string.watch_filter_unwatched
            WatchFilter.InProgress -> R.string.watch_filter_in_progress
            WatchFilter.Watched -> R.string.watch_filter_watched
            else -> R.string.watch_filter_all
        },
    )

    /** All items / Unwatched / In progress / Watched, sent as `?watch=`.
     *  Only offered for library types whose items carry a watch state. */
    private fun showWatchFilterMenu() {
        val options = WatchStateUi.WATCH_FILTERS
        val labels = options.map { watchFilterLabel(it) }.toTypedArray()
        val checked = options.indexOf(viewModel.watch.value).coerceAtLeast(0)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.watch_filter_title)
            .setSingleChoiceItems(labels, checked) { d, idx ->
                changeQuery { viewModel.setWatchFilter(options[idx]) }
                d.dismiss()
            }
            .show()
    }

    /** Two-step chooser shown when the user activates the title-bar
     *  Sort/Filter orb: first pick "Sort by…" or "Filter by
     *  genre…", then drill into the corresponding existing menu.
     *  This is the no-MENU-button entry point for TV-DM
     *  compliance. */
    private fun showSortFilterChooser() {
        // (label, action) so the Watch entry can be left out for library types
        // without a watch state (music, photos, books, podcasts).
        val entries = buildList<Pair<String, () -> Unit>> {
            add(getString(R.string.sort_by) to ::showSortMenu)
            add(getString(R.string.filter_by_genre) to ::showGenreMenu)
            if (WatchStateUi.supportsWatchFilter(libraryType)) {
                add(getString(R.string.filter_by_watch) to ::showWatchFilterMenu)
                add(getString(R.string.surprise_me) to viewModel::surpriseMe)
            }
        }
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.sort_and_filter)
            .setItems(entries.map { it.first }.toTypedArray()) { d, idx ->
                d.dismiss()
                entries.getOrNull(idx)?.second?.invoke()
            }
            .show()
    }

    /** Apply a Sort / genre / Watch pick. Only a pick that changed the query
     *  reloads the grid, so only that one arms [focusGridOnReload] — an
     *  unchanged pick must not yank focus on the next refresh. */
    private fun changeQuery(change: () -> Unit) {
        val before = Triple(viewModel.sort.value, viewModel.genre.value, viewModel.watch.value)
        change()
        if (Triple(viewModel.sort.value, viewModel.genre.value, viewModel.watch.value) != before) {
            focusGridOnReload = true
        }
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
                changeQuery { viewModel.setGenre(if (idx == 0) null else genres[idx - 1]) }
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
                changeQuery { viewModel.setSort(opt.first, opt.second) }
                d.dismiss()
            }
            .show()
    }
}
