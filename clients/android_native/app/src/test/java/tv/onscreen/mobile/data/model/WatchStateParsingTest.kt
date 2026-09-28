package tv.onscreen.mobile.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiResponse

/**
 * Wire-shape pins for the v2.5 watch-state fields, parsed through the
 * generated Moshi adapters with JSON copied from the Go handlers'
 * response structs (hub.go, items_watched.go, libraries.go, items.go).
 * Also pins that pre-v2.5 payloads (no new keys) still parse.
 */
class WatchStateParsingTest {

    private val moshi = Moshi.Builder().build()

    private fun <T> envelope(type: Class<T>) =
        moshi.adapter<ApiResponse<T>>(Types.newParameterizedType(ApiResponse::class.java, type))

    @Test
    fun `up-next envelope parses a resume episode`() {
        val json = """{"data":{"mode":"resume","episode":{"id":"e4","title":"Four",
            "season_id":"s3","season_number":3,"episode_number":4,
            "view_offset_ms":61000,"duration_ms":1800000,"thumb_path":"t.jpg"}}}"""
        val up = envelope(UpNext::class.java).fromJson(json)!!.data
        assertThat(up.mode).isEqualTo(UpNextMode.RESUME)
        assertThat(up.episode!!.season_id).isEqualTo("s3")
        assertThat(up.episode!!.episode_number).isEqualTo(4)
        assertThat(up.episode!!.view_offset_ms).isEqualTo(61000)
    }

    @Test
    fun `up-next none omits the episode`() {
        val up = envelope(UpNext::class.java).fromJson("""{"data":{"mode":"none"}}""")!!.data
        assertThat(up.mode).isEqualTo(UpNextMode.NONE)
        assertThat(up.episode).isNull()
    }

    @Test
    fun `hub parses next_up episode tiles and plan_to_watch`() {
        val json = """{"data":{"continue_watching":[],"recently_added":[],
            "recently_added_by_library":[],"trending":[],
            "next_up":[{"id":"e5","title":"Five","show_title":"Show","show_id":"sh",
              "type":"episode","season_number":2,"episode_number":5,"updated_at":1}],
            "plan_to_watch":[{"id":"m","title":"Film","type":"movie","updated_at":2}]}}"""
        val hub = envelope(HubData::class.java).fromJson(json)!!.data
        val tile = hub.next_up.single()
        assertThat(tile.show_title).isEqualTo("Show")
        assertThat(tile.show_id).isEqualTo("sh")
        assertThat(tile.season_number).isEqualTo(2)
        assertThat(tile.episode_number).isEqualTo(5)
        assertThat(hub.plan_to_watch.map { it.id }).containsExactly("m")
    }

    @Test
    fun `pre-v2_5 hub without the new rows still parses`() {
        val json = """{"data":{"continue_watching":[],"recently_added":[],
            "recently_added_by_library":[],"trending":[]}}"""
        val hub = envelope(HubData::class.java).fromJson(json)!!.data
        assertThat(hub.next_up).isEmpty()
        assertThat(hub.plan_to_watch).isEmpty()
    }

    @Test
    fun `library items carry watch fields when present`() {
        val adapter = moshi.adapter(MediaItem::class.java)
        val show = adapter.fromJson("""{"id":"s","title":"S","type":"show",
            "created_at":"2026-01-01T00:00:00Z","updated_at":1700000000000,
            "leaf_count":10,"unwatched_count":4}""")!!
        assertThat(show.leaf_count).isEqualTo(10)
        assertThat(show.unwatched_count).isEqualTo(4)
        val movie = adapter.fromJson("""{"id":"m","title":"M","type":"movie",
            "created_at":"2026-01-01T00:00:00Z","updated_at":1700000000000,
            "watch_state":"in_progress","view_offset_ms":5000}""")!!
        assertThat(movie.watch_state).isEqualTo(WatchStateValue.IN_PROGRESS)
        assertThat(movie.view_offset_ms).isEqualTo(5000)
        val old = adapter.fromJson("""{"id":"m","title":"M","type":"movie",
            "created_at":"2026-01-01T00:00:00Z","updated_at":1700000000000}""")!!
        assertThat(old.watch_state).isNull()
        assertThat(old.unwatched_count).isNull()
    }

    @Test
    fun `item detail carries watch_state`() {
        val d = moshi.adapter(ItemDetail::class.java).fromJson("""{"id":"m","library_id":"l",
            "title":"M","type":"movie","genres":[],"view_offset_ms":0,
            "watch_state":"watched","is_favorite":false,"updated_at":1}""")!!
        assertThat(d.watch_state).isEqualTo(WatchStateValue.WATCHED)
    }

    @Test
    fun `random pick parses`() {
        val r = envelope(RandomLibraryItem::class.java).fromJson("""{"data":{"id":"x","type":"show"}}""")!!.data
        assertThat(r).isEqualTo(RandomLibraryItem("x", "show"))
    }
}
