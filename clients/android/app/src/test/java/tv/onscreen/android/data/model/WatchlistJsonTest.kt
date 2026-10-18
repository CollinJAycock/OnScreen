package tv.onscreen.android.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import org.junit.Test
import tv.onscreen.android.data.api.ApiListResponse
import tv.onscreen.android.data.api.ApiResponse

/**
 * Wire shapes of GET /api/v1/watchlist and /watchlist/counts, as
 * internal/api/v1/watchlist.go emits them (omitempty fields left out).
 */
class WatchlistJsonTest {

    private val moshi = Moshi.Builder().build()

    private fun page(json: String): ApiListResponse<WatchlistItem> {
        val type = Types.newParameterizedType(ApiListResponse::class.java, WatchlistItem::class.java)
        return moshi.adapter<ApiListResponse<WatchlistItem>>(type).fromJson(json)!!
    }

    private fun counts(json: String): WatchlistCounts {
        val type = Types.newParameterizedType(ApiResponse::class.java, WatchlistCounts::class.java)
        return moshi.adapter<ApiResponse<WatchlistCounts>>(type).fromJson(json)!!.data
    }

    @Test
    fun `a listing page decodes an episode with its ancestry and a bare movie`() {
        val resp = page(
            """
            {"data":[
              {"id":"e5","library_id":"l1","type":"episode","title":"Five","index":5,
               "parent_id":"s2","parent_title":"Season 2","parent_index":2,
               "grandparent_id":"sh","grandparent_title":"Show","poster_path":"show/poster.jpg",
               "thumb_path":"e5/thumb.jpg","duration_ms":1500000,"watch_state":"in_progress",
               "status":"watching","status_created_at":"2026-10-01T10:00:00Z",
               "status_updated_at":"2026-10-02T10:00:00.123456Z"},
              {"id":"m1","library_id":"l2","type":"movie","title":"Movie","year":1999,
               "watch_state":"unwatched","status":"watching",
               "status_created_at":"2026-10-01T10:00:00Z","status_updated_at":"2026-10-01T10:00:00Z"}
            ],"meta":{"total":7,"cursor":""}}
            """.trimIndent(),
        )
        assertThat(resp.meta.total).isEqualTo(7)
        val (episode, movie) = resp.data
        assertThat(episode.type).isEqualTo("episode")
        assertThat(episode.index).isEqualTo(5)
        assertThat(episode.parent_index).isEqualTo(2)
        assertThat(episode.parent_title).isEqualTo("Season 2")
        assertThat(episode.grandparent_id).isEqualTo("sh")
        assertThat(episode.grandparent_title).isEqualTo("Show")
        assertThat(episode.poster_path).isEqualTo("show/poster.jpg")
        assertThat(episode.duration_ms).isEqualTo(1_500_000)
        assertThat(episode.watch_state).isEqualTo("in_progress")
        assertThat(episode.status).isEqualTo(WatchStatusValues.Watching)
        assertThat(episode.status_updated_at).isEqualTo("2026-10-02T10:00:00.123456Z")

        assertThat(movie.year).isEqualTo(1999)
        assertThat(movie.index).isNull()
        assertThat(movie.parent_title).isNull()
        assertThat(movie.grandparent_title).isNull()
        assertThat(movie.poster_path).isNull()
    }

    @Test
    fun `an empty page decodes`() {
        val resp = page("""{"data":[],"meta":{"total":0,"cursor":""}}""")
        assertThat(resp.data).isEmpty()
        assertThat(resp.meta.total).isEqualTo(0)
    }

    @Test
    fun `counts decode per status and per type`() {
        val c = counts(
            """
            {"data":{"total":5,"statuses":{
              "plan_to_watch":{"total":3,"types":{"movie":2,"show":1}},
              "watching":{"total":2,"types":{"episode":2}},
              "on_hold":{"total":0,"types":{}},
              "completed":{"total":0,"types":{}},
              "dropped":{"total":0,"types":{}}
            }}}
            """.trimIndent(),
        )
        assertThat(c.total).isEqualTo(5)
        assertThat(c.totalFor(WatchStatusValues.PlanToWatch)).isEqualTo(3)
        assertThat(c.statuses[WatchStatusValues.PlanToWatch]?.types).containsExactly("movie", 2, "show", 1)
        assertThat(c.totalFor(WatchStatusValues.Watching)).isEqualTo(2)
        assertThat(c.totalFor(WatchStatusValues.Dropped)).isEqualTo(0)
    }

    @Test
    fun `counts with missing statuses read as zero`() {
        val c = counts("""{"data":{"total":1,"statuses":{"completed":{"total":1,"types":{"movie":1}}}}}""")
        assertThat(c.totalFor(WatchStatusValues.Completed)).isEqualTo(1)
        assertThat(c.totalFor(WatchStatusValues.PlanToWatch)).isEqualTo(0)
        assertThat(c.totalFor(WatchStatusValues.OnHold)).isEqualTo(0)

        val bare = counts("""{"data":{"total":0}}""")
        assertThat(bare.statuses).isEmpty()
        assertThat(bare.totalFor(WatchStatusValues.Watching)).isEqualTo(0)
    }
}
