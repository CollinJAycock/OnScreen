package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.ArtistAppearance
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.MixTrack
import tv.onscreen.mobile.data.model.SimilarArtist
import tv.onscreen.mobile.data.repository.MusicRepository
import tv.onscreen.mobile.playback.InstantMixQueue

/** Similar artists, "Appears on" and Instant mix (v2.6). */
@OptIn(ExperimentalCoroutinesApi::class)
class MusicExtrasTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() {
        Dispatchers.setMain(dispatcher)
        InstantMixQueue.clear()
    }

    @After fun tearDown() {
        Dispatchers.resetMain()
        InstantMixQueue.clear()
    }

    private fun appearance(track: String, album: String, title: String = "Album $album") = ArtistAppearance(
        id = track, title = "t-$track", album_id = album, album_title = title,
        album_year = 2020, album_artist_title = "Various Artists",
    )

    private fun mix(vararg ids: String) = ids.map { MixTrack(id = it, title = "t-$it", album_id = "al-$it", index = 1) }

    private fun artist(id: String = "ar") = ItemDetail(id = id, library_id = "l", title = "Artist", type = "artist")

    private fun http404() = retrofit2.HttpException(
        retrofit2.Response.error<Any>(404, "{}".toResponseBody("application/json".toMediaTypeOrNull())),
    )

    @Test
    fun `appearances group into their albums, in the server's order`() {
        val albums = appearsOnAlbums(
            listOf(appearance("t1", "b"), appearance("t2", "a"), appearance("t3", "b"), appearance("t3", "b")),
        )
        assertThat(albums.map { it.id }).containsExactly("b", "a").inOrder()
        assertThat(albums.first().trackCount).isEqualTo(2)
        assertThat(albums.first().artistTitle).isEqualTo("Various Artists")
    }

    @Test
    fun `only artists, albums and tracks seed a mix`() {
        assertThat(listOf("artist", "album", "track").all(::canSeedInstantMix)).isTrue()
        assertThat(listOf("movie", "episode", "audiobook", null).none(::canSeedInstantMix)).isTrue()
    }

    @Test
    fun `the parked mix is taken once, by its first track, while fresh`() {
        assertThat(InstantMixQueue.put(mix("a", "b", "c", "b"), nowMs = 1_000)).isEqualTo("a")
        assertThat(InstantMixQueue.take("b", nowMs = 1_100)).isNull()
        val rest = InstantMixQueue.take("a", nowMs = 1_100)!!
        assertThat(rest.map { it.id }).containsExactly("b", "c").inOrder()
        assertThat(rest.first().albumId).isEqualTo("al-b")
        assertThat(InstantMixQueue.take("a", nowMs = 1_200)).isNull()

        InstantMixQueue.put(mix("x", "y"), nowMs = 0)
        assertThat(InstantMixQueue.take("x", nowMs = 60_000)).isNull()
        assertThat(InstantMixQueue.put(emptyList(), nowMs = 0)).isNull()
    }

    @Test
    fun `an artist page loads its similar artists and appearances`() = runTest(dispatcher) {
        val repo = mockk<MusicRepository>()
        coEvery { repo.similarArtists("ar") } returns listOf(
            SimilarArtist(id = "ar", title = "Self"),
            SimilarArtist(id = "s1", title = "Like"),
        )
        coEvery { repo.appearsOn("ar") } returns listOf(appearance("t1", "comp"))
        val vm = MusicExtrasViewModel(repo)

        vm.bind(artist())
        advanceUntilIdle()

        assertThat(vm.state.value.similar.map { it.id }).containsExactly("s1")
        assertThat(vm.state.value.appearsOn.map { it.id }).containsExactly("comp")
    }

    @Test
    fun `an older server shows no rows`() = runTest(dispatcher) {
        val repo = mockk<MusicRepository>()
        coEvery { repo.similarArtists(any()) } throws http404()
        coEvery { repo.appearsOn(any()) } throws http404()
        val vm = MusicExtrasViewModel(repo)

        vm.bind(artist())
        advanceUntilIdle()

        assertThat(vm.state.value.similar).isEmpty()
        assertThat(vm.state.value.appearsOn).isEmpty()
    }

    @Test
    fun `an album page asks for no artist rows`() = runTest(dispatcher) {
        val repo = mockk<MusicRepository>()
        val vm = MusicExtrasViewModel(repo)
        vm.bind(artist().copy(type = "album"))
        advanceUntilIdle()
        coVerify(exactly = 0) { repo.similarArtists(any()) }
        assertThat(vm.state.value.artistId).isNull()
    }

    @Test
    fun `instant mix parks the queue and opens the player on its first track`() = runTest(dispatcher) {
        val repo = mockk<MusicRepository>()
        coEvery { repo.instantMix("al-1") } returns mix("t1", "t2", "t3")
        val vm = MusicExtrasViewModel(repo).also { it.clock = { 5_000L } }

        vm.instantMix("al-1")
        advanceUntilIdle()

        assertThat(vm.playMix.first()).isEqualTo("t1")
        assertThat(InstantMixQueue.take("t1", 5_000L)!!.map { it.id }).containsExactly("t2", "t3").inOrder()
        assertThat(vm.state.value.mixLoading).isFalse()
    }

    @Test
    fun `an empty mix or an older server says so`() = runTest(dispatcher) {
        val repo = mockk<MusicRepository>()
        coEvery { repo.instantMix("a") } returns emptyList()
        coEvery { repo.instantMix("b") } throws http404()
        val vm = MusicExtrasViewModel(repo)

        vm.instantMix("a")
        advanceUntilIdle()
        assertThat(vm.messages.first()).isEqualTo("Nothing to mix from here yet.")

        vm.instantMix("b")
        advanceUntilIdle()
        assertThat(vm.messages.first()).contains("isn’t available on this server")
    }
}
