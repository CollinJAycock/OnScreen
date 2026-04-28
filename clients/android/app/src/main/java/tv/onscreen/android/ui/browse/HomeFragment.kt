package tv.onscreen.android.ui.browse

import android.app.AlertDialog
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.leanback.app.BrowseSupportFragment
import androidx.leanback.widget.*
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.*
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.CardPresenter
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.NavCard
import tv.onscreen.android.ui.common.NavCardPresenter
import tv.onscreen.android.ui.detail.DetailFragment
import tv.onscreen.android.ui.favorites.FavoritesFragment
import tv.onscreen.android.ui.history.HistoryFragment
import tv.onscreen.android.ui.notifications.NotificationsFragment
import tv.onscreen.android.ui.playback.PlaybackFragment
import tv.onscreen.android.ui.search.SearchFragment
import tv.onscreen.android.ui.settings.SettingsFragment
import androidx.leanback.widget.FocusHighlight
import javax.inject.Inject

@AndroidEntryPoint
class HomeFragment : BrowseSupportFragment() {

    @Inject lateinit var prefs: ServerPrefs
    private lateinit var viewModel: HomeViewModel
    private var serverUrl: String = ""
    private var errorOverlay: ErrorOverlay? = null

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

        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""
        }

        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.uiState.collectLatest { state ->
                if (state.isLoading) return@collectLatest
                val hasContent = state.continueWatching.isNotEmpty() ||
                    state.recentlyAdded.isNotEmpty() ||
                    state.libraryPreviews.any { it.second.isNotEmpty() } ||
                    state.collections.isNotEmpty()
                if (state.error != null && !hasContent) {
                    errorOverlay?.show(state.error) { viewModel.load() }
                } else {
                    errorOverlay?.hide()
                    buildRows(state)
                }
            }
        }

        setOnItemViewClickedListener { _, item, _, _ ->
            when (item) {
                is HubItem -> {
                    if (item.type == "show") {
                        parentFragmentManager.beginTransaction()
                            .replace(R.id.main_container, DetailFragment.newInstance(item.id))
                            .addToBackStack(null)
                            .commit()
                    } else {
                        val resumeMs = item.view_offset_ms ?: 0
                        if (resumeMs > 0) {
                            showResumeDialog(resumeMs) { offset ->
                                parentFragmentManager.beginTransaction()
                                    .replace(R.id.main_container, PlaybackFragment.newInstance(item.id, offset))
                                    .addToBackStack(null)
                                    .commit()
                            }
                        } else {
                            parentFragmentManager.beginTransaction()
                                .replace(R.id.main_container, PlaybackFragment.newInstance(item.id, 0))
                                .addToBackStack(null)
                                .commit()
                        }
                    }
                }
                is MediaItem -> {
                    if (item.type == "show") {
                        parentFragmentManager.beginTransaction()
                            .replace(R.id.main_container, DetailFragment.newInstance(item.id))
                            .addToBackStack(null)
                            .commit()
                    } else {
                        parentFragmentManager.beginTransaction()
                            .replace(R.id.main_container, PlaybackFragment.newInstance(item.id, 0))
                            .addToBackStack(null)
                            .commit()
                    }
                }
                is SearchResult -> {
                    if (item.type == "show") {
                        parentFragmentManager.beginTransaction()
                            .replace(R.id.main_container, DetailFragment.newInstance(item.id))
                            .addToBackStack(null)
                            .commit()
                    } else {
                        parentFragmentManager.beginTransaction()
                            .replace(R.id.main_container, PlaybackFragment.newInstance(item.id, 0))
                            .addToBackStack(null)
                            .commit()
                    }
                }
                is MediaCollection -> {
                    parentFragmentManager.beginTransaction()
                        .replace(R.id.main_container, CollectionFragment.newInstance(item.id, item.name))
                        .addToBackStack(null)
                        .commit()
                }
                is NavCard -> {
                    val fragment = when (item.id) {
                        NAV_FAVORITES -> FavoritesFragment()
                        NAV_HISTORY -> HistoryFragment()
                        NAV_NOTIFICATIONS -> NotificationsFragment()
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

        setOnSearchClickedListener {
            parentFragmentManager.beginTransaction()
                .replace(R.id.main_container, SearchFragment())
                .addToBackStack(null)
                .commit()
        }
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
        val navPresenter = NavCardPresenter(requireContext())
        var headerId = 0L

        if (state.continueWatching.isNotEmpty()) {
            val listAdapter = ArrayObjectAdapter(cardPresenter)
            state.continueWatching.forEach { listAdapter.add(it) }
            val header = HeaderItem(headerId++, getString(R.string.continue_watching))
            rowsAdapter.add(ListRow(header, listAdapter))
        }

        if (state.recentlyAdded.isNotEmpty()) {
            val listAdapter = ArrayObjectAdapter(cardPresenter)
            state.recentlyAdded.forEach { listAdapter.add(it) }
            val header = HeaderItem(headerId++, getString(R.string.recently_added))
            rowsAdapter.add(ListRow(header, listAdapter))
        }

        state.libraryPreviews.forEach { (library, items) ->
            if (items.isNotEmpty()) {
                val listAdapter = ArrayObjectAdapter(cardPresenter)
                items.forEach { listAdapter.add(it) }
                val header = HeaderItem(headerId++, library.name)
                rowsAdapter.add(ListRow(header, listAdapter))
            }
        }

        if (state.collections.isNotEmpty()) {
            val listAdapter = ArrayObjectAdapter(cardPresenter)
            state.collections.forEach { listAdapter.add(it) }
            val header = HeaderItem(headerId++, getString(R.string.collections))
            rowsAdapter.add(ListRow(header, listAdapter))
        }

        // Browse row: Favorites / History / Notifications / Settings.
        val navAdapter = ArrayObjectAdapter(navPresenter)
        navAdapter.add(NavCard(NAV_FAVORITES, getString(R.string.favorites), R.drawable.ic_heart_filled))
        navAdapter.add(NavCard(NAV_HISTORY, getString(R.string.history), R.drawable.ic_history))
        navAdapter.add(NavCard(NAV_NOTIFICATIONS, getString(R.string.notifications), R.drawable.ic_bell, state.unreadNotifications))
        navAdapter.add(NavCard(NAV_SETTINGS, getString(R.string.settings), R.drawable.ic_settings))
        rowsAdapter.add(ListRow(HeaderItem(headerId++, getString(R.string.browse)), navAdapter))

        adapter = rowsAdapter
    }

    private fun showResumeDialog(resumeMs: Long, onChoice: (Long) -> Unit) {
        val tc = fmtTimecode(resumeMs)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.resume_watching)
            .setPositiveButton(getString(R.string.resume_from, tc)) { d, _ ->
                d.dismiss(); onChoice(resumeMs)
            }
            .setNegativeButton(R.string.start_over) { d, _ ->
                d.dismiss(); onChoice(0)
            }
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
        private const val NAV_NOTIFICATIONS = "notifications"
        private const val NAV_SETTINGS = "settings"
    }
}
