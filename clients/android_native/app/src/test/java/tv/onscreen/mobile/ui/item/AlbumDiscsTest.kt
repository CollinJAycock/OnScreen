package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.ChildItem

/** Port of web/src/lib/albumDiscs.test.ts. */
class AlbumDiscsTest {

    private fun t(id: String, index: Int? = null, disc: Int? = null) =
        ChildItem(id = id, title = "T-$id", type = "track", index = index, disc_number = disc)

    private fun List<DiscGroup>.shape() = map { g -> g.disc to g.tracks.map { it.id } }

    @Test
    fun `a single-disc album is one group`() {
        val groups = albumDiscGroups(listOf(t("a", 1, disc = 1), t("b", 2, disc = 1)))
        assertThat(groups.shape()).containsExactly(1 to listOf("a", "b"))
    }

    @Test
    fun `tracks without a disc number are one group on disc 1`() {
        val groups = albumDiscGroups(listOf(t("b", 2), t("a", 1)))
        assertThat(groups.shape()).containsExactly(1 to listOf("a", "b"))
    }

    @Test
    fun `shuffled tracks group by disc in track order`() {
        val groups = albumDiscGroups(
            listOf(t("d2t1", 1, disc = 2), t("d1t2", 2, disc = 1), t("d1t1", 1, disc = 1), t("d2t2", 2, disc = 2)),
        )
        assertThat(groups.shape()).containsExactly(
            1 to listOf("d1t1", "d1t2"),
            2 to listOf("d2t1", "d2t2"),
        ).inOrder()
    }

    @Test
    fun `a track without a disc number counts as disc 1`() {
        val groups = albumDiscGroups(listOf(t("2-1", 1, disc = 2), t("1-2", 2), t("1-1", 1, disc = 1)))
        assertThat(groups.shape()).containsExactly(
            1 to listOf("1-1", "1-2"),
            2 to listOf("2-1"),
        ).inOrder()
    }

    @Test
    fun `an unnumbered track goes last on its disc`() {
        val groups = albumDiscGroups(listOf(t("none", disc = 1), t("2-1", 1, disc = 2), t("1-1", 1, disc = 1)))
        assertThat(groups.shape()).containsExactly(
            1 to listOf("1-1", "none"),
            2 to listOf("2-1"),
        ).inOrder()
    }

    @Test
    fun `three discs give three groups`() {
        val groups = albumDiscGroups(listOf(t("2-1", 1, disc = 2), t("1-1", 1), t("1-2", 2), t("3-1", 1, disc = 3)))
        assertThat(groups.map { it.disc }).containsExactly(1, 2, 3).inOrder()
    }

    @Test
    fun `no tracks gives no groups`() {
        assertThat(albumDiscGroups(emptyList())).isEmpty()
    }

    @Test
    fun `the groups flatten to the play order and leave the input alone`() {
        val children = listOf(
            t("d2t2", 2, disc = 2), t("none", disc = 1), t("d1t3", 3), t("d2t1", 1, disc = 2),
            t("d1t1", 1, disc = 1), t("d3t1", 1, disc = 3),
        )
        val before = children.map { it.id }
        val flat = albumDiscGroups(children).flatMap { it.tracks }
        assertThat(flat).containsExactlyElementsIn(children.sortedWith(ChildItem.PLAY_ORDER)).inOrder()
        assertThat(children.map { it.id }).isEqualTo(before)
    }
}
