package tv.onscreen.android.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.android.data.api.ApiResponse
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.UserPreferences

class PreferencesRepositoryTest {

    @Test
    fun `get caches so a second read does not hit the network`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPreferences() } returns ApiResponse(UserPreferences(preferred_audio_lang = "en"))
        val repo = PreferencesRepository(api)

        repo.get()
        repo.get()

        coVerify(exactly = 1) { api.getPreferences() }
    }

    // This is a @Singleton with a process-lifetime cache. Without invalidation
    // the next user to sign in on a shared TV inherits the previous user's hub
    // layout, audio/subtitle languages and rating labels — and saving any one
    // setting writes the rest of that stale object onto their account.
    @Test
    fun `invalidate forces a refetch for the next identity`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPreferences() } returnsMany listOf(
            ApiResponse(UserPreferences(preferred_audio_lang = "en")),
            ApiResponse(UserPreferences(preferred_audio_lang = "ja")),
        )
        val repo = PreferencesRepository(api)

        assertThat(repo.get().preferred_audio_lang).isEqualTo("en")
        repo.invalidate()
        assertThat(repo.get().preferred_audio_lang).isEqualTo("ja")
    }

    // The PUT answers 204 with no body. Caching what we SENT would be wrong:
    // the endpoint deliberately ignores hub_layout (its own route) and
    // max_content_rating (the admin-set parental ceiling), so the client must
    // re-read rather than assume its request was stored verbatim.
    @Test
    fun `set re-reads authoritative state instead of echoing the request`() = runTest {
        val api = mockk<OnScreenApi>()
        val sent = UserPreferences(preferred_audio_lang = "ja", max_content_rating = "R")
        val authoritative = UserPreferences(preferred_audio_lang = "ja", max_content_rating = "PG")
        coEvery { api.setPreferences(any()) } returns Unit
        coEvery { api.getPreferences() } returns ApiResponse(authoritative)
        val repo = PreferencesRepository(api)

        val result = repo.set(sent)

        assertThat(result.max_content_rating).isEqualTo("PG")
        coVerify(exactly = 1) { api.getPreferences() }
    }

    @Test
    fun `set leaves the cache holding the re-read value`() = runTest {
        val api = mockk<OnScreenApi>()
        val authoritative = UserPreferences(preferred_audio_lang = "de")
        coEvery { api.setPreferences(any()) } returns Unit
        coEvery { api.getPreferences() } returns ApiResponse(authoritative)
        val repo = PreferencesRepository(api)

        repo.set(UserPreferences(preferred_audio_lang = "de"))
        val cached = repo.get()

        assertThat(cached).isEqualTo(authoritative)
        // One fetch for the post-write re-read; get() must serve from cache.
        coVerify(exactly = 1) { api.getPreferences() }
    }
}
