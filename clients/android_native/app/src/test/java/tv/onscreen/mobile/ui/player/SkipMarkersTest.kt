package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.Marker

class SkipMarkersTest {

    private fun marker(
        kind: String = "intro",
        start: Long = 30_000,
        end: Long = 90_000,
    ) = Marker(kind = kind, start_ms = start, end_ms = end, source = "auto")

    @Test
    fun `markerKey is stable across calls and includes kind`() {
        val a = marker(kind = "intro", start = 0, end = 100)
        val b = marker(kind = "credits", start = 0, end = 100)
        // Same window, different kind → different keys (so a clip-show
        // episode with overlapping intro/credits markers can dismiss
        // them independently).
        assertThat(SkipMarkers.markerKey(a)).isNotEqualTo(SkipMarkers.markerKey(b))
        assertThat(SkipMarkers.markerKey(a)).isEqualTo(SkipMarkers.markerKey(a))
    }

    @Test
    fun `activeAt returns null when position is before any marker`() {
        val markers = listOf(marker(start = 30_000, end = 90_000))
        assertThat(SkipMarkers.activeAt(markers, 0, emptySet())).isNull()
        assertThat(SkipMarkers.activeAt(markers, 29_999, emptySet())).isNull()
    }

    @Test
    fun `activeAt returns the marker covering the position`() {
        val m = marker(start = 30_000, end = 90_000)
        // start_ms is inclusive (matches the existing Composable's
        // `position in start..end` check).
        assertThat(SkipMarkers.activeAt(listOf(m), 30_000, emptySet())).isEqualTo(m)
        assertThat(SkipMarkers.activeAt(listOf(m), 60_000, emptySet())).isEqualTo(m)
        assertThat(SkipMarkers.activeAt(listOf(m), 90_000, emptySet())).isEqualTo(m)
    }

    @Test
    fun `activeAt returns null past the marker end`() {
        val m = marker(start = 30_000, end = 90_000)
        assertThat(SkipMarkers.activeAt(listOf(m), 90_001, emptySet())).isNull()
    }

    @Test
    fun `activeAt skips dismissed markers`() {
        val intro = marker(kind = "intro", start = 0, end = 30_000)
        val credits = marker(kind = "credits", start = 60_000, end = 90_000)
        val dismissed = setOf(SkipMarkers.markerKey(intro))

        // Position inside the dismissed intro marker → null (don't
        // re-show the button after the user said no).
        assertThat(SkipMarkers.activeAt(listOf(intro, credits), 10_000, dismissed)).isNull()
        // Position inside the not-dismissed credits marker → still works.
        assertThat(SkipMarkers.activeAt(listOf(intro, credits), 70_000, dismissed))
            .isEqualTo(credits)
    }

    @Test
    fun `activeAt with multiple overlapping markers returns the first match`() {
        // Pathological but possible: a season recap (kind=intro) that
        // overlaps the actual show intro (kind=intro). First-match-wins
        // is the documented behaviour; without it we'd have to make a
        // priority ordering call the user didn't ask for.
        val a = marker(kind = "intro", start = 0, end = 60_000)
        val b = marker(kind = "intro", start = 30_000, end = 90_000)
        assertThat(SkipMarkers.activeAt(listOf(a, b), 45_000, emptySet())).isEqualTo(a)
    }

    @Test
    fun `activeAt on empty input returns null`() {
        assertThat(SkipMarkers.activeAt(emptyList(), 1000, emptySet())).isNull()
    }
}
