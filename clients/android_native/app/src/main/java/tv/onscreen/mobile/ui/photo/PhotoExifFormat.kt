package tv.onscreen.mobile.ui.photo

import tv.onscreen.mobile.data.model.PhotoExif

/**
 * Pure helpers for the EXIF detail sheet. The "show only the rows
 * that have data" rule + the camera/lens/aperture/shutter/ISO label
 * formatting is broken out so it's testable without spinning up a
 * Compose preview.
 */
object PhotoExifFormat {

    /** One labelled row to render in the EXIF sheet. UI iterates this
     *  list and renders a label + value pair per entry. Empty list =
     *  no EXIF — sheet shows a "no metadata" placeholder. */
    data class Row(val label: String, val value: String)

    /**
     * Build the row list from an [exif] payload. Skips fields that
     * are null or empty. Order is intentional — chronological / camera
     * / lens / exposure / dimensions / GPS — the order a photo nerd
     * scans an info pane.
     */
    fun rows(exif: PhotoExif?): List<Row> {
        if (exif == null) return emptyList()
        val out = mutableListOf<Row>()

        exif.taken_at?.takeIf { it.isNotBlank() }?.let {
            out.add(Row("Taken", it))
        }
        // Camera = make + model when both present, model alone otherwise.
        cameraLabel(exif)?.let { out.add(Row("Camera", it)) }
        exif.lens_model?.takeIf { it.isNotBlank() }?.let {
            out.add(Row("Lens", it))
        }

        val exposureParts = mutableListOf<String>()
        exif.aperture?.let { exposureParts.add(formatAperture(it)) }
        exif.shutter_speed?.takeIf { it.isNotBlank() }?.let { exposureParts.add(it) }
        exif.iso?.let { exposureParts.add("ISO $it") }
        if (exposureParts.isNotEmpty()) {
            out.add(Row("Exposure", exposureParts.joinToString(" · ")))
        }

        exif.focal_length_mm?.let {
            out.add(Row("Focal length", formatFocal(it)))
        }
        if (exif.width != null && exif.height != null) {
            out.add(Row("Dimensions", "${exif.width} × ${exif.height}"))
        }
        if (exif.gps_lat != null && exif.gps_lon != null) {
            out.add(Row("GPS", formatGps(exif.gps_lat, exif.gps_lon)))
        }
        return out
    }

    /** "Sony α7 IV" / "α7 IV" / "Sony" — whichever combo is non-blank.
     *  Make-then-model when both present; model wins on collision so
     *  we don't get "Sony Sony α7 IV" if model already includes make. */
    internal fun cameraLabel(exif: PhotoExif): String? {
        val make = exif.camera_make?.trim().orEmpty()
        val model = exif.camera_model?.trim().orEmpty()
        return when {
            make.isEmpty() && model.isEmpty() -> null
            model.isEmpty() -> make
            make.isEmpty() -> model
            // Some cameras prefix model with make (e.g. "Sony ILCE-7M4");
            // dedup so we don't write "Sony Sony ILCE-7M4".
            model.startsWith(make, ignoreCase = true) -> model
            else -> "$make $model"
        }
    }

    /** "f/2.8" / "f/8" — strip trailing zeros so 8.0 → "f/8" but 1.4
     *  stays "f/1.4". Aperture values are typically one decimal place. */
    internal fun formatAperture(f: Double): String {
        val rounded = Math.round(f * 10) / 10.0
        return if (rounded == rounded.toInt().toDouble()) "f/${rounded.toInt()}"
        else "f/%.1f".format(rounded)
    }

    /** "35mm" — round to the nearest mm, drop the decimal. */
    internal fun formatFocal(mm: Double): String = "${Math.round(mm).toInt()}mm"

    /** Compact GPS label "37.7749° N, 122.4194° W" — for the EXIF sheet
     *  + the "open in maps" link. Latitude positive = N, negative = S;
     *  longitude positive = E, negative = W. */
    fun formatGps(lat: Double, lon: Double): String {
        val latLabel = "%.4f° %s".format(Math.abs(lat), if (lat >= 0) "N" else "S")
        val lonLabel = "%.4f° %s".format(Math.abs(lon), if (lon >= 0) "E" else "W")
        return "$latLabel, $lonLabel"
    }

    /** Build an `geo:` URI suitable for an external-app Intent. Points
     *  to the lat/lon with a sensible zoom level. Fallback path is
     *  `https://www.google.com/maps?q=lat,lon` for devices without a
     *  registered geo: handler — UI tries the Intent first and falls
     *  back to the URL. */
    fun mapsGeoUri(lat: Double, lon: Double): String = "geo:$lat,$lon?z=15"

    fun mapsHttpsUrl(lat: Double, lon: Double): String =
        "https://www.google.com/maps?q=$lat,$lon"
}
