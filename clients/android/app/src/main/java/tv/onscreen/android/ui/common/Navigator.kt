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

    /** Where a card of [type] opens. Pure, so the routing is unit-tested
     *  (NavigatorRoutingTest) without building fragments. */
    enum class Destination { DETAIL, PHOTO, COLLECTION, PLAYER }

    internal fun destinationKind(type: String): Destination = when (type) {
        // Containers: the detail screen, to pick a season / episode / track /
        // chapter / book to play. book_author + book_series are the audiobook
        // hierarchy parents above an audiobook row; same shape as artist /
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
        "book_author", "book_series", "audiobook",
        // Movies route to detail too: the page handles a leaf item (Play +
        // Resume / Play From Start), and straight to playback meant the user
        // never saw the movie's metadata, summary or fanart.
        "movie",
        // An e-book (EPUB / PDF / CBZ). The TV has no reader: the catch-all
        // sent it to the player, the server refused to transcode it (422) and
        // the user got "Playback error HTTP 422". Its detail page has Status
        // and says where to read it, like the webOS app.
        "book" -> Destination.DETAIL
        // Photos render full-screen via Coil; ExoPlayer doesn't decode JPEGs.
        "photo" -> Destination.PHOTO
        // Collections / playlists drill into their own grid.
        "collection", "playlist" -> Destination.COLLECTION
        // Anything ExoPlayer can play: episodes, music tracks, audiobook
        // chapters, podcast episodes. Leaf items the user drilled to through
        // the parent's detail page, so a second detail hop would be friction.
        else -> Destination.PLAYER
    }

    private fun destinationFor(itemId: String, type: String, resumeMs: Long): Fragment =
        when (destinationKind(type)) {
            Destination.DETAIL -> DetailFragment.newInstance(itemId)
            Destination.PHOTO -> PhotoViewFragment.newInstance(itemId)
            // The id is the collection id; CollectionFragment fetches the
            // title with the collection metadata.
            Destination.COLLECTION -> CollectionFragment.newInstance(itemId, "")
            Destination.PLAYER -> {
                // Picked by the user, so not part of an instant mix that was
                // playing (PlayQueue).
                tv.onscreen.android.playback.PlayQueue.clear()
                PlaybackFragment.newInstance(itemId, resumeMs)
            }
        }
}
