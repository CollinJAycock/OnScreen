package tv.onscreen.android.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import org.junit.Test
import tv.onscreen.android.data.api.ApiResponse

/**
 * Wire shapes of the v2.6 collection fields (internal/api/v1/hub.go
 * collection_rows, collections.go poster_version), and the older-server
 * shapes that omit them.
 */
class CollectionPhase2JsonTest {

    private val moshi = Moshi.Builder().build()

    private inline fun <reified T> envelope(json: String): T? {
        val type = Types.newParameterizedType(ApiResponse::class.java, T::class.java)
        return moshi.adapter<ApiResponse<T>>(type).fromJson(json)?.data
    }

    @Test
    fun `hub collection_rows decode`() {
        val hub = envelope<HubData>(
            """
            {"data":{"continue_watching":[],"recently_added":[],"trending":[],
              "collection_rows":[{"collection_id":"c1","name":"Spooky Season",
                "items":[{"id":"m1","title":"Halloween","type":"movie","year":1978,"updated_at":1}]}]
            }}
            """.trimIndent(),
        )!!
        val row = hub.collection_rows.single()
        assertThat(row.collection_id).isEqualTo("c1")
        assertThat(row.name).isEqualTo("Spooky Season")
        assertThat(row.items.single().title).isEqualTo("Halloween")
    }

    @Test
    fun `an older hub without collection_rows still parses`() {
        val hub = envelope<HubData>("""{"data":{"continue_watching":[],"recently_added":[]}}""")!!
        assertThat(hub.collection_rows).isEmpty()
    }

    @Test
    fun `an uploaded cover gives the collection a cover path`() {
        val type = Types.newParameterizedType(List::class.java, MediaCollection::class.java)
        val cols = moshi.adapter<List<MediaCollection>>(type).fromJson(
            """
            [{"id":"c1","name":"Spooky","type":"manual","created_at":"","poster_version":1700000000000},
             {"id":"c2","name":"Plain","type":"manual","created_at":"","poster_path":"p.jpg"}]
            """.trimIndent(),
        )!!
        assertThat(cols[0].coverApiPath).isEqualTo("/api/v1/collections/c1/poster?v=1700000000000")
        assertThat(cols[1].coverApiPath).isNull()
    }
}
