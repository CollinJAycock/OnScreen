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
            "show", "season", "artist", "album", "podcast",
            "book_author", "book_series" ->
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
            // tracks, audiobooks (single-file MVP), podcast episodes
            // — all leaf items the user has already drilled to via
            // the parent container's detail page, so a second detail
            // hop would be friction.
            else ->
                PlaybackFragment.newInstance(itemId, resumeMs)
        }
    }
}
