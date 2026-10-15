package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.ArtistAppearance
import tv.onscreen.mobile.data.model.MixTrack
import tv.onscreen.mobile.data.model.SimilarArtist
import javax.inject.Inject
import javax.inject.Singleton

/** Music discovery (v2.6): similar artists, "appears on", instant mixes. */
@Singleton
open class MusicRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun similarArtists(artistId: String): List<SimilarArtist> =
        api.similarArtists(artistId).data

    open suspend fun appearsOn(artistId: String): List<ArtistAppearance> =
        api.artistAppearsOn(artistId).data

    open suspend fun instantMix(seedId: String): List<MixTrack> =
        api.instantMix(seedId).data
}
