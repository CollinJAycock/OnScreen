package tv.onscreen.mobile.data

import android.net.Uri

/**
 * Builds a full artwork URL for Coil image loading. Default width targets
 * TV poster cards (~240dp @ 320dpi + 1.2× focus scale ≈ 576px).
 */
fun artworkUrl(serverUrl: String, path: String, width: Int = 500): String {
    // Uri.encode preserves path separators ('/') and encodes spaces and special chars.
    val encoded = path.split("/").joinToString("/") { Uri.encode(it) }
    return "${serverUrl}/artwork/${encoded}?w=$width"
}
