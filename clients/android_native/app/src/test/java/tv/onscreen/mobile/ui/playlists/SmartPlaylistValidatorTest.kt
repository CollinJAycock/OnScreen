package tv.onscreen.mobile.ui.playlists

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class SmartPlaylistValidatorTest {

    private fun draft(
        name: String = "My picks",
        types: Set<String> = setOf("movie"),
        genres: String = "",
        yMin: String = "",
        yMax: String = "",
        rating: String = "",
        limit: String = "",
    ) = SmartPlaylistRulesDraft(
        name = name,
        types = types,
        genresCsv = genres,
        yearMin = yMin,
        yearMax = yMax,
        ratingMin = rating,
        limit = limit,
    )

    private fun assertOk(r: SmartPlaylistValidator.Result): SmartPlaylistValidator.Result.Ok {
        assertThat(r).isInstanceOf(SmartPlaylistValidator.Result.Ok::class.java)
        return r as SmartPlaylistValidator.Result.Ok
    }

    private fun assertError(r: SmartPlaylistValidator.Result): String {
        assertThat(r).isInstanceOf(SmartPlaylistValidator.Result.Error::class.java)
        return (r as SmartPlaylistValidator.Result.Error).message
    }

    @Test
    fun `minimal draft with name and one type validates`() {
        val ok = assertOk(SmartPlaylistValidator.validate(draft()))
        assertThat(ok.name).isEqualTo("My picks")
        assertThat(ok.rules.types).containsExactly("movie")
        // Empty optional fields → null on the wire shape.
        assertThat(ok.rules.year_min).isNull()
        assertThat(ok.rules.year_max).isNull()
        assertThat(ok.rules.rating_min).isNull()
        assertThat(ok.rules.limit).isNull()
        assertThat(ok.rules.genres).isEmpty()
    }

    @Test
    fun `name is required`() {
        assertThat(assertError(SmartPlaylistValidator.validate(draft(name = ""))))
            .contains("Name is required")
        assertThat(assertError(SmartPlaylistValidator.validate(draft(name = "   "))))
            .contains("Name is required")
    }

    @Test
    fun `name length cap`() {
        val long = "a".repeat(101)
        assertThat(assertError(SmartPlaylistValidator.validate(draft(name = long))))
            .contains("too long")
    }

    @Test
    fun `genres CSV trims and drops empties`() {
        val ok = assertOk(SmartPlaylistValidator.validate(
            draft(genres = "  Action ,  , Drama, ")
        ))
        assertThat(ok.rules.genres).containsExactly("Action", "Drama").inOrder()
    }

    @Test
    fun `year range bounds enforced and ordered`() {
        // Below 1800 = before motion pictures existed; above 2100 =
        // far-future placeholder. Catches "1024" typos.
        assertThat(assertError(SmartPlaylistValidator.validate(draft(yMin = "1500"))))
            .contains("out of range")
        assertThat(assertError(SmartPlaylistValidator.validate(draft(yMax = "2200"))))
            .contains("out of range")
        // min > max
        assertThat(assertError(SmartPlaylistValidator.validate(
            draft(yMin = "2000", yMax = "1990")
        ))).contains("min) must be ≤ year (max)")
        // Non-numeric
        assertThat(assertError(SmartPlaylistValidator.validate(draft(yMin = "twenty"))))
            .contains("whole number")
    }

    @Test
    fun `valid year range round-trips`() {
        val ok = assertOk(SmartPlaylistValidator.validate(draft(yMin = "1990", yMax = "2010")))
        assertThat(ok.rules.year_min).isEqualTo(1990)
        assertThat(ok.rules.year_max).isEqualTo(2010)
    }

    @Test
    fun `rating must be 0 to 10 numeric`() {
        assertThat(assertError(SmartPlaylistValidator.validate(draft(rating = "11"))))
            .contains("between 0 and 10")
        assertThat(assertError(SmartPlaylistValidator.validate(draft(rating = "-0.5"))))
            .contains("between 0 and 10")
        assertThat(assertError(SmartPlaylistValidator.validate(draft(rating = "abc"))))
            .contains("must be a number")
        val ok = assertOk(SmartPlaylistValidator.validate(draft(rating = "7.5")))
        assertThat(ok.rules.rating_min).isEqualTo(7.5)
    }

    @Test
    fun `limit must be 1 to 500`() {
        assertThat(assertError(SmartPlaylistValidator.validate(draft(limit = "0"))))
            .contains("1–500")
        assertThat(assertError(SmartPlaylistValidator.validate(draft(limit = "501"))))
            .contains("1–500")
        val ok = assertOk(SmartPlaylistValidator.validate(draft(limit = "100")))
        assertThat(ok.rules.limit).isEqualTo(100)
    }

    @Test
    fun `multiple types are preserved`() {
        val ok = assertOk(SmartPlaylistValidator.validate(
            draft(types = setOf("movie", "show"))
        ))
        assertThat(ok.rules.types).containsExactly("movie", "show")
    }

    @Test
    fun `empty types is allowed but flagged as likely-empty`() {
        // Server quirk: empty-types returns nothing, not "everything".
        // We accept the draft (user may want to fill in later) but
        // surface a hint so they aren't surprised.
        val empty = draft(types = emptySet())
        assertOk(SmartPlaylistValidator.validate(empty))
        assertThat(SmartPlaylistValidator.isLikelyEmpty(empty)).isTrue()
        assertThat(SmartPlaylistValidator.isLikelyEmpty(draft())).isFalse()
    }

    @Test
    fun `whitespace-only optional fields parse as null`() {
        val ok = assertOk(SmartPlaylistValidator.validate(
            draft(yMin = "   ", yMax = " ", rating = "", limit = "  ")
        ))
        assertThat(ok.rules.year_min).isNull()
        assertThat(ok.rules.year_max).isNull()
        assertThat(ok.rules.rating_min).isNull()
        assertThat(ok.rules.limit).isNull()
    }
}
