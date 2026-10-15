package tv.onscreen.android.ui.detail

import android.app.AlertDialog
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.HorizontalScrollView
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
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
import tv.onscreen.android.data.artworkOriginalUrl
import tv.onscreen.android.data.artworkUrl
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.playback.MediaVersions
import tv.onscreen.android.playback.PlayQueue
import tv.onscreen.android.ui.common.ErrorOverlay
import tv.onscreen.android.ui.common.Navigator
import tv.onscreen.android.ui.common.WatchStateUi
import tv.onscreen.android.ui.common.dismissOnViewDestroyed
import tv.onscreen.android.ui.common.focusableOnTv
import tv.onscreen.android.ui.common.ReportProblem
import tv.onscreen.android.ui.common.ReportProblemDialog
import tv.onscreen.android.ui.playback.PlaybackFragment
import javax.inject.Inject

@AndroidEntryPoint
class DetailFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var watchNext: tv.onscreen.android.playback.WatchNextManager

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

    /** Last playback position handed back by the player via a fragment
     *  result. When set, it takes precedence over the loaded item's
     *  view_offset_ms when rendering the Resume button — load()'s server
     *  refetch on return can race the just-sent progress write, so this
     *  override guarantees the label reflects what the user just watched. */
    private var resumeOverrideMs: Long? = null

    /** Up-next episode to scroll the episode list to on the first bind of a
     *  show / season (the season tab holding it is opened too). */
    private var initialScrollEpisodeId: String? = null

    /** The version picked for a movie / episode with several
     *  (bindVersionPicker): the file Play starts. Kept for the fragment's
     *  life, so a return from the player keeps the pick. */
    private var selectedFileId: String? = null

    companion object {
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_SIBLING_IDS = "sibling_ids"
        private const val ARG_CURRENT_INDEX = "current_index"

        /** Fragment-result channel the player uses to hand the exact
         *  final position back when it exits, so the Resume label
         *  refreshes immediately on return. Without it the detail
         *  screen's server refetch races the just-sent progress write
         *  and re-renders the pre-playback offset (stale "Resume from"). */
        const val RESULT_PLAYBACK_PROGRESS = "detail_playback_progress"
        const val RESULT_KEY_ITEM_ID = "item_id"
        const val RESULT_KEY_POSITION_MS = "position_ms"

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

        // The player hands its final position back here on exit. Apply
        // it as an override and, if the detail is already on-screen,
        // refresh the play buttons immediately; otherwise bindDetail
        // picks the override up when the item finishes loading.
        parentFragmentManager.setFragmentResultListener(
            RESULT_PLAYBACK_PROGRESS, viewLifecycleOwner,
        ) { _, bundle ->
            if (bundle.getString(RESULT_KEY_ITEM_ID) != itemId) return@setFragmentResultListener
            val pos = bundle.getLong(RESULT_KEY_POSITION_MS, -1L)
            if (pos < 0L) return@setFragmentResultListener
            resumeOverrideMs = pos
            // `view` here is the onViewCreated root, captured by this
            // listener; viewLifecycleOwner keeps the listener alive only
            // while that view exists, so it's safe to use directly.
            val boundItem = viewModel.uiState.value.item
            if (boundItem != null) {
                configurePlayButtons(
                    boundItem,
                    view.findViewById(R.id.btn_play),
                    view.findViewById(R.id.btn_play_from_start),
                    focusPlay = false, // refresh-only: keep the user's current focus
                )
            }
        }

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
                bindWatchState(view, state)
            }
        }

        // One-shot results of the watched marks.
        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.events.collect { event ->
                when (event) {
                    is DetailEvent.Marked -> {
                        if (!event.all) {
                            // A mark clears the resume point server-side, so
                            // the position the player handed back is stale:
                            // Resume becomes Play.
                            resumeOverrideMs = null
                            viewModel.uiState.value.item?.let { item ->
                                // ...and the launcher's Continue Watching row
                                // (Google TV Watch Next) would keep offering
                                // a resume that no longer exists. Off the main
                                // thread: provider query + delete over binder.
                                lifecycleScope.launch(kotlinx.coroutines.Dispatchers.IO) {
                                    runCatching { watchNext.remove(item.id) }
                                }
                                configurePlayButtons(
                                    item,
                                    view.findViewById(R.id.btn_play),
                                    view.findViewById(R.id.btn_play_from_start),
                                    focusPlay = false,
                                )
                            }
                        }
                        toast(
                            when {
                                event.all && event.watched -> R.string.marked_all_watched
                                event.all -> R.string.marked_all_unwatched
                                event.watched -> R.string.marked_watched
                                else -> R.string.marked_unwatched
                            },
                        )
                    }
                    is DetailEvent.MarkFailed ->
                        toast(if (event.rateLimited) R.string.watch_rate_limited else R.string.mark_failed)
                }
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

        bindTitle(root, titleView, item)

        // Year · rating · runtime · genres · studios / networks (v2.6).
        val parts = DetailMeta.lineParts(item)
        subtitleView.text = parts.joinToString(" · ")
        subtitleView.visibility = if (parts.isEmpty()) View.GONE else View.VISIBLE

        if (!item.summary.isNullOrEmpty()) {
            summaryView.text = item.summary
            summaryView.visibility = View.VISIBLE
        } else {
            summaryView.visibility = View.GONE
        }

        // configureEpisodes FIRST: it is what assigns seasonMap, and
        // configurePlayButtons reads seasonMap to tell a multi-file audiobook
        // (a container with chapters) from a single-file one (a leaf). Run the
        // other way round, seasonMap was still its emptyMap() initialiser on
        // every first bind, so a multi-file book always took the leaf branch and
        // Play called playItem() with the CONTAINER id — which has no files, so
        // playback died with "No playable file".
        configureEpisodes(root, item, seasons)
        bindVersionPicker(root, item)
        configurePlayButtons(item, btnPlay, btnFromStart)
        bindInstantMix(root, item)
        bindReportProblem(root, item)
    }

    /** "Instant Mix" on an artist or album (v2.6). */
    private fun bindInstantMix(root: View, item: ItemDetail) {
        val btn = root.findViewById<Button>(R.id.btn_instant_mix) ?: return
        btn.visibility = if (item.type == "artist" || item.type == "album") View.VISIBLE else View.GONE
        btn.setOnClickListener { startInstantMix(item.id) }
    }

    /** Play an instant mix seeded from [seedId] (an artist, album or track)
     *  as a queue: DetailViewModel.instantMix makes it the PlayQueue. */
    private fun startInstantMix(seedId: String) {
        viewModel.instantMix(seedId) { firstTrackId ->
            if (!isAdded) return@instantMix
            if (firstTrackId == null) {
                toast(R.string.instant_mix_failed)
            } else {
                playItem(firstTrackId, 0L, keepQueue = true)
            }
        }
    }

    /** Long-press menu on an album's track row: an instant mix from it. */
    private fun showTrackMenu(track: ChildItem) {
        if (!isAdded) return
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(track.title)
            .setItems(arrayOf(getString(R.string.instant_mix))) { d, _ ->
                d.dismiss()
                if (isAdded) startInstantMix(track.id)
            }
            .create()
            .dismissOnViewDestroyed(this)
            .show()
    }

    /**
     * The title: a movie's or show's clear logo (v2.6, a transparent PNG
     * fetched at its own size) in place of the text once it has loaded; the
     * text stays when there is none or it fails. The logo carries the title
     * as its content description, so TalkBack still reads it.
     */
    private fun bindTitle(root: View, titleView: TextView, item: ItemDetail) {
        titleView.text = item.title
        titleView.visibility = View.VISIBLE
        val logoView = root.findViewById<ImageView>(R.id.title_logo) ?: return
        val logo = DetailMeta.logoPath(item)
        if (logo == null || serverUrl.isEmpty()) {
            logoView.visibility = View.GONE
            return
        }
        logoView.contentDescription = item.title
        logoView.load(artworkOriginalUrl(serverUrl, logo)) {
            listener(
                onSuccess = { _, _ ->
                    logoView.visibility = View.VISIBLE
                    titleView.visibility = View.GONE
                },
                onError = { _, _ ->
                    logoView.visibility = View.GONE
                    titleView.visibility = View.VISIBLE
                },
            )
        }
    }

    /**
     * "Version: …" on a movie or episode with more than one version
     * (MediaVersions: editions and encodes; a stacked movie's parts are one).
     * The pick is what Play / Resume start; the first listed (the server's
     * best) until then.
     */
    private fun bindVersionPicker(root: View, item: ItemDetail) {
        val btn = root.findViewById<Button>(R.id.btn_version) ?: return
        val versions = if (item.type == "movie" || item.type == "episode") {
            MediaVersions.versions(item.files)
        } else {
            emptyList()
        }
        if (versions.size < 2) {
            btn.visibility = View.GONE
            selectedFileId = null
            return
        }
        val current = versions.firstOrNull { it.fileId == selectedFileId } ?: versions.first()
        selectedFileId = current.fileId
        btn.visibility = View.VISIBLE
        btn.text = getString(R.string.version_label, current.label)
        btn.setOnClickListener {
            val checked = versions.indexOfFirst { it.fileId == selectedFileId }.coerceAtLeast(0)
            AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
                .setTitle(R.string.version)
                .setSingleChoiceItems(versions.map { it.label }.toTypedArray(), checked) { d, idx ->
                    d.dismiss()
                    if (!isAdded) return@setSingleChoiceItems
                    selectedFileId = versions[idx].fileId
                    btn.text = getString(R.string.version_label, versions[idx].label)
                }
                .create()
                .dismissOnViewDestroyed(this)
                .show()
        }
    }

    /** "Report a problem" on a movie / episode / show — ReportProblemDialog
     *  files the report against the item (and its first file, when it has
     *  one) and shows the caller's earlier reports. */
    private fun bindReportProblem(root: View, item: ItemDetail) {
        val btn = root.findViewById<Button>(R.id.btn_report_problem) ?: return
        btn.visibility = if (ReportProblem.isReportable(item.type)) View.VISIBLE else View.GONE
        btn.setOnClickListener {
            ReportProblemDialog.show(childFragmentManager, item.id, item.files.firstOrNull()?.id, item.title)
        }
    }

    private fun configurePlayButtons(item: ItemDetail, btnPlay: Button, btnFromStart: Button, focusPlay: Boolean = true) {
        // Audiobook is dual-shape: a single-file book has files of its
        // own (no children) and behaves like a leaf, while a multi-file
        // book has audiobook_chapter children and behaves like an
        // album. Pick the right branch at runtime by checking whether
        // the children load returned anything.
        // Read the freshly-loaded children, not the stale field — see the
        // ordering note in bindDetail.
        val isMultiFileAudiobook = item.type == "audiobook" &&
            seasonMap.values.any { it.isNotEmpty() }
        // Shows / seasons: the server says what Play starts (resume / next /
        // first / rewatch). Null when the up-next call failed or the server
        // predates it — then the generic container pick below applies.
        val upNext = if (WatchStateUi.isWatchContainer(item.type)) viewModel.uiState.value.upNext else null
        when {
            upNext != null -> {
                val action = WatchStateUi.upNextAction(upNext)
                btnFromStart.visibility = View.GONE
                if (action == null) {
                    // mode "none": no episodes, so no play action at all.
                    btnPlay.visibility = View.GONE
                } else {
                    btnPlay.visibility = View.VISIBLE
                    btnPlay.text = when (action.kind) {
                        WatchStateUi.UpNextAction.Kind.Resume ->
                            if (action.code.isNotEmpty()) getString(R.string.up_next_resume, action.code)
                            else getString(R.string.up_next_resume_plain)
                        WatchStateUi.UpNextAction.Kind.Play ->
                            if (action.code.isNotEmpty()) getString(R.string.up_next_play, action.code)
                            else getString(R.string.play)
                        WatchStateUi.UpNextAction.Kind.WatchAgain -> getString(R.string.up_next_watch_again)
                    }
                    btnPlay.contentDescription = upNext.episode?.title?.takeIf { it.isNotBlank() }
                        ?.let { "${btnPlay.text}, $it" }
                    btnPlay.setOnClickListener { playItem(action.episodeId, action.startMs) }
                }
            }
            item.type in setOf("show", "season", "album", "podcast") || isMultiFileAudiobook -> {
                // Container: Play picks an in-progress / first-unwatched
                // child (episode for show, track for album, episode for
                // podcast, chapter for audiobook). The container itself
                // has no `files` so playItem(item.id) would error with
                // "No playable file."
                btnPlay.visibility = View.VISIBLE
                btnPlay.text = getString(R.string.play)
                btnPlay.contentDescription = null
                btnPlay.setOnClickListener {
                    val target = inProgressEpisode() ?: firstUnwatchedEpisode() ?: firstEpisode()
                    if (target != null) {
                        playItem(target.id, target.view_offset_ms)
                    } else {
                        // Children haven't loaded (or there are none) — give
                        // feedback instead of a dead click that looks broken.
                        android.widget.Toast.makeText(
                            requireContext(), R.string.nothing_to_play, android.widget.Toast.LENGTH_SHORT,
                        ).show()
                    }
                }
                btnFromStart.visibility = View.GONE
            }
            item.type == "artist" -> {
                // Play All resolves to the first track of the
                // chronologically-first album; the player's
                // cross-album auto-advance chains through the rest of
                // the catalog. Shuffle picks a random track from any
                // album. Both reuse the existing single-item playItem
                // path — no queue plumbing required.
                btnPlay.visibility = View.VISIBLE
                btnPlay.text = getString(R.string.play_all)
                btnPlay.setOnClickListener {
                    viewModel.resolvePlayAllStart(item.id) { trackId ->
                        if (trackId != null) playItem(trackId, 0L)
                    }
                }
                btnFromStart.visibility = View.VISIBLE
                btnFromStart.text = getString(R.string.shuffle)
                btnFromStart.setOnClickListener {
                    viewModel.resolveShuffleStart(item.id) { trackId ->
                        if (trackId != null) playItem(trackId, 0L)
                    }
                }
            }
            item.type == "book_author" || item.type == "book_series" -> {
                // Pure browse parents — Play All on an author or
                // series would need a "first book → play first
                // chapter" double-resolve, and the user already has
                // to pick a book before playback makes sense (vs
                // music where Play All means "everything by this
                // artist"). Hide both buttons; the books list below
                // is the only affordance.
                btnPlay.visibility = View.GONE
                btnFromStart.visibility = View.GONE
                // Both play buttons are hidden for these browse-only parents, so
                // without this focus falls to the heart instead of the books list.
                // Best-effort: hand focus to the list once it's populated.
                if (focusPlay) {
                    btnPlay.post {
                        btnPlay.rootView.findViewById<RecyclerView>(R.id.episode_list)?.requestFocus()
                    }
                }
            }
            else -> {
                // Leaf items (movie, episode, track, single-file
                // audiobook, photo with files attached). Plays itself.
                // resumeOverrideMs (the position the player just handed
                // back) wins over the server-loaded offset so the label
                // is accurate the instant we return from the player.
                // The version picked (bindVersionPicker), read at the click.
                val resumeMs = resumeOverrideMs ?: item.view_offset_ms
                if (resumeMs > 0) {
                    btnPlay.text = getString(R.string.resume, fmtTimecode(resumeMs))
                    btnPlay.setOnClickListener { playItem(item.id, resumeMs, selectedFileId) }
                    btnFromStart.visibility = View.VISIBLE
                    btnFromStart.setOnClickListener { playItem(item.id, 0L, selectedFileId) }
                } else {
                    btnPlay.text = getString(R.string.play)
                    btnPlay.setOnClickListener { playItem(item.id, 0L, selectedFileId) }
                    btnFromStart.visibility = View.GONE
                }
            }
        }
        // Only grab focus on the initial bind. The post-playback result-listener
        // re-bind (which updates the Resume label) must NOT yank focus back up to
        // Play — that's what threw the user off the episode list on return.
        if (focusPlay && btnPlay.visibility == View.VISIBLE) btnPlay.requestFocus()
    }

    private fun firstEpisode(): ChildItem? {
        val episodes = seasonMap.values.firstOrNull { it.isNotEmpty() } ?: return null
        return episodes.firstOrNull()
    }

    /** Also an album's tracks (disc, then number), for its Play button. */
    private fun allEpisodesInOrder(): List<ChildItem> {
        val seasons = seasonMap.entries.sortedBy { it.key.index ?: Int.MAX_VALUE }
        return seasons.flatMap { (_, eps) -> eps.sortedWith(ChildItem.PLAY_ORDER) }
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
            "audiobook" -> R.string.chapters
            "book_author", "book_series" -> R.string.books
            else -> R.string.episodes
        })
        header.visibility = View.VISIBLE
        // An artist's tabs are different kinds of rows (Albums, Music Videos,
        // Appears On, Similar Artists), so the header names the open tab
        // rather than always reading "Albums".
        val headerFor: (ChildItem) -> Unit = { group -> if (item.type == "artist") header.text = group.title }
        // Tabs render whenever the children fall into more than one
        // group: shows with seasons (the original case), and artists
        // with both albums and music videos (per v2.0 — music_video
        // items hang off the artist with no album parent so they get
        // their own "Music Videos" tab next to "Albums"). Single-
        // group types (album, podcast, audiobook chapters, book
        // series) keep the bare list with no tabs above it.
        tabsScroll.visibility =
            if (seasons.size > 1 && (item.type == "show" || item.type == "artist")) View.VISIBLE else View.GONE
        list.visibility = View.VISIBLE

        if (episodeAdapter == null) {
            // Route child clicks through Navigator so containers (an
            // artist's albums) drill into their own detail screen
            // instead of being mis-played as media. Tracks / episodes
            // / podcast episodes still hit PlaybackFragment via
            // Navigator's else branch.
            // Show / season episode rows get a long-press watched toggle;
            // an album's tracks an instant mix (v2.6).
            val onLongClick: ((ChildItem) -> Unit)? = when {
                WatchStateUi.isWatchContainer(item.type) -> { child -> if (child.type == "episode") showEpisodeMenu(child) }
                item.type == "album" -> { child -> if (child.type == "track") showTrackMenu(child) }
                else -> null
            }
            episodeAdapter = EpisodeAdapter(serverUrl, item.poster_path, onLongClick) { child ->
                Navigator.open(parentFragmentManager, child.id, child.type, child.view_offset_ms)
            }
            list.layoutManager = LinearLayoutManager(requireContext(), LinearLayoutManager.HORIZONTAL, false)
            // Rebind a changed row (watched toggle, progress refresh) in its
            // own view holder: a change animation swaps in a second holder,
            // which flickers the row and can drop D-pad focus mid-toggle.
            (list.itemAnimator as? androidx.recyclerview.widget.SimpleItemAnimator)?.supportsChangeAnimations = false
            list.adapter = episodeAdapter
        }

        if (currentSeasonId == null) {
            // Open on the season holding the up-next episode (show pages), so
            // the episode Play is about to start is on screen; else the first.
            val upNextEpisode = viewModel.uiState.value.upNext?.episode
            val upNextSeason = upNextEpisode?.let { ep -> seasons.keys.firstOrNull { it.id == ep.season_id } }
            currentSeasonId = upNextSeason?.id ?: seasons.keys.firstOrNull()?.id
            initialScrollEpisodeId = upNextEpisode?.id
        }

        tabsContainer.removeAllViews()
        seasons.keys.forEachIndexed { i, season ->
            val pill = LayoutInflater.from(requireContext()).inflate(android.R.layout.simple_list_item_1, tabsContainer, false) as TextView
            pill.apply {
                background = resources.getDrawable(R.drawable.tab_pill, null)
                // Pill label: shows use "Season N" formatting (the
                // original case — `season.index` is the season number).
                // Artists use the synthetic group title verbatim
                // ("Albums" / "Music Videos") because the index field
                // there is just a sort key, not a number to display.
                text = if (item.type == "show") {
                    season.index?.let { getString(R.string.season_n, it) } ?: season.title
                } else {
                    season.title
                }
                setTextColor(resources.getColor(R.color.text_primary, null))
                textSize = 13f
                setPadding(36, 16, 36, 16)
                isFocusable = true
                isFocusableInTouchMode = true
                isSelected = season.id == currentSeasonId
                // D-pad past either end of the strip stays on it: with nothing
                // beside the end pill, focus search jumped up to the action
                // buttons (RIGHT from "Similar Artists" landed on Shuffle).
                id = View.generateViewId()
                if (i == 0) nextFocusLeftId = id
                if (i == seasons.size - 1) nextFocusRightId = id
                setOnClickListener {
                    currentSeasonId = season.id
                    headerFor(season)
                    for (ci in 0 until tabsContainer.childCount) {
                        tabsContainer.getChildAt(ci).isSelected = (ci == i)
                    }
                    // Read the live map, not this bind's snapshot: watched
                    // marks and returns from playback refresh the lists.
                    episodeAdapter?.submit(activeEpisodes())
                }
            }
            val lp = LinearLayout.LayoutParams(LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT)
            lp.marginEnd = 16
            tabsContainer.addView(pill, lp)
        }

        val activeSeason = seasons.entries.firstOrNull { it.key.id == currentSeasonId } ?: seasons.entries.first()
        headerFor(activeSeason.key)
        episodeAdapter?.submit(activeSeason.value)

        // First bind of a show / season: bring the up-next episode (and its
        // season tab) into view instead of leaving the list at episode 1.
        initialScrollEpisodeId?.let { epId ->
            initialScrollEpisodeId = null
            val pos = activeSeason.value.indexOfFirst { it.id == epId }
            if (pos > 0) list.scrollToPosition(pos)
            val tabIndex = seasons.keys.indexOfFirst { it.id == activeSeason.key.id }
            if (tabIndex > 0) {
                tabsScroll.post {
                    tabsContainer.getChildAt(tabIndex)?.let { tabsScroll.scrollTo(it.left, 0) }
                }
            }
        }
    }

    /** Episodes of the selected season tab, from the live [seasonMap]. */
    private fun activeEpisodes(): List<ChildItem> =
        (seasonMap.entries.firstOrNull { it.key.id == currentSeasonId } ?: seasonMap.entries.firstOrNull())
            ?.value ?: emptyList()

    /**
     * Re-applied on every state emission after the first bind: watched marks,
     * Up Next and returns from playback all change it. Refreshes the episode
     * list in place (DiffUtil keeps D-pad focus), the Play button label and
     * the Mark watched button.
     */
    private fun bindWatchState(root: View, state: DetailUiState) {
        val item = state.item ?: return
        if (!detailBound) return
        if (state.seasons.isNotEmpty() && state.seasons != seasonMap) {
            seasonMap = state.seasons
            episodeAdapter?.submit(activeEpisodes())
        }
        configurePlayButtons(
            item,
            root.findViewById(R.id.btn_play),
            root.findViewById(R.id.btn_play_from_start),
            focusPlay = false,
        )
        val btn = root.findViewById<Button>(R.id.btn_mark_watched) ?: return
        when {
            WatchStateUi.isMarkableLeaf(item.type) -> {
                btn.visibility = View.VISIBLE
                btn.text = getString(if (state.watched) R.string.mark_unwatched else R.string.mark_watched)
                btn.setOnClickListener { viewModel.toggleWatched() }
            }
            WatchStateUi.isWatchContainer(item.type) -> {
                val opts = WatchStateUi.markAllOptions(state.upNext)
                when {
                    opts.watched && opts.unwatched -> {
                        btn.visibility = View.VISIBLE
                        btn.text = getString(R.string.mark_all_menu)
                        btn.setOnClickListener { showMarkAllMenu(item) }
                    }
                    opts.watched -> {
                        btn.visibility = View.VISIBLE
                        btn.text = getString(R.string.mark_all_watched)
                        btn.setOnClickListener { viewModel.markAll(true) }
                    }
                    opts.unwatched -> {
                        btn.visibility = View.VISIBLE
                        btn.text = getString(R.string.mark_all_unwatched)
                        btn.setOnClickListener { confirmMarkAllUnwatched(item) }
                    }
                    else -> btn.visibility = View.GONE
                }
            }
            else -> btn.visibility = View.GONE
        }
        // Not isEnabled=false: disabling the focused button can drop D-pad
        // focus on some TV builds. The ViewModel ignores a second mark while
        // one is in flight; the dim is just feedback.
        btn.alpha = if (state.markBusy) 0.5f else 1f
    }

    /** Show / season "Mark all…" when both directions make sense. */
    private fun showMarkAllMenu(item: ItemDetail) {
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(item.title)
            .setItems(arrayOf(getString(R.string.mark_all_watched), getString(R.string.mark_all_unwatched))) { d, idx ->
                d.dismiss()
                if (!isAdded) return@setItems
                if (idx == 0) viewModel.markAll(true) else confirmMarkAllUnwatched(item)
            }
            .create()
            .dismissOnViewDestroyed(this)
            .show()
    }

    /** Unwatching a whole SHOW wipes every watched mark and resume point in
     *  it — confirm first. A season is small enough to just do. */
    private fun confirmMarkAllUnwatched(item: ItemDetail) {
        if (item.type != "show") {
            viewModel.markAll(false)
            return
        }
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.confirm_mark_show_unwatched_title)
            .setMessage(getString(R.string.confirm_mark_show_unwatched, item.title))
            .setPositiveButton(R.string.mark_all_unwatched) { d, _ ->
                d.dismiss()
                if (isAdded) viewModel.markAll(false)
            }
            .setNegativeButton(R.string.cancel) { d, _ -> d.dismiss() }
            .create()
            .focusableOnTv()
            .dismissOnViewDestroyed(this)
            .show()
    }

    /** Long-press menu on an episode row: its watched toggle. */
    private fun showEpisodeMenu(episode: ChildItem) {
        if (!isAdded) return
        val label = getString(if (episode.watched) R.string.episode_mark_unwatched else R.string.episode_mark_watched)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(episode.title)
            .setItems(arrayOf(label)) { d, _ ->
                d.dismiss()
                if (isAdded) viewModel.toggleEpisodeWatched(episode)
            }
            .create()
            .dismissOnViewDestroyed(this)
            .show()
    }

    private fun toast(res: Int) {
        if (isAdded) Toast.makeText(requireContext(), res, Toast.LENGTH_SHORT).show()
    }

    /** [fileId]: the version to play (bindVersionPicker); null = the first.
     *  [keepQueue]: the start of an instant mix, whose PlayQueue was just
     *  set; anything else the user starts here ends a mix that was playing. */
    private fun playItem(itemId: String, startMs: Long, fileId: String? = null, keepQueue: Boolean = false) {
        if (!keepQueue) PlayQueue.clear()
        parentFragmentManager.beginTransaction()
            .replace(R.id.main_container, PlaybackFragment.newInstance(itemId, startMs, fileId = fileId))
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
