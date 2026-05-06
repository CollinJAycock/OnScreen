package tv.onscreen.mobile.ui.pair

import java.net.URLEncoder

/**
 * Pure helpers for the SSO bridge — the Custom Tabs detour that lets
 * a user sign in via OIDC / SAML / LDAP / local on the web, then
 * auto-claims a pair PIN to deliver a token pair to the native app.
 *
 * URL build + validation lives here so the round-trip math (especially
 * the same-origin guard) is JVM-testable in isolation. The
 * Custom-Tabs invocation itself is in [SsoLauncher].
 */
object SsoBridge {

    /**
     * Build the URL to load in Custom Tabs for the SSO bridge. The
     * web `/pair` page accepts:
     *   - `?code=N` — pre-fills the pairing PIN
     *   - `&auto=1` — auto-claims when the user is signed in
     *   - `&device_name=…` — labels the resulting session row
     *
     * If the user isn't signed in yet, `/pair` redirects to
     * `/login?next=…` which honors `next=` for local + LDAP and
     * stashes it in sessionStorage for the OIDC / SAML round-trip.
     * Either way, after sign-in the user lands back on `/pair?code=N`,
     * which auto-claims, and the polling app receives the tokens.
     *
     * [serverUrl] is the validated server origin (no trailing slash).
     * [pin] is the 6-digit pairing code; we don't validate length here
     * (caller has it from the server response).
     */
    fun buildPairUrl(serverUrl: String, pin: String, deviceName: String): String {
        val origin = serverUrl.trimEnd('/')
        val nameQ = if (deviceName.isNotBlank())
            "&device_name=" + URLEncoder.encode(deviceName, "UTF-8")
        else ""
        return "$origin/pair?code=$pin&auto=1$nameQ"
    }

    /**
     * Sanity-check the server URL before handing it to Custom Tabs.
     * Refuses anything that isn't an absolute http(s) URL — Custom
     * Tabs will silently fail on a bare-host or relative path, and
     * we'd rather catch it here with a clear "couldn't open browser"
     * surface than leak a confusing no-op to the user.
     */
    fun isLaunchableServerUrl(serverUrl: String): Boolean {
        val trimmed = serverUrl.trim()
        if (trimmed.isEmpty()) return false
        // Manual scheme check (Uri.parse pulled in android.net which
        // is unavailable in JVM unit tests). Custom Tabs only
        // launches http(s); a user typing a bare host gets a clear
        // refusal here rather than a silent no-op there.
        val schemeEnd = trimmed.indexOf("://")
        if (schemeEnd <= 0) return false
        val scheme = trimmed.substring(0, schemeEnd).lowercase()
        return scheme == "http" || scheme == "https"
    }
}
