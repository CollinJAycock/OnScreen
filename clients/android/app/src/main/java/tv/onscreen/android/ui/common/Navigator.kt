package tv.onscreen.android.ui.common

import androidx.fragment.app.Fragment
import androidx.fragment.app.FragmentManager
import tv.onscreen.android.R
import tv.onscreen.android.ui.browse.CollectionFragment
import tv.onscreen.android.ui.detail.DetailFragment
import tv.onscreen.android.ui.photo.PhotoViewFragment
import tv.onscreen.android.ui.playback.PlaybackFragment

/**
 * Routes a card-click to the right destination fragment based on
 * the item's type. Centralised here so HomeFragment, LibraryFragment,
 * FavoritesFragment, HistoryFragment, and SearchFragment all stay
 * in sync — adding a new playable type (audiobook chapter, podcast
 * episode, music track) is one diff here, not five.
 */
object Navigator {

    /** Route the selection. `resumeMs` only applies to playable
     *  video; photo / detail destinations ignore it. */
    fun open(
        fm: FragmentManager,
        itemId: String,
        type: String,
        resumeMs: Long = 0L,
    ) {
        // Callers reach this from click handlers AND from coroutines that
        // resume after a network round-trip, so the activity may already have
        // saved state by the time we get here (user pressed HOME, or the TV
        // switched input, mid-request). commit() after onSaveInstanceState is
        // an IllegalStateException; the navigation is worthless at that point
        // anyway, because MainActivity resets to Home on the way back in.
        if (fm.isStateSaved) return
        val destination = destinationFor(itemId, type, resumeMs)
        fm.beginTransaction()
            .replace(R.id.main_container, destination)
            .addToBackStack(null)
            .commit()
    }

    private fun destinationFor(itemId: String, type: String, resumeMs: Long): Fragment {
        return when (type) {
            // Containers — go to the detail screen so the user can
            // pick a season / episode / track / chapter / book to play.
            // book_author + book_series are the audiobook hierarchy
            // parents above an audiobook row; same shape as artist /
            // album, drilling renders the children list.
            // "audiobook" belongs here. It used to fall to the catch-all and go
            // straight to the player, which for a multi-file book — a container
            // with chapter children and no files of its own — died instantly
            // with "No playable file". The whole chapter screen was written and
            // unreachable: DetailViewModel loads children for "audiobook",
            // DetailFragment has a multi-file branch, and EpisodeAdapter already
            // labels the children "Chapter n". Single-file books are better off
            // here too — they get Resume / Play From Start, and the Chapters
            // section stays hidden because the child list comes back empty.
            "show", "season", "artist", "album", "podcast",
            "book_author", "book_series", "audiobook" ->
                DetailFragment.newInstance(itemId)

            // Movies route to detail too. The detail page already
            // handles a leaf item (Play button + Resume / Play From
            // Start when there's a view_offset_ms), and routing
            // straight to playback meant the user never saw the
            // movie's metadata, summary, or fanart. Containers and
            // movies use the same detail page; the page reads
            // item.type to pick the right configurePlayButtons
            // branch (single-Play for movies, pick-child for shows
            // / albums / podcasts).
            "movie" ->
                DetailFragment.newInstance(itemId)

            // Photos render full-screen via Coil; can't go through
            // ExoPlayer (it doesn't decode JPEGs).
            "photo" ->
                PhotoViewFragment.newInstance(itemId)

            // Collections / playlists drill into their own grid.
            // The id is the collection id; the title is filled in
            // when the fragment loads (CollectionFragment fetches
            // the collection metadata before listing items).
            "collection", "playlist" ->
                CollectionFragment.newInstance(itemId, "")

            // Default: anything ExoPlayer can play. Episodes, music
            // tracks, audiobook chapters, podcast episodes
            // — all leaf items the user has already drilled to via
            // the parent container's detail page, so a second detail
            // hop would be friction.
            else ->
                PlaybackFragment.newInstance(itemId, resumeMs)
        }
    }
}
