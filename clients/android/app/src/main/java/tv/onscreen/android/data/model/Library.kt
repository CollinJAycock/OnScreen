package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class Library(
    val id: String,
    val name: String,
    val type: String,
    val created_at: String,
    val updated_at: String,
    /** v2.1. When false (default) every authenticated user can see
     *  this library. When true, access requires an explicit row in
     *  `library_access`. Admins always bypass the check. The TV
     *  client only ever sees libraries the caller has access to,
     *  so this field is informational — useful if a future settings
     *  surface needs to render a privacy badge. */
    val is_private: Boolean = false,
    /** v2.1. When true on a private library, every newly-created
     *  non-admin user (invite accept, OIDC/SAML/LDAP JIT
     *  provisioning, admin-create) is automatically granted access.
     *  Surface this on admin settings screens; non-admin clients
     *  can ignore it. */
    val auto_grant_new_users: Boolean = false,
)

/** One entry of GET /libraries/{id}/genres. The server returns
 *  `[{"name":"Action","count":42}]`; decoding that into a plain
 *  List<String> threw, and LibraryRepository's catch-all turned the throw
 *  into an empty list — so the genre filter was permanently empty with no
 *  error anywhere. */
@JsonClass(generateAdapter = true)
data class GenreCount(
    val name: String,
    val count: Long = 0,
)
