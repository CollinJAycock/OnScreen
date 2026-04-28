package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * Intro / credits marker for an episode. The server detects these
 * automatically (intromarker package) or accepts admin-supplied
 * overrides; the client treats both the same way — when player
 * position enters the [start_ms, end_ms] window, surface a
 * "SKIP INTRO" / "SKIP CREDITS" button that jumps past the marker.
 *
 * Returned only for items of type "episode"; movies and containers
 * get an empty list so the client can call the endpoint
 * unconditionally without branching on item type.
 */
@JsonClass(generateAdapter = true)
data class Marker(
    val kind: String, // "intro" | "credits"
    val start_ms: Long,
    val end_ms: Long,
    val source: String = "auto", // "auto" | "manual" | "chapter"
)
