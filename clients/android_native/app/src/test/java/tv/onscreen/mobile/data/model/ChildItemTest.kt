package tv.onscreen.mobile.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiListResponse

/**
 * A track's disc (/items/{id}/children, internal/api/v1/items.go
 * ChildItemResponse) and the (disc, index) order it implies. A multi-disc
 * album numbers each disc from 1, so index order alone interleaves the discs.
 */
class ChildItemTest {

    private val moshi = Moshi.Builder().build()

    private fun track(id: String, index: Int?, disc: Int? = null) =
        ChildItem(id = id, title = id, type = "track", index = index, disc_number = disc)

    @Test
    fun `children decode disc_number, and read null where the server omits it`() {
        val type = Types.newParameterizedType(ApiListResponse::class.java, ChildItem::class.java)
        val json = """{"data":[
            {"id":"a","title":"A","type":"track","index":1,"disc_number":2,"view_offset_ms":0,
             "watched":false,"created_at":"2026-09-28T00:00:00Z","updated_at":1},
            {"id":"b","title":"B","type":"episode","index":1,"view_offset_ms":0,
             "watched":false,"created_at":"2026-09-28T00:00:00Z","updated_at":1}
        ],"meta":{"total":2,"cursor":null}}"""

        val kids = moshi.adapter<ApiListResponse<ChildItem>>(type).fromJson(json)!!.data

        assertThat(kids.map { it.disc_number }).containsExactly(2, null).inOrder()
    }

    @Test
    fun `PLAY_ORDER is disc then track, with no disc meaning disc 1`() {
        val shuffled = listOf(
            track("d2t2", 2, disc = 2),
            track("d1t2", 2),
            track("d2t1", 1, disc = 2),
            track("d1t1", 1, disc = 1),
        )

        assertThat(shuffled.sortedWith(ChildItem.PLAY_ORDER).map { it.id })
            .containsExactly("d1t1", "d1t2", "d2t1", "d2t2").inOrder()
    }

    @Test
    fun `PLAY_ORDER puts an unnumbered row at the end of its disc`() {
        val kids = listOf(track("d2t1", 1, disc = 2), track("d1-none", null), track("d1t1", 1))

        assertThat(kids.sortedWith(ChildItem.PLAY_ORDER).map { it.id })
            .containsExactly("d1t1", "d1-none", "d2t1").inOrder()
    }
}
