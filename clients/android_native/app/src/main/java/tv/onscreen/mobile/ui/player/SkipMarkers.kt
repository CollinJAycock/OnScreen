package tv.onscreen.mobile.ui.player

import tv.onscreen.mobile.data.model.Marker

/**
 * Pure helpers for the Skip-intro / Skip-credits overlay. The lookup
 * + dismissal-set logic lives outside the @Composable so it can be
 * unit-tested without spinning up an ExoPlayer.
 */
object SkipMarkers {

    /**
     * Stable per-marker key for the dismissal set. We don't have a
     * server-issued marker id on the model (markers are derived from
     * intro / credits detection per-item), so we synthesise a key
     * from `(kind, start_ms, end_ms)` — that triple is unique per
     * marker per item and stable across the session.
     */
    fun markerKey(m: Marker): String = "${m.kind}:${m.start_ms}-${m.end_ms}"

    /**
     * Find the marker covering [positionMs] that the user hasn't
     * dismissed. Returns null when no marker covers the position or
     * the only matching markers are all in [dismissed].
     *
     * Order: first match wins. The server emits intro before credits
     * by start_ms, so an item with both gets the intro skip first
     * (which the user is more likely to want), then credits.
     */
    fun activeAt(
        markers: List<Marker>,
        positionMs: Long,
        dismissed: Set<String>,
    ): Marker? = markers.firstOrNull { m ->
        positionMs in m.start_ms..m.end_ms && markerKey(m) !in dismissed
    }
}
