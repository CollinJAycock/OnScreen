package tv.onscreen.android.ui.common

import org.junit.Assert.assertEquals
import org.junit.Test
import tv.onscreen.android.ui.common.Navigator.Destination

class NavigatorRoutingTest {

    @Test
    fun `an e-book opens its detail page, never the player`() {
        // The player asked the server to transcode the EPUB, which answered
        // 422: "Playback error HTTP 422" on the Hisense (2026-11-05).
        assertEquals(Destination.DETAIL, Navigator.destinationKind("book"))
    }

    @Test
    fun `containers, movies and audiobooks open their detail page`() {
        for (type in listOf(
            "show", "season", "artist", "album", "podcast",
            "book_author", "book_series", "audiobook", "movie",
        )) {
            assertEquals(type, Destination.DETAIL, Navigator.destinationKind(type))
        }
    }

    @Test
    fun `photos, collections and playable leaves keep their routes`() {
        assertEquals(Destination.PHOTO, Navigator.destinationKind("photo"))
        assertEquals(Destination.COLLECTION, Navigator.destinationKind("collection"))
        assertEquals(Destination.COLLECTION, Navigator.destinationKind("playlist"))
        for (type in listOf("episode", "track", "audiobook_chapter", "podcast_episode", "music_video", "home_video")) {
            assertEquals(type, Destination.PLAYER, Navigator.destinationKind(type))
        }
    }
}
