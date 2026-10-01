package tv.onscreen.mobile.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import org.junit.Test

/**
 * Wire shape of the progress PUT, through the generated Moshi adapter the
 * Retrofit converter uses (it writes no nulls).
 */
class ProgressRequestTest {

    private val adapter = Moshi.Builder().build().adapter(ProgressRequest::class.java)

    @Test
    fun `an unknown duration is left out, not sent as 0`() {
        // The server keeps the duration it knows for the item when the field
        // is absent; the client never claims one it can't tell.
        assertThat(adapter.toJson(ProgressRequest(61_000L, null, "playing")))
            .isEqualTo("""{"view_offset_ms":61000,"state":"playing"}""")
    }

    @Test
    fun `a known duration and decision go out as given`() {
        assertThat(adapter.toJson(ProgressRequest(61_000L, 7_200_000L, "stopped", "directStream")))
            .isEqualTo("""{"view_offset_ms":61000,"duration_ms":7200000,"state":"stopped","decision":"directStream"}""")
    }
}
