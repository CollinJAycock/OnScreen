package tv.onscreen.android.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import org.junit.Test

/** Multi-episode files (v2.6): index_end on item detail and children. */
class EpisodeRangeTest {

    @Test
    fun `a range runs from index to index_end`() {
        assertThat(EpisodeRange.end(1, 2)).isEqualTo(2)
        assertThat(EpisodeRange.last(1, 2)).isEqualTo(2)
    }

    @Test
    fun `a single episode has no range and ends at its own index`() {
        assertThat(EpisodeRange.end(4, null)).isNull()
        assertThat(EpisodeRange.last(4, null)).isEqualTo(4)
    }

    @Test
    fun `an index_end that doesn't run past index reads as a single episode`() {
        assertThat(EpisodeRange.end(3, 3)).isNull()
        assertThat(EpisodeRange.end(3, 2)).isNull()
        assertThat(EpisodeRange.last(3, 2)).isEqualTo(3)
        assertThat(EpisodeRange.end(null, 2)).isNull()
    }

    @Test
    fun `children and detail decode index_end, null where the server omits it`() {
        val moshi = Moshi.Builder().build()
        val multi = moshi.adapter(ChildItem::class.java).fromJson(
            """{"id":"a","title":"A","type":"episode","index":1,"index_end":2}""",
        )!!
        val single = moshi.adapter(ChildItem::class.java).fromJson(
            """{"id":"b","title":"B","type":"episode","index":3}""",
        )!!
        val detail = moshi.adapter(ItemDetail::class.java).fromJson(
            """{"id":"a","library_id":"l","title":"A","type":"episode","index":1,"index_end":2}""",
        )!!

        assertThat(multi.index_end).isEqualTo(2)
        assertThat(multi.lastIndex).isEqualTo(2)
        assertThat(single.index_end).isNull()
        assertThat(single.lastIndex).isEqualTo(3)
        assertThat(detail.index_end).isEqualTo(2)
    }
}
