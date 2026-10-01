package tv.onscreen.android.ui.browse

import android.app.AlertDialog
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import androidx.leanback.app.BrowseSupportFragment
import androidx.leanback.widget.*
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.BuildConfig
import tv.onscreen.android.R
import tv.onscreen.android.data.model.*
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.MainActivity
import tv.onscreen.android.ui.NavigationDestination
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.dismissOnViewDestroyed
import tv.onscreen.android.ui.common.focusableOnTv
import tv.onscreen.android.ui.common.GridScrollMemory
import tv.onscreen.android.ui.common.NavCard
import tv.onscreen.android.ui.common.NavCardPresenter
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.common.ViewAllCard
import tv.onscreen.android.ui.common.ViewAllCardPresenter
import tv.onscreen.android.ui.favorites.FavoritesFragment
import tv.onscreen.android.ui.history.HistoryFragment
import tv.onscreen.android.ui.livetv.LiveTVFragment
import tv.onscreen.android.ui.livetv.RecordingsFragment
import tv.onscreen.android.ui.search.SearchFragment
import tv.onscreen.android.ui.settings.SettingsFragment
import androidx.leanback.widget.FocusHighlight
import javax.inject.Inject

@AndroidEntryPoint
class HomeFragment : BrowseSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var watchNext: tv.onscreen.android.playback.WatchNextManager
    private lateinit var viewModel: HomeViewModel
    // Only for the "Change server" escape hatch below, which needs the same
    // full teardown (revoke + Watch Next purge + stop background audio) that
    // SettingsFragment performs. Activity-scoped so the ViewModel — and the
    // coroutine it runs on — survive this fragment's view being torn down by
    // the navigation that immediately follows.
    private val settingsViewModel by lazy {
        ViewModelProvider(requireActivity())[
            tv.onscreen.android.ui.settings.SettingsViewModel::class.java,
        ]
    }
    private var serverUrl: String = ""
    private var errorOverlay: ErrorOverlay? = null
    // Last state we actually rendered. onResume re-fetches, but if the content is
    // identical we skip rebuilding the rows — a rebuild reassigns the adapter and
    // snaps the Browse fragment's focus/scroll back to the first row.
    private var lastBuiltState: HomeUiState? = null
    // Returning from a sub-screen recreates this view and (when content changed)
    // rebuilds the rows from a fresh adapter, which snaps focus back to the first
    // row. Remember the selected row and re-apply it after the rebuild.
    private val scroll = GridScrollMemory()
    // Header of the row Home was left from. Rows above it come and go between
    // visits (Continue Watching, Next Up, Plan to Watch — a mark or a finished
    // episode on the detail screen changes them), so the saved index alone put
    // focus on a neighbouring row; the restore looks the row up by name first.
    private var leftFromRow: String? = null
    // (row, item) to put focus back on after the rebuild a Continue Watching
    // removal triggers — without it the rebuilt adapter snaps focus to the
    // first row, far from where the user just was.
    private var pendingFocus: Pair<Int, Int>? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title = getString(R.string.app_name)
        headersState = HEADERS_ENABLED
        isHeadersTransitionOnBackEnabled = true
        brandColor = resources.getColor(R.color.bg_secondary, null)
        searchAffordanceColor = resources.getColor(R.color.accent, null)
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
        viewModel = ViewModelProvider(this)[HomeViewModel::class.java]
        // Arm on every return (leftFromRow set by onDestroyView), not only when
        // the saved index is nonzero: a row can appear above row 0 meanwhile.
        scroll.onViewRecreated(returning = leftFromRow != null)

        // One-shot results of Continue Watching removals.
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.events.collect { event ->
                when (event) {
                    // Also drop it from the launcher's Continue Watching row
                    // (Google TV Watch Next) — rows there are keyed by the
                    // played item, so this matches movies / other videos; a
                    // show tile's id finds nothing and is a no-op.
                    is HomeEvent.Dismissed -> lifecycleScope.launch(kotlinx.coroutines.Dispatchers.IO) {
                        runCatching { watchNext.remove(event.itemId) }
                    }
                    is HomeEvent.DismissFailed -> Toast.makeText(
                        requireContext(),
                        if (event.rateLimited) R.string.watch_rate_limited else R.string.cw_remove_failed,
                        Toast.LENGTH_SHORT,
                    ).show()
                }
            }
        }

        // Read serverUrl FIRST, then start collecting UI state. Two
        // parallel coroutines raced before — if the cached API response
        // landed before the prefs read finished, buildRows ran with
        // serverUrl="" and CardPresenter fell through to the
        // "no-url" branch, setting a flat colour tile in place of every
        // poster. Symptom: black tiles on Fire TV (where the race lost
        // reliably); intermittent on faster boot paths elsewhere.
        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""
            viewModel.uiState.collectLatest { state ->
                if (state.isLoading) return@collectLatest
                val hasContent = state.continueWatchingTV.isNotEmpty() ||
                    state.continueWatchingMovies.isNotEmpty() ||
                    state.continueWatchingOther.isNotEmpty() ||
                    state.recentlyAdded.isNotEmpty() ||
                    state.trending.isNotEmpty() ||
                    state.nextUp.isNotEmpty() ||
                    state.planToWatch.isNotEmpty() ||
                    state.libraryPreviews.any { it.second.isNotEmpty() } ||
                    state.collections.isNotEmpty()
                if (state.error != null && !hasContent) {
                    errorOverlay?.show(
                        state.error,
                        onRetry = { viewModel.load() },
                        // Escape hatch for a wrong/stale server URL: forget
                        // the server and return to the setup screen, instead
                        // of being trapped retrying a dead endpoint.
                        onChangeServer = {
                            // Confirm first: this wipes the server URL AND all
                            // tokens, costing a full re-setup + re-login on a
                            // D-pad — and the button sits one DOWN press from
                            // the focused Retry on a screen the user reached by
                            // a transient network error. SettingsFragment's
                            // equivalent action has always been confirmed.
                            AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
                                .setTitle(R.string.change_server)
                                .setMessage(R.string.confirm_change_server)
                                .setPositiveButton(R.string.change_server) { d, _ ->
                                    d.dismiss()
                                    // Full teardown, same as SettingsFragment's
                                    // equivalent action. prefs.clearAll() alone
                                    // wipes only the LOCAL copies: it skipped the
                                    // server-side revoke (leaving the 30-day
                                    // refresh token, the access token and the
                                    // 24h user-scoped asset token alive for their
                                    // full TTLs), skipped the identity-cache
                                    // invalidation, skipped stopping parked
                                    // background audio, and — the one that
                                    // reliably bit — skipped watchNext.removeAll(),
                                    // so the departing user's titles, resume
                                    // positions and working deep links stayed on
                                    // the launcher's Continue Watching strip for
                                    // the next person to pick up the remote.
                                    // Clearing local tokens does not touch the
                                    // launcher's database.
                                    //
                                    // activity scope, not viewLifecycleOwner:
                                    // navigateTo tears this fragment's view down
                                    // mid-flow, which would cancel the revoke
                                    // half-way (the same reason SettingsFragment
                                    // uses the activity scope here).
                                    (activity as? MainActivity)?.lifecycleScope?.launch {
                                        settingsViewModel.logout()
                                        prefs.clearAll()
                                        (activity as? MainActivity)
                                            ?.navigateTo(NavigationDestination.SERVER_SETUP)
                                    }
                                }
                                .setNegativeButton(R.string.cancel) { d, _ -> d.dismiss() }
                                .create()
                                .focusableOnTv()
                                .dismissOnViewDestroyed(this@HomeFragment)
                                .show()
                        },
                    )
                } else {
                    // NOTE: Home must NOT use ErrorOverlay.showEmpty.
                    //
                    // It did briefly, and that was a trap. showEmpty hides both
                    // of the overlay's buttons and the overlay is opaque and
                    // takes focus — and because it replaced the buildRows call
                    // rather than accompanying it, the Browse strip never got
                    // built. That strip is the ONLY place SettingsFragment is
                    // instantiated in the whole app, so Settings, Sign out and
                    // Change server all became unreachable; BACK on Home exits
                    // to the launcher and relaunching lands in the same state.
                    // Recovery was Clear Data.
                    //
                    // The other six screens can use showEmpty safely because
                    // they are pushed with addToBackStack and BACK returns to
                    // Home. Home is the root — it has no "back" — so its empty
                    // state is a ROW inside the normal row set (see buildRows),
                    // leaving every navigation affordance on screen.
                    errorOverlay?.hide()
                    if (state != lastBuiltState) {
                        lastBuiltState = state
                        buildRows(state)
                        pendingFocus?.let { (row, index) ->
                            pendingFocus = null
                            restoreFocus(row, index)
                        }
                    }
                    // Returning here recreated the view: a rebuild (content changed)
                    // reassigns the adapter and a skipped rebuild (unchanged) reuses
                    // the retained one — either way put focus back on the row the user
                    // left from instead of snapping to the top. By name first —
                    // the saved index is off by one per row that came or went
                    // above it, and out of range when the user left from the
                    // last row — falling back to the index.
                    val rows = (adapter as? ArrayObjectAdapter)?.size() ?: 0
                    scroll.restoreIfPending(rows, resolve = { rowNamed(leftFromRow) }) { setSelectedPosition(it) }
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            when (item) {
                is HubItem -> {
                    val resumeMs = item.view_offset_ms ?: 0
                    // Resume dialog only makes sense for leaf items that
                    // skip the detail page — episodes, tracks, etc. land
                    // straight in playback so we ask up front. Movies
                    // now route to the detail page (which has its own
                    // Resume / Play From Start buttons), so a dialog
                    // here would be a redundant extra step.
                    val skipDialog = item.type == "show" || item.type == "movie" || item.type == "photo"
                    if (resumeMs > 0 && !skipDialog) {
                        showResumeDialog(resumeMs) { offset ->
                            Navigator.open(parentFragmentManager, item.id, item.type, offset)
                        }
                    } else {
                        Navigator.open(parentFragmentManager, item.id, item.type, 0)
                    }
                }
                is MediaItem -> {
                    Navigator.open(parentFragmentManager, item.id, item.type, 0)
                }
                is SearchResult -> {
                    Navigator.open(parentFragmentManager, item.id, item.type, 0)
                }
                is MediaCollection -> {
                    parentFragmentManager.beginTransaction()
                        .replace(R.id.main_container, CollectionFragment.newInstance(item.id, item.name))
                        .addToBackStack(null)
                        .commit()
                }
                is ViewAllCard -> {
                    parentFragmentManager.beginTransaction()
                        .replace(
                            R.id.main_container,
                            LibraryFragment.newInstance(item.libraryId, item.libraryName, item.libraryType),
                        )
                        .addToBackStack(null)
                        .commit()
                }
                is NavCard -> {
                    val fragment = when (item.id) {
                        NAV_FAVORITES -> FavoritesFragment()
                        NAV_HISTORY -> HistoryFragment()
                        // Gated as well as the cards themselves, so the
                        // firetv flavor has no path to the Live TV screens
                        // and R8 can drop them (see the flavor comment in
                        // app/build.gradle.kts).
                        NAV_LIVE_TV -> if (BuildConfig.LIVE_TV) LiveTVFragment() else null
                        NAV_RECORDINGS -> if (BuildConfig.LIVE_TV) RecordingsFragment() else null
                        NAV_SETTINGS -> SettingsFragment()
                        else -> null
                    }
                    if (fragment != null) {
                        parentFragmentManager.beginTransaction()
                            .replace(R.id.main_container, fragment)
                            .addToBackStack(null)
                            .commit()
                    }
                }
            }
        }

        // Track which row is focused so a rebuild-on-return can land back on it
        // instead of the top. (Browse's 4th `row` param is the Row; selectedPosition
        // is the simpler, equivalent row index.)
        setOnItemViewSelectedListener { _, _, _, _ -> scroll.record(selectedPosition) }

        setOnSearchClickedListener {
            parentFragmentManager.beginTransaction()
                .replace(R.id.main_container, SearchFragment())
                .addToBackStack(null)
                .commit()
        }
    }

    override fun onDestroyView() {
        val rows = adapter as? ArrayObjectAdapter
        leftFromRow = rows?.takeIf { selectedPosition in 0 until it.size() }
            ?.let { (it.get(selectedPosition) as? ListRow)?.headerItem?.name }
        super.onDestroyView()
    }

    /** Index of the row whose header is [name], or null. */
    private fun rowNamed(name: String?): Int? {
        val rows = adapter as? ArrayObjectAdapter ?: return null
        name ?: return null
        return (0 until rows.size()).firstOrNull { (rows.get(it) as? ListRow)?.headerItem?.name == name }
    }

    override fun onResume() {
        super.onResume()
        // Refresh unread count / new items after returning from sub-screens.
        if (::viewModel.isInitialized) viewModel.load()
    }

    private fun buildRows(state: HomeUiState) {
        val rowsAdapter = ArrayObjectAdapter(ListRowPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
            shadowEnabled = false
            selectEffectEnabled = false
        })
        val cardPresenter = CardPresenter(requireContext(), serverUrl)
        // Continue Watching cards get a long-press menu (Remove from Continue
        // Watching); every other row keeps the plain presenter.
        val continuePresenter = CardPresenter(requireContext(), serverUrl) { item ->
            if (item is HubItem) showContinueWatchingMenu(item)
        }
        val navPresenter = NavCardPresenter(requireContext())
        var headerId = 0L

        // ClassPresenterSelector lets a single library row mix item types — cards
        // for MediaItems plus a trailing "View all" tile that routes into the
        // dedicated LibraryFragment. Without the selector, ArrayObjectAdapter falls
        // back to the single presenter and renders ViewAllCard with the wrong layout.
        val viewAllPresenter = ViewAllCardPresenter(requireContext())
        val libraryRowSelector = ClassPresenterSelector().apply {
            addClassPresenter(MediaItem::class.java, cardPresenter)
            addClassPresenter(ViewAllCard::class.java, viewAllPresenter)
        }

        // A row whose items are HubItems (continue-watching, trending, recently
        // added). Returns null for an empty bucket so the row is skipped.
        fun hubRow(title: String, items: List<HubItem>, presenter: Presenter = cardPresenter): ListRow? {
            if (items.isEmpty()) return null
            val a = ArrayObjectAdapter(presenter)
            items.forEach { a.add(it) }
            return ListRow(HeaderItem(headerId++, title), a)
        }

        // Candidate home rows in DEFAULT order, each keyed so the user's saved hub
        // layout (configured on the web home, shared per-account via prefs) can
        // reorder + hide them. The web-shared keys are continue_*, next_up,
        // plan_to_watch, trending and library:<uuid>; recently_added / collections
        // are TV-only extras the web never emits, so they fall through to their
        // default position. Default order mirrors the web home: Next Up right
        // under Continue Watching TV, Plan to Watch after the Continue rows (the
        // server also slots both there in layouts saved before they existed).
        class Section(val key: String, val build: () -> ListRow?)
        val sections = buildList {
            add(Section("continue_tv") { hubRow(getString(R.string.continue_watching_tv), state.continueWatchingTV, continuePresenter) })
            add(Section("next_up") { hubRow(getString(R.string.hub_next_up), state.nextUp) })
            add(Section("continue_movies") { hubRow(getString(R.string.continue_watching_movies), state.continueWatchingMovies, continuePresenter) })
            add(Section("continue_other") { hubRow(getString(R.string.continue_watching), state.continueWatchingOther, continuePresenter) })
            add(Section("plan_to_watch") { hubRow(getString(R.string.hub_plan_to_watch), state.planToWatch) })
            add(Section("trending") { hubRow(getString(R.string.trending), state.trending) })
            add(Section("recently_added") { hubRow(getString(R.string.recently_added), state.recentlyAdded) })
            state.libraryPreviews.forEach { (library, items) ->
                add(
                    Section("library:${library.id}") {
                        if (items.isEmpty()) {
                            null
                        } else {
                            val a = ArrayObjectAdapter(libraryRowSelector)
                            items.forEach { a.add(it) }
                            // Trailing "View all" tile into the full library grid;
                            // carries the type so LibraryFragment can pick its sort.
                            a.add(ViewAllCard(library.id, library.name, library.type))
                            ListRow(HeaderItem(headerId++, library.name), a)
                        }
                    },
                )
            }
            add(
                Section("collections") {
                    if (state.collections.isEmpty()) {
                        null
                    } else {
                        val a = ArrayObjectAdapter(cardPresenter)
                        state.collections.forEach { a.add(it) }
                        ListRow(HeaderItem(headerId++, getString(R.string.collections)), a)
                    }
                },
            )
        }

        // Apply the saved layout: enabled sections first, in the saved order,
        // skipping keys with no matching row (a removed library, or the web-only
        // "libraries" tile grid the TV doesn't render); then any section not in the
        // saved layout, in default order. Mirrors the web home's ordering exactly.
        val byKey = sections.associateBy { it.key }
        val used = mutableSetOf<String>()
        val ordered = mutableListOf<Section>()
        for (pref in state.hubLayout) {
            val s = byKey[pref.key] ?: continue
            used.add(pref.key)
            if (pref.enabled) ordered.add(s)
        }
        for (s in sections) if (s.key !in used) ordered.add(s)
        ordered.forEach { section -> section.build()?.let { rowsAdapter.add(it) } }

        // Empty-state row, FIRST so it reads as the page's message — but as a
        // row, not an overlay, so everything below stays reachable.
        val hasContent = state.continueWatchingTV.isNotEmpty() ||
            state.continueWatchingMovies.isNotEmpty() ||
            state.continueWatchingOther.isNotEmpty() ||
            state.recentlyAdded.isNotEmpty() ||
            state.trending.isNotEmpty() ||
            state.nextUp.isNotEmpty() ||
            state.planToWatch.isNotEmpty() ||
            state.libraryPreviews.any { it.second.isNotEmpty() } ||
            state.collections.isNotEmpty()
        if (!hasContent) {
            // Two SHORT header rows: Leanback's header dock is ~270dp and
            // hard-clips with no ellipsis, so the old one-line 84-char
            // sentence cut off mid-word. Each of these fits the dock.
            rowsAdapter.add(
                ListRow(
                    HeaderItem(headerId++, getString(R.string.empty_home_title)),
                    ArrayObjectAdapter(cardPresenter),
                ),
            )
            rowsAdapter.add(
                ListRow(
                    HeaderItem(headerId++, getString(R.string.empty_home_hint)),
                    ArrayObjectAdapter(cardPresenter),
                ),
            )
        }

        // Browse row: Favorites / History / [Live TV / Recordings] / Settings.
        // The Fire TV build has no Live TV or Recordings (BuildConfig.LIVE_TV;
        // see the flavor comment in app/build.gradle.kts).
        val navAdapter = ArrayObjectAdapter(navPresenter)
        navAdapter.add(NavCard(NAV_FAVORITES, getString(R.string.favorites), R.drawable.ic_heart_filled))
        navAdapter.add(NavCard(NAV_HISTORY, getString(R.string.history), R.drawable.ic_history))
        if (BuildConfig.LIVE_TV) {
            navAdapter.add(NavCard(NAV_LIVE_TV, getString(R.string.live_tv), R.drawable.ic_live_tv))
            navAdapter.add(NavCard(NAV_RECORDINGS, getString(R.string.recordings), R.drawable.ic_recordings))
        }
        navAdapter.add(NavCard(NAV_SETTINGS, getString(R.string.settings), R.drawable.ic_settings))
        rowsAdapter.add(ListRow(HeaderItem(headerId++, getString(R.string.browse)), navAdapter))

        adapter = rowsAdapter
    }

    /**
     * Long-press menu for a Continue Watching card. A list dialog (not a
     * button-only one) so the D-pad lands on the action straight away; Back
     * cancels. Removal is optimistic — the card disappears at once and comes
     * back with a toast if the server call fails.
     */
    private fun showContinueWatchingMenu(item: HubItem) {
        if (!isAdded) return
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(item.title)
            .setItems(arrayOf(getString(R.string.cw_remove))) { d, _ ->
                d.dismiss()
                if (!isAdded) return@setItems
                pendingFocus = locate(item)
                viewModel.dismissContinueWatching(item)
            }
            .create()
            .dismissOnViewDestroyed(this)
            .show()
    }

    /** (row, index-in-row) of [item] in the current rows, or null. */
    private fun locate(item: Any): Pair<Int, Int>? {
        val rows = adapter as? ArrayObjectAdapter ?: return null
        for (r in 0 until rows.size()) {
            val row = rows.get(r) as? ListRow ?: continue
            val index = (row.adapter as? ArrayObjectAdapter)?.indexOf(item) ?: -1
            if (index >= 0) return r to index
        }
        return null
    }

    /** Select row [row] (clamped) and the card at [index] within it (clamped
     *  to the row's new size — the removed card's neighbour slides in). */
    private fun restoreFocus(row: Int, index: Int) {
        val rows = adapter as? ArrayObjectAdapter ?: return
        if (rows.size() == 0) return
        val r = row.coerceIn(0, rows.size() - 1)
        val rowSize = ((rows.get(r) as? ListRow)?.adapter?.size() ?: 0)
        val i = index.coerceIn(0, (rowSize - 1).coerceAtLeast(0))
        setSelectedPosition(r, false, ListRowPresenter.SelectItemViewHolderTask(i))
    }

    private fun showResumeDialog(resumeMs: Long, onChoice: (Long) -> Unit) {
        val tc = fmtTimecode(resumeMs)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.resume_watching)
            .setPositiveButton(getString(R.string.resume_from, tc)) { d, _ ->
                d.dismiss(); if (isAdded) onChoice(resumeMs)
            }
            .setNegativeButton(R.string.start_over) { d, _ ->
                d.dismiss(); if (isAdded) onChoice(0)
            }
            .create()
            .focusableOnTv()
            .dismissOnViewDestroyed(this)
            .show()
    }

    private fun fmtTimecode(ms: Long): String {
        val totalSec = ms / 1000
        val h = totalSec / 3600
        val m = (totalSec % 3600) / 60
        val s = totalSec % 60
        return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
    }

    companion object {
        private const val NAV_FAVORITES = "favorites"
        private const val NAV_HISTORY = "history"
        private const val NAV_LIVE_TV = "live_tv"
        private const val NAV_RECORDINGS = "recordings"
        private const val NAV_SETTINGS = "settings"
    }
}
