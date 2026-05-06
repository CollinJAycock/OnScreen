package tv.onscreen.mobile.ui.pair

import android.content.Context
import android.net.Uri
import androidx.browser.customtabs.CustomTabsIntent

/**
 * Android-side glue that hands a pre-built SSO-bridge URL off to
 * Chrome Custom Tabs. Pure URL build lives in [SsoBridge]; this
 * file only owns the platform invocation so the rest of the SSO
 * flow stays JVM-testable.
 *
 * Custom Tabs is preferred over a regular Intent.ACTION_VIEW for
 * three reasons:
 *   - Stays in-app visually (custom toolbar colour, app icon)
 *   - Shares cookies with the user's default browser, so a previous
 *     web sign-in to the same OnScreen instance carries over and the
 *     SSO step is one tap
 *   - The user can dismiss with the system back button without
 *     leaving the app
 */
object SsoLauncher {

    /**
     * Launch [url] in a Custom Tabs session. Returns true on success,
     * false when no browser handler is installed (rare but possible
     * on stripped Android builds — the caller should surface "couldn't
     * open browser").
     */
    fun launch(context: Context, url: String): Boolean {
        return try {
            val intent = CustomTabsIntent.Builder()
                .setShowTitle(true)
                .build()
            intent.launchUrl(context, Uri.parse(url))
            true
        } catch (_: Throwable) {
            false
        }
    }
}
