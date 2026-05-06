package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * Per-provider enabled-flag + display name. Returned by the
 * `/auth/{oidc,saml,ldap}/enabled` endpoints. Phone client fans out
 * the three calls in parallel on the login screen and decides which
 * extra controls to render.
 */
@JsonClass(generateAdapter = true)
data class AuthProviderStatus(
    val enabled: Boolean,
    /** Operator-set label, e.g. "Corporate AD" or "Google Workspace".
     *  Server defaults to the provider name when blank. */
    val display_name: String,
)

/** Aggregate snapshot the LoginScreen reads to decide what to show.
 *  All three flags can be true at once (a server with both OIDC and
 *  LDAP wired). All-false = local login is the only path; the
 *  extra-providers row stays hidden. */
data class AuthProviders(
    val ldap: AuthProviderStatus? = null,
    val oidc: AuthProviderStatus? = null,
    val saml: AuthProviderStatus? = null,
) {
    /** True if any browser-redirect provider is enabled. UI shows a
     *  "Sign in with SSO via web — pair this device" hint pointing
     *  the user at the existing pair flow. */
    val needsBrowserPairing: Boolean
        get() = (oidc?.enabled == true) || (saml?.enabled == true)

    /** True if LDAP is available; the form gets a toggle. */
    val ldapEnabled: Boolean
        get() = ldap?.enabled == true
}
