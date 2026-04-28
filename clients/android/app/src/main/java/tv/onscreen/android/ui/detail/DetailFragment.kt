package tv.onscreen.android.ui.detail

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.HorizontalScrollView
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import coil.load
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.artworkUrl
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.playback.PlaybackFragment
import javax.inject.Inject

@AndroidEntryPoint
class DetailFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: DetailViewModel
    private var serverUrl: String = ""
    private var episodeAdapter: EpisodeAdapter? = null
    private var currentSeasonId: String? = null
    private var seasonMap: Map<ChildItem, List<ChildItem>> = emptyMap()
    /** Guards re-binding when only the favorite flag toggles. Reset in
     *  onDestroyView — the value is meaningful per-view, but the field
     *  itself survives the fragment instance, so coming back from the
     *  back stack would otherwise leave the freshly-recreated view
     *  empty (poster, fanart, episode list never bound). */
    private var detailBound = false
    private var errorOverlay: ErrorOverlay? = null

    companion object {
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_SIBLING_IDS = "sibling_ids"
        private const val ARG_CURRENT_INDEX = "current_index"

        fun newInstance(itemId: String): DetailFragment {
            return DetailFragment().apply {
                arguments = Bundle().apply { putString(ARG_ITEM_ID, itemId) }
            }
        }

        fun newInstance(itemId: String, siblingIds: ArrayList<String>, currentIndex: Int): DetailFragment {
            return DetailFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_ITEM_ID, itemId)
                    putStringArrayList(ARG_SIBLING_IDS, siblingIds)
                    putInt(ARG_CURRENT_INDEX, currentIndex)
                }
            }
        }
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View {
        val inner = inflater.inflate(R.layout.fragment_detail, container, false)
        val overlay = ErrorOverlay.wrap(inner)
        errorOverlay = overlay
        return overlay.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[DetailViewModel::class.java]

        val itemId = arguments?.getString(ARG_ITEM_ID) ?: return
        val siblingIds = arguments?.getStringArrayList(ARG_SIBLING_IDS)
        val currentIndex = arguments?.getInt(ARG_CURRENT_INDEX, -1) ?: -1

        view.findViewById<ImageView>(R.id.btn_left).apply {
            if (siblingIds != null && currentIndex > 0) {
                visibility = View.VISIBLE
                setOnClickListener { navigateToSibling(siblingIds, currentIndex - 1) }
            }
        }
        view.findViewById<ImageView>(R.id.btn_right).apply {
            if (siblingIds != null && currentIndex in 0 until siblingIds.size - 1) {
                visibility = View.VISIBLE
                setOnClickListener { navigateToSibling(siblingIds, currentIndex + 1) }
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first() ?: ""
            viewModel.load(itemId)

            viewModel.uiState.collectLatest { state ->
                if (state.error != null && state.item == null) {
                    errorOverlay?.show(state.error) { viewModel.load(itemId) }
                    return@collectLatest
                }
                errorOverlay?.hide()
                if (state.item == null) return@collectLatest
                if (!detailBound) {
                    bindDetail(view, state.item, state.seasons)
                    detailBound = true
                }
                bindFavorite(view, state.isFavorite)
            }
        }
    }

    private fun bindDetail(root: View, item: ItemDetail, seasons: Map<ChildItem, List<ChildItem>>) {
        val fanart = root.findViewById<ImageView>(R.id.fanart)
        val poster = root.findViewById<ImageView>(R.id.poster)
        val titleView = root.findViewById<TextView>(R.id.title)
        val subtitleView = root.findViewById<TextView>(R.id.subtitle)
        val summaryView = root.findViewById<TextView>(R.id.summary)
        val btnPlay = root.findViewById<Button>(R.id.btn_play)
        val btnFromStart = root.findViewById<Button>(R.id.btn_play_from_start)

        if (!item.poster_path.isNullOrEmpty() && serverUrl.isNotEmpty()) {
            poster.load(artworkUrl(serverUrl, item.poster_path, 800)) {
                crossfade(true); placeholder(R.color.bg_elevated); error(R.color.bg_elevated)
            }
        }
        val fanartPath = item.fanart_path ?: item.poster_path
        if (!fanartPath.isNullOrEmpty() && serverUrl.isNotEmpty()) {
            fanart.load(artworkUrl(serverUrl, fanartPath, 1280)) { crossfade(true) }
        }

        titleView.text = item.title

        val parts = mutableListOf<String>()
        item.year?.let { parts.add(it.toString()) }
        item.content_rating?.let { parts.add(it) }
        item.duration_ms?.let { ms ->
            val min = ms / 60_000
            parts.add(if (min >= 60) "${min / 60}h ${min % 60}m" else "${min}m")
        }
        item.rating?.let { parts.add("★ %.1f".format(it)) }
        if (item.genres.isNotEmpty()) parts.add(item.genres.take(3).joinToString(", "))
        subtitleView.text = parts.joinToString(" · ")
        subtitleView.visibility = if (parts.isEmpty()) View.GONE else View.VISIBLE

        if (!item.summary.isNullOrEmpty()) {
            summaryView.text = item.summary
            summaryView.visibility = View.VISIBLE
        } else {
            summaryView.visibility = View.GONE
        }

        configurePlayButtons(item, btnPlay, btnFromStart)
        configureEpisodes(root, item, seasons)
    }

    private fun configurePlayButtons(item: ItemDetail, btnPlay: Button, btnFromStart: Button) {
        when (item.type) {
            "show", "season", "album", "podcast" -> {
                // Container: Play picks an in-progress / first-unwatched
                // child (episode for show, track for album, episode for
                // podcast). The container itself has no `files` so
                // playItem(item.id) would error with "No playable file."
                btnPlay.text = getString(R.string.play)
                btnPlay.setOnClickListener {
                    val target = inProgressEpisode() ?: firstUnwatchedEpisode() ?: firstEpisode()
                    if (target != null) {
                        playItem(target.id, target.view_offset_ms)
                    }
                }
                btnFromStart.visibility = View.GONE
            }
            "artist" -> {
                // Artist children are albums (containers), not tracks.
                // A "Play All" experience would need recursive
                // traversal (first track of first album); skip until
                // that UX lands. For now the user picks an album from
                // the grid below.
                btnPlay.visibility = View.GONE
                btnFromStart.visibility = View.GONE
            }
            else -> {
                val resumeMs = item.view_offset_ms
                if (resumeMs > 0) {
                    btnPlay.text = getString(R.string.resume, fmtTimecode(resumeMs))
                    btnPlay.setOnClickListener { playItem(item.id, resumeMs) }
                    btnFromStart.visibility = View.VISIBLE
                    btnFromStart.setOnClickListener { playItem(item.id, 0L) }
                } else {
                    btnPlay.text = getString(R.string.play)
                    btnPlay.setOnClickListener { playItem(item.id, 0L) }
                    btnFromStart.visibility = View.GONE
                }
            }
        }
        if (btnPlay.visibility == View.VISIBLE) btnPlay.requestFocus()
    }

    private fun firstEpisode(): ChildItem? {
        val episodes = seasonMap.values.firstOrNull { it.isNotEmpty() } ?: return null
        return episodes.firstOrNull()
    }

    private fun allEpisodesInOrder(): List<ChildItem> {
        val seasons = seasonMap.entries.sortedBy { it.key.index ?: Int.MAX_VALUE }
        return seasons.flatMap { (_, eps) -> eps.sortedBy { it.index ?: Int.MAX_VALUE } }
    }

    private fun inProgressEpisode(): ChildItem? =
        allEpisodesInOrder().firstOrNull { !it.watched && it.view_offset_ms > 0 }

    private fun firstUnwatchedEpisode(): ChildItem? =
        allEpisodesInOrder().firstOrNull { !it.watched }

    private fun configureEpisodes(root: View, item: ItemDetail, seasons: Map<ChildItem, List<ChildItem>>) {
        val header = root.findViewById<TextView>(R.id.episodes_header)
        val tabsScroll = root.findViewById<HorizontalScrollView>(R.id.season_tabs_scroll)
        val tabsContainer = root.findViewById<LinearLayout>(R.id.season_tabs)
        val list = root.findViewById<RecyclerView>(R.id.episode_list)

        seasonMap = seasons

        if (seasons.isEmpty()) {
            header.visibility = View.GONE
            tabsScroll.visibility = View.GONE
            list.visibility = View.GONE
            return
        }

        // Header label depends on what the children represent. The
        // layout id stays "episodes_header" for backwards compat;
        // the visible text is the right one per type.
        header.text = getString(when (item.type) {
            "album" -> R.string.tracks
            "artist" -> R.string.albums
            else -> R.string.episodes
        })
        header.visibility = View.VISIBLE
        // Season tabs are show-specific. Albums and podcasts have a
        // single child group; rendering tabs there is meaningless.
        tabsScroll.visibility = if (seasons.size > 1 && item.type == "show") View.VISIBLE else View.GONE
        list.visibility = View.VISIBLE

        if (episodeAdapter == null) {
            // Route child clicks through Navigator so containers (an
            // artist's albums) drill into their own detail screen
            // instead of being mis-played as media. Tracks / episodes
            // / podcast episodes still hit PlaybackFragment via
            // Navigator's else branch.
            episodeAdapter = EpisodeAdapter(serverUrl) { child ->
                Navigator.open(parentFragmentManager, child.id, child.type, child.view_offset_ms)
            }
            list.layoutManager = LinearLayoutManager(requireContext(), LinearLayoutManager.HORIZONTAL, false)
            list.adapter = episodeAdapter
        }

        if (currentSeasonId == null) {
            currentSeasonId = seasons.keys.firstOrNull()?.id
        }

        tabsContainer.removeAllViews()
        seasons.keys.forEachIndexed { i, season ->
            val pill = LayoutInflater.from(requireContext()).inflate(android.R.layout.simple_list_item_1, tabsContainer, false) as TextView
            pill.apply {
                background = resources.getDrawable(R.drawable.tab_pill, null)
                text = season.index?.let { getString(R.string.season_n, it) } ?: season.title
                setTextColor(resources.getColor(R.color.text_primary, null))
                textSize = 13f
                setPadding(36, 16, 36, 16)
                isFocusable = true
                isFocusableInTouchMode = true
                isSelected = season.id == currentSeasonId
                setOnClickListener {
                    currentSeasonId = season.id
                    for (ci in 0 until tabsContainer.childCount) {
                        tabsContainer.getChildAt(ci).isSelected = (ci == i)
                    }
                    episodeAdapter?.submit(seasons[season] ?: emptyList())
                }
            }
            val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT)
            lp.marginEnd = 16
            tabsContainer.addView(pill, lp)
        }

        val activeSeason = seasons.entries.firstOrNull { it.key.id == currentSeasonId } ?: seasons.entries.first()
        episodeAdapter?.submit(activeSeason.value)
    }

    private fun playItem(itemId: String, startMs: Long) {
        parentFragmentManager.beginTransaction()
            .replace(R.id.main_container, PlaybackFragment.newInstance(itemId, startMs))
            .addToBackStack(null)
            .commit()
    }

    private fun fmtTimecode(ms: Long): String {
        val totalSec = ms / 1000
        val h = totalSec / 3600
        val m = (totalSec % 3600) / 60
        val s = totalSec % 60
        return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
    }

    private fun bindFavorite(root: View, isFavorite: Boolean) {
        val btn = root.findViewById<ImageView>(R.id.btn_favorite) ?: return
        btn.setImageResource(if (isFavorite) R.drawable.ic_heart_filled else R.drawable.ic_heart)
        btn.contentDescription = getString(if (isFavorite) R.string.unfavorite else R.string.favorite)
        btn.setOnClickListener { viewModel.toggleFavorite() }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        detailBound = false
        episodeAdapter = null
        errorOverlay = null
    }

    private fun navigateToSibling(siblingIds: ArrayList<String>, index: Int) {
        val targetId = siblingIds[index]
        parentFragmentManager.beginTransaction()
            .replace(R.id.main_container, newInstance(targetId, siblingIds, index))
            .commit()
    }
}
