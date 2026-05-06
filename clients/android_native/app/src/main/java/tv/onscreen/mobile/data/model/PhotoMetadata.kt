package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * Per-photo EXIF metadata returned by `GET /items/{id}/exif`. Every
 * field is optional — a PNG with no EXIF block returns the empty
 * object; the UI hides null rows rather than rendering "—" placeholders.
 *
 * `taken_at` is RFC3339; UI parses to a date for display.
 */
@JsonClass(generateAdapter = true)
data class PhotoExif(
    val taken_at: String? = null,
    val camera_make: String? = null,
    val camera_model: String? = null,
    val lens_model: String? = null,
    val focal_length_mm: Double? = null,
    val aperture: Double? = null,
    val shutter_speed: String? = null,
    val iso: Int? = null,
    val flash: Boolean? = null,
    val orientation: Int? = null,
    val width: Int? = null,
    val height: Int? = null,
    val gps_lat: Double? = null,
    val gps_lon: Double? = null,
    val gps_alt: Double? = null,
)

/** Timeline bucket — one (year, month, count) row per month with at
 *  least one photo in the library. Sorted descending (newest first)
 *  by the server. */
@JsonClass(generateAdapter = true)
data class PhotoTimelineBucket(
    val year: Int,
    val month: Int, // 1-12
    val count: Int,
)

/** Geotagged photo for the map view. The server emits these via
 *  `GET /photos/map`; v1 phone client renders them as a list with
 *  open-in-maps intent rather than a real map render. */
@JsonClass(generateAdapter = true)
data class PhotoMapPoint(
    val id: String,
    val lat: Double,
    val lon: Double,
    val taken_at: String? = null,
    val poster_path: String? = null,
)
