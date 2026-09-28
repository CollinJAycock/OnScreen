package tv.onscreen.android.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import org.junit.Test
import tv.onscreen.android.data.api.ApiListResponse
import tv.onscreen.android.data.api.ApiResponse

/**
 * Wire shapes of the v2.5 watch-state fields, as the Go handlers emit them
 * (internal/api/v1/hub.go, items_watched.go, library_watch.go, items.go), and
 * the older-server shapes that omit them.
 */
class WatchStateJsonTest {

    private val moshi = Moshi.Builder().build()

    private inline fun <reified T> envelope(json: String): T? {
        val type = Types.newParameterizedType(ApiResponse::class.java, T::class.java)
        return moshi.adapter<ApiResponse<T>>(type).fromJson(json)?.data
    }

    @Test
    fun `hub next_up and plan_to_watch decode with episode fields`() {
        val hub = envelope<HubData>(
            """
            {"data":{
              "continue_watching":[],"continue_watching_tv":[],"continue_watching_movies":[],
              "continue_watching_other":[],"recently_added":[],"recently_added_by_library":[],
              "trending":[],
              "next_up":[{"id":"e5","title":"Five","show_title":"Show","type":"episode","year":2020,
                "poster_path":"p.jpg","thumb_path":"t.jpg","duration_ms":1500000,"updated_at":1700000000000,
                "show_id":"s1","season_number":2,"episode_number":5}],
              "plan_to_watch":[{"id":"m1","title":"Movie","type":"movie","updated_at":1}]
            }}
            """.trimIndent(),
        )!!
        val tile = hub.next_up.single()
        assertThat(tile.show_title).isEqualTo("Show")
        assertThat(tile.show_id).isEqualTo("s1")
        assertThat(tile.season_number).isEqualTo(2)
        assertThat(tile.episode_number).isEqualTo(5)
        assertThat(hub.plan_to_watch.map { it.id }).containsExactly("m1")
    }

    @Test
    fun `an older hub without the watch rows still parses`() {
        val hub = envelope<HubData>("""{"data":{"continue_watching":[],"recently_added":[],"trending":[]}}""")!!
        assertThat(hub.next_up).isEmpty()
        assertThat(hub.plan_to_watch).isEmpty()
    }

    @Test
    fun `up-next decodes every mode`() {
        val resume = envelope<UpNext>(
            """{"data":{"mode":"resume","episode":{"id":"e","title":"T","season_id":"se","season_number":3,
              "episode_number":4,"view_offset_ms":90000,"duration_ms":1500000,"thumb_path":"x.jpg"}}}""",
        )!!
        assertThat(resume.mode).isEqualTo("resume")
        assertThat(resume.episode?.view_offset_ms).isEqualTo(90_000)
        assertThat(resume.episode?.season_id).isEqualTo("se")

        // "none" omits episode entirely.
        val none = envelope<UpNext>("""{"data":{"mode":"none"}}""")!!
        assertThat(none.mode).isEqualTo("none")
        assertThat(none.episode).isNull()
    }

    @Test
    fun `library items carry watch fields when present and parse without them`() {
        val type = Types.newParameterizedType(ApiListResponse::class.java, MediaItem::class.java)
        val page = moshi.adapter<ApiListResponse<MediaItem>>(type).fromJson(
            """
            {"data":[
              {"id":"m","title":"M","type":"movie","created_at":"2026-01-01T00:00:00Z","updated_at":1,
               "duration_ms":6000000,"watch_state":"in_progress","view_offset_ms":600000},
              {"id":"s","title":"S","type":"show","created_at":"2026-01-01T00:00:00Z","updated_at":1,
               "leaf_count":10,"unwatched_count":3},
              {"id":"a","title":"A","type":"artist","created_at":"2026-01-01T00:00:00Z","updated_at":1}
            ],"meta":{"total":3,"cursor":""}}
            """.trimIndent(),
        )!!
        val (movie, show, artist) = page.data
        assertThat(movie.watch_state).isEqualTo("in_progress")
        assertThat(movie.view_offset_ms).isEqualTo(600_000)
        assertThat(show.leaf_count).isEqualTo(10)
        assertThat(show.unwatched_count).isEqualTo(3)
        assertThat(artist.watch_state).isNull()
        assertThat(artist.unwatched_count).isNull()
    }

    @Test
    fun `item detail watch_state is optional`() {
        val watched = envelope<ItemDetail>(
            """{"data":{"id":"m","library_id":"l","title":"M","type":"movie","genres":[],
              "view_offset_ms":0,"watch_state":"watched","is_favorite":false,"updated_at":1}}""",
        )!!
        assertThat(watched.watch_state).isEqualTo("watched")

        val old = envelope<ItemDetail>(
            """{"data":{"id":"m","library_id":"l","title":"M","type":"movie","genres":[],
              "view_offset_ms":0,"is_favorite":false,"updated_at":1}}""",
        )!!
        assertThat(old.watch_state).isNull()
    }
}
