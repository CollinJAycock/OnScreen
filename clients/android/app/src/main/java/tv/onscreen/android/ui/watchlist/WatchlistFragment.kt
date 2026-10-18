package tv.onscreen.android.ui.watchlist

import android.app.AlertDialog
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import androidx.leanback.app.BrowseSupportFragment
import androidx.leanback.widget.ArrayObjectAdapter
import androidx.leanback.widget.FocusHighlight
import androidx.leanback.widget.HeaderItem
import androidx.leanback.widget.ListRow
import androidx.leanback.widget.ListRowPresenter
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.WatchlistItem
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.common.dismissOnViewDestroyed
import tv.onscreen.android.ui.common.syncItems
import tv.onscreen.android.ui.detail.DetailFragment
import javax.inject.Inject

/**
 * The caller's watchlist: one row per watch-status (Plan to Watch, Watching,
 * On Hold, Completed, Dropped), each headed with its count and paged 50 cards
 * at a time as the user scrolls along it. Empty statuses get no row.
 *
 * OK opens the title's detail page; a long-press of OK opens a menu to move
 * the card to another status or take it off the list. Coming back reloads
 * (the status may have changed meanwhile) and puts focus back on the card
 * the user left from.
 */
@AndroidEntryPoint
class WatchlistFragment : BrowseSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: WatchlistViewModel
    private var errorOverlay: ErrorOverlay? = null
    private var cardPresenter: CardPresenter? = null
    private var rowsAdapter: ArrayObjectAdapter? = null

    // (status, total) per rendered row. Rows are rebuilt only when this
    // changes — a rebuild reassigns the adapter and snaps focus to the top,
    // so an unchanged refresh just syncs each row's cards in place.
    private var renderedShape: List<Pair<String, Int>>? = null

    // The card focus was last on. Lives on the fragment instance, so it
    // survives the view being torn down by the detail page on top of it.
    private var lastStatus: String? = null
    private var lastItemId: String? = null
    private var lastRow = 0
    private var lastIndex = 0
    private var restorePending = false

    // The ViewModel loads on creation; onResume reloads only on a return.
    private var resumedOnce = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title = getString(R.string.watchlist)
        headersState = HEADERS_DISABLED
        brandColor = resources.getColor(R.color.bg_secondary, null)
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
        viewModel = ViewModelProvider(this)[WatchlistViewModel::class.java]
        rowsAdapter = null
        renderedShape = null
        restorePending = lastStatus != null

        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.events.collect { event ->
                when (event) {
                    is WatchlistEvent.ChangeFailed -> Toast.makeText(
                        requireContext(),
                        if (event.rateLimited) R.string.watch_rate_limited else R.string.watchlist_change_failed,
                        Toast.LENGTH_SHORT,
                    ).show()
                }
            }
        }

        // serverUrl first, then the state: CardPresenter needs it for the
        // poster URLs (see the same ordering note in HomeFragment).
        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = prefs.serverUrl.first() ?: ""
            cardPresenter = CardPresenter(requireContext(), serverUrl) { item ->
                if (item is WatchlistItem) showStatusMenu(item)
            }
            viewModel.uiState.collectLatest { state ->
                render(state)
                when {
                    state.error != null && state.rows.isEmpty() ->
                        errorOverlay?.show(state.error) { viewModel.load() }
                    state.loaded && state.rows.isEmpty() ->
                        errorOverlay?.showEmpty(R.string.empty_watchlist_title, R.string.empty_watchlist_message)
                    else -> errorOverlay?.hide()
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            if (item is WatchlistItem) openDetails(item)
        }

        setOnItemViewSelectedListener { _, item, _, row ->
            if (item !is WatchlistItem || row !is ListRow) return@setOnItemViewSelectedListener
            val status = WatchlistUi.STATUSES.getOrNull(row.headerItem.id.toInt()) ?: return@setOnItemViewSelectedListener
            val rowItems = row.adapter as? ArrayObjectAdapter ?: return@setOnItemViewSelectedListener
            val index = rowItems.indexOf(item)
            // Ignored while a restore is pending: populating the rows selects
            // the first card and would overwrite the card we're returning to.
            if (!restorePending) {
                lastStatus = status
                lastItemId = item.id
                lastRow = selectedPosition
                lastIndex = index
            }
            // Prefetch the row's next page as the selection nears its end.
            if (index >= 0 && index >= rowItems.size() - PREFETCH_DISTANCE) viewModel.loadMore(status)
        }
    }

    override fun onResume() {
        super.onResume()
        if (resumedOnce) viewModel.load() else resumedOnce = true
    }

    override fun onDestroyView() {
        cardPresenter = null
        rowsAdapter = null
        super.onDestroyView()
    }

    private fun render(state: WatchlistUiState) {
        val presenter = cardPresenter ?: return
        val shape = state.rows.map { it.status to it.total }
        val current = rowsAdapter
        if (current == null || shape != renderedShape) {
            val rows = ArrayObjectAdapter(
                ListRowPresenter(FocusHighlight.ZOOM_FACTOR_NONE).apply {
                    shadowEnabled = false
                    selectEffectEnabled = false
                },
            )
            state.rows.forEach { row ->
                val label = WatchlistUi.labelRes(row.status)?.let { getString(it) } ?: row.status
                val cards = ArrayObjectAdapter(presenter)
                row.items.forEach { cards.add(it) }
                // Header id = the status's place in STATUSES, so a selected
                // row maps back to its status.
                val header = HeaderItem(
                    WatchlistUi.STATUSES.indexOf(row.status).toLong(),
                    getString(R.string.watchlist_row_header, label, row.total),
                )
                rows.add(ListRow(header, cards))
            }
            rowsAdapter = rows
            renderedShape = shape
            adapter = rows
            // A rebuild snaps focus to the first card: put it back on the
            // card the user was on (or where it was, if that card moved).
            if (lastStatus != null) restoreFocus(state)
        } else {
            state.rows.forEachIndexed { i, row ->
                val cards = (current.get(i) as? ListRow)?.adapter as? ArrayObjectAdapter ?: return@forEachIndexed
                cards.syncItems(row.items, updateChanged = true) { it.id }
            }
            if (restorePending && state.rows.isNotEmpty()) restoreFocus(state)
        }
    }

    private fun restoreFocus(state: WatchlistUiState) {
        restorePending = false
        val target = WatchlistUi.focusTarget(
            rows = state.rows.map { row -> row.status to row.items.map { it.id } },
            status = lastStatus,
            itemId = lastItemId,
            rowIndex = lastRow,
            itemIndex = lastIndex,
        ) ?: return
        setSelectedPosition(target.first, false, ListRowPresenter.SelectItemViewHolderTask(target.second))
    }

    private fun openDetails(item: WatchlistItem) {
        val fm = parentFragmentManager
        if (fm.isStateSaved) return
        // Photos have no detail page; their viewer is the closest thing.
        if (item.type == "photo") {
            Navigator.open(fm, item.id, item.type)
            return
        }
        fm.beginTransaction()
            .replace(R.id.main_container, DetailFragment.newInstance(item.id))
            .addToBackStack(null)
            .commit()
    }

    /**
     * Long-press menu: move the card to another status, or take it off the
     * list. A list dialog so the D-pad lands on the first action at once;
     * Back cancels.
     */
    private fun showStatusMenu(item: WatchlistItem) {
        if (!isAdded) return
        val targets = WatchlistUi.moveTargets(item.status)
        val labels = targets.map { status ->
            val label = WatchlistUi.labelRes(status)?.let { getString(it) } ?: status
            getString(R.string.watchlist_move_to, label)
        } + getString(R.string.watchlist_remove)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(item.title)
            .setItems(labels.toTypedArray()) { d, which ->
                d.dismiss()
                if (!isAdded) return@setItems
                // The last entry (past the targets) is Remove → null.
                viewModel.changeStatus(item, targets.getOrNull(which))
            }
            .create()
            .dismissOnViewDestroyed(this)
            .show()
    }

    companion object {
        /** Start fetching a row's next page this many cards from its end. */
        private const val PREFETCH_DISTANCE = 10
    }
}
