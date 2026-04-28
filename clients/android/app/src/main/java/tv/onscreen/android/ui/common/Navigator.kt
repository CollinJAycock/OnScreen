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
            // pick a season / episode / track / chapter to play.
            "show", "season", "artist", "album", "podcast" ->
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

            // Default: anything ExoPlayer can play. Movies, episodes,
            // music tracks, audiobooks (single-file MVP), podcast
            // episodes. Music + audiobooks render with a black video
            // surface and the standard transport controls — a proper
            // music-player UI is a follow-up.
            else ->
                PlaybackFragment.newInstance(itemId, resumeMs)
        }
    }
}
