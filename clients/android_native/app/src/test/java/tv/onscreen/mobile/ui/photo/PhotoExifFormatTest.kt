package tv.onscreen.mobile.ui.photo

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.PhotoExif

class PhotoExifFormatTest {

    @Test
    fun `null exif yields empty rows`() {
        assertThat(PhotoExifFormat.rows(null)).isEmpty()
    }

    @Test
    fun `empty exif yields empty rows`() {
        assertThat(PhotoExifFormat.rows(PhotoExif())).isEmpty()
    }

    @Test
    fun `populated exif yields rows in canonical order`() {
        val exif = PhotoExif(
            taken_at = "2024-09-15T18:30:00Z",
            camera_make = "Sony",
            camera_model = "ILCE-7M4",
            lens_model = "FE 24-70mm F2.8 GM",
            focal_length_mm = 50.0,
            aperture = 2.8,
            shutter_speed = "1/250",
            iso = 400,
            width = 6336,
            height = 4224,
            gps_lat = 37.7749,
            gps_lon = -122.4194,
        )
        val rows = PhotoExifFormat.rows(exif)
        // Canonical order: Taken / Camera / Lens / Exposure / Focal /
        // Dimensions / GPS. A user scanning top-to-bottom hits the
        // most-asked-about fields first.
        assertThat(rows.map { it.label }).containsExactly(
            "Taken", "Camera", "Lens", "Exposure", "Focal length", "Dimensions", "GPS",
        ).inOrder()
        assertThat(rows.first { it.label == "Camera" }.value).isEqualTo("Sony ILCE-7M4")
        assertThat(rows.first { it.label == "Exposure" }.value).isEqualTo("f/2.8 · 1/250 · ISO 400")
        assertThat(rows.first { it.label == "Focal length" }.value).isEqualTo("50mm")
        assertThat(rows.first { it.label == "Dimensions" }.value).isEqualTo("6336 × 4224")
    }

    @Test
    fun `cameraLabel handles missing make or model`() {
        assertThat(PhotoExifFormat.cameraLabel(PhotoExif(camera_model = "α7 IV"))).isEqualTo("α7 IV")
        assertThat(PhotoExifFormat.cameraLabel(PhotoExif(camera_make = "Sony"))).isEqualTo("Sony")
        assertThat(PhotoExifFormat.cameraLabel(PhotoExif())).isNull()
    }

    @Test
    fun `cameraLabel dedups make-prefixed model`() {
        // Sony cameras often emit make="Sony" and model="Sony α7 IV"
        // — concatenating naively would render "Sony Sony α7 IV".
        val exif = PhotoExif(camera_make = "Sony", camera_model = "Sony ILCE-7M4")
        assertThat(PhotoExifFormat.cameraLabel(exif)).isEqualTo("Sony ILCE-7M4")
    }

    @Test
    fun `formatAperture strips trailing zero on whole-stop values`() {
        // f/8 not f/8.0; f/1.4 stays as-is.
        assertThat(PhotoExifFormat.formatAperture(8.0)).isEqualTo("f/8")
        assertThat(PhotoExifFormat.formatAperture(1.4)).isEqualTo("f/1.4")
        assertThat(PhotoExifFormat.formatAperture(2.8)).isEqualTo("f/2.8")
        // Round to one decimal — 5.66 → 5.7.
        assertThat(PhotoExifFormat.formatAperture(5.66)).isEqualTo("f/5.7")
    }

    @Test
    fun `formatFocal rounds to integer mm`() {
        assertThat(PhotoExifFormat.formatFocal(50.0)).isEqualTo("50mm")
        assertThat(PhotoExifFormat.formatFocal(34.7)).isEqualTo("35mm")
        assertThat(PhotoExifFormat.formatFocal(35.5)).isEqualTo("36mm")
    }

    @Test
    fun `formatGps emits NS_EW labels with sign`() {
        // Northern + Western hemisphere (San Francisco)
        assertThat(PhotoExifFormat.formatGps(37.7749, -122.4194))
            .isEqualTo("37.7749° N, 122.4194° W")
        // Southern + Eastern (Sydney)
        assertThat(PhotoExifFormat.formatGps(-33.8688, 151.2093))
            .isEqualTo("33.8688° S, 151.2093° E")
        // Equator + Prime Meridian
        assertThat(PhotoExifFormat.formatGps(0.0, 0.0)).isEqualTo("0.0000° N, 0.0000° E")
    }

    @Test
    fun `mapsGeoUri builds geo URI with zoom`() {
        // geo: scheme is the standard Android-side handoff to the
        // user's installed maps app (Google Maps, OsmAnd, etc.).
        assertThat(PhotoExifFormat.mapsGeoUri(37.7749, -122.4194))
            .isEqualTo("geo:37.7749,-122.4194?z=15")
    }

    @Test
    fun `mapsHttpsUrl falls back to google maps web link`() {
        assertThat(PhotoExifFormat.mapsHttpsUrl(37.7749, -122.4194))
            .isEqualTo("https://www.google.com/maps?q=37.7749,-122.4194")
    }

    @Test
    fun `aperture without iso or shutter still emits an Exposure row`() {
        val exif = PhotoExif(aperture = 4.0)
        val rows = PhotoExifFormat.rows(exif)
        assertThat(rows).hasSize(1)
        assertThat(rows[0].label).isEqualTo("Exposure")
        assertThat(rows[0].value).isEqualTo("f/4")
    }
}
