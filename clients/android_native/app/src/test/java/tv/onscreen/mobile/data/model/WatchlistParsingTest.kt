package tv.onscreen.mobile.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiListResponse
import tv.onscreen.mobile.data.api.ApiResponse

/**
 * Wire-shape pins for the v2.6 watchlist, parsed through the generated Moshi
 * adapters with JSON shaped like the Go handlers' output
 * (internal/api/v1/watchlist.go: WatchlistItem, WatchlistCounts).
 */
class WatchlistParsingTest {

    private val moshi = Moshi.Builder().build()

    private val listAdapter = moshi.adapter<ApiListResponse<WatchlistItem>>(
        Types.newParameterizedType(ApiListResponse::class.java, WatchlistItem::class.java),
    )
    private val countsAdapter = moshi.adapter<ApiResponse<WatchlistCounts>>(
        Types.newParameterizedType(ApiResponse::class.java, WatchlistCounts::class.java),
    )

    @Test
    fun `list page parses an episode card with its ancestry`() {
        val json = """{"data":[
            {"id":"e1","library_id":"L","type":"episode","title":"Pilot","index":2,
             "parent_id":"s1","parent_title":"Season 1","parent_index":1,
             "grandparent_id":"sh","grandparent_title":"The Show",
             "poster_path":"sh/poster.jpg","thumb_path":"e1/thumb.jpg","duration_ms":1800000,
             "watch_state":"in_progress","status":"watching",
             "status_created_at":"2026-10-01T10:00:00Z","status_updated_at":"2026-10-02T11:30:00.123456Z"}
          ],"meta":{"total":51,"cursor":""}}"""
        val page = listAdapter.fromJson(json)!!
        assertThat(page.meta.total).isEqualTo(51)
        val e = page.data.single()
        assertThat(e.type).isEqualTo("episode")
        assertThat(e.index).isEqualTo(2)
        assertThat(e.parent_index).isEqualTo(1)
        assertThat(e.grandparent_title).isEqualTo("The Show")
        assertThat(e.poster_path).isEqualTo("sh/poster.jpg")
        assertThat(e.duration_ms).isEqualTo(1800000L)
        assertThat(e.watch_state).isEqualTo(WatchStateValue.IN_PROGRESS)
        assertThat(WatchStatus.fromWire(e.status)).isEqualTo(WatchStatus.WATCHING)
        assertThat(e.status_updated_at).isEqualTo("2026-10-02T11:30:00.123456Z")
    }

    @Test
    fun `a movie card with the optional keys omitted still parses`() {
        // Go's omitempty drops every nil pointer: no year, index, ancestry or art.
        val json = """{"data":[{"id":"m","library_id":"L","type":"movie","title":"Film",
            "watch_state":"unwatched","status":"plan_to_watch",
            "status_created_at":"2026-10-01T10:00:00Z","status_updated_at":"2026-10-01T10:00:00Z"}],
            "meta":{"total":1,"cursor":""}}"""
        val m = listAdapter.fromJson(json)!!.data.single()
        assertThat(m.year).isNull()
        assertThat(m.index).isNull()
        assertThat(m.poster_path).isNull()
        assertThat(m.grandparent_title).isNull()
        assertThat(m.status).isEqualTo("plan_to_watch")
    }

    @Test
    fun `counts parse per status with the type breakdown, missing statuses absent`() {
        val json = """{"data":{"total":5,"statuses":{
            "plan_to_watch":{"total":4,"types":{"movie":3,"show":1}},
            "completed":{"total":1,"types":{"episode":1}}}}}"""
        val c = countsAdapter.fromJson(json)!!.data
        assertThat(c.total).isEqualTo(5)
        assertThat(c.statuses["plan_to_watch"]!!.total).isEqualTo(4)
        assertThat(c.statuses["plan_to_watch"]!!.types).containsExactly("movie", 3, "show", 1)
        assertThat(c.statuses["completed"]!!.types).containsExactly("episode", 1)
        assertThat(c.statuses).doesNotContainKey("watching")
    }

    @Test
    fun `counts with an empty types map parse`() {
        val c = countsAdapter.fromJson(
            """{"data":{"total":0,"statuses":{"dropped":{"total":0,"types":{}}}}}""",
        )!!.data
        assertThat(c.statuses["dropped"]!!.types).isEmpty()
    }

    @Test
    fun `watch-status body with fields at top level and under data parses the bare shape`() {
        // v2.6 writes the fields twice (top level for this app, `data` for
        // envelope readers). The extra `data` key must be ignored.
        val json = """{"status":"on_hold","created_at":"2026-10-01T10:00:00Z",
            "updated_at":"2026-10-02T10:00:00Z",
            "data":{"status":"on_hold","created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-02T10:00:00Z"}}"""
        val r = moshi.adapter(WatchStatusResponse::class.java).fromJson(json)!!
        assertThat(WatchStatus.fromWire(r.status)).isEqualTo(WatchStatus.ON_HOLD)
        assertThat(r.updated_at).isEqualTo("2026-10-02T10:00:00Z")
    }
}
