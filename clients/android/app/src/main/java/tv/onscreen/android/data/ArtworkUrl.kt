package tv.onscreen.android.data

import android.net.Uri

/**
 * Builds a full artwork URL for Coil image loading. Default width targets
 * TV poster cards (~240dp @ 320dpi + 1.2× focus scale ≈ 576px).
 *
 * The scheme is lowercased because Coil's fetcher matcher is
 * case-sensitive on the URL scheme (`http://` / `https://`) while
 * OkHttp's HttpUrl parser is not. A server URL saved as
 * `Https://example.com` works fine for Retrofit / direct-play but
 * makes Coil throw "Unable to create a fetcher that supports"
 * — symptom is every poster on the home screen rendering as a flat
 * colour tile while video playback still works. Normalising here
 * fixes both existing installs (capital-H stored) and any future
 * inputs the setup flow forgot to canonicalise.
 */
fun artworkUrl(serverUrl: String, path: String, width: Int = 500): String {
    // Uri.encode preserves path separators ('/') and encodes spaces and special chars.
    val encoded = path.split("/").joinToString("/") { Uri.encode(it) }
    return "${normaliseScheme(serverUrl)}/artwork/${encoded}?w=$width"
}

/** Lowercase the scheme prefix of an http(s) URL. Pass-through for
 *  values that don't start with http:// / https:// in any casing. */
internal fun normaliseScheme(url: String): String {
    val sep = "://"
    val idx = url.indexOf(sep)
    if (idx <= 0) return url
    val scheme = url.substring(0, idx).lowercase()
    return scheme + url.substring(idx)
}
