package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.ArtistAppearance
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.MixTrack
import tv.onscreen.android.data.model.SimilarArtist
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Music discovery (server v2.6): an artist's similar artists and the albums
 * by others it appears on, and instant mixes. Results come back as the
 * ChildItem rows the detail screen's lists and the play queue take.
 */
@Singleton
open class MusicRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    /** Artists like [artistId], as artist rows (open their own page). */
    open suspend fun similarArtists(artistId: String): List<ChildItem> =
        similarRows(api.getSimilarArtists(artistId).data)

    /** Albums by other artists that feature [artistId], as album rows. */
    open suspend fun appearsOn(artistId: String): List<ChildItem> =
        appearanceAlbums(api.getArtistAppearances(artistId).data)

    /** An instant mix seeded from a track, album or artist, as track rows in
     *  play order. */
    open suspend fun instantMix(seedId: String): List<ChildItem> =
        mixRows(api.getInstantMix(seedId).data)

    companion object {
        fun similarRows(artists: List<SimilarArtist>): List<ChildItem> = artists
            .filter { it.id.isNotEmpty() }
            .map { ChildItem(id = it.id, title = it.title, type = "artist", poster_path = it.poster_path) }

        /**
         * One album row per album, in the server's order (newest album
         * first), however many of its tracks credit the artist. The row names
         * the album's own artist after its title when known.
         */
        fun appearanceAlbums(tracks: List<ArtistAppearance>): List<ChildItem> = tracks
            .filter { it.album_id.isNotEmpty() }
            .distinctBy { it.album_id }
            .map { t ->
                val by = t.album_artist_title?.trim()?.takeIf { it.isNotEmpty() }
                ChildItem(
                    id = t.album_id,
                    title = if (by != null) "${t.album_title} · $by" else t.album_title,
                    type = "album",
                    year = t.album_year,
                    poster_path = t.album_poster_path,
                )
            }

        fun mixRows(tracks: List<MixTrack>): List<ChildItem> = tracks
            .filter { it.id.isNotEmpty() }
            .distinctBy { it.id }
            .map { t ->
                ChildItem(
                    id = t.id,
                    title = t.title,
                    type = "track",
                    duration_ms = t.duration_ms,
                    poster_path = t.album_poster_path,
                    index = t.index,
                )
            }
    }
}
