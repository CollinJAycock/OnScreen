package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class Library(
    val id: String,
    val name: String,
    val type: String,
    val created_at: String,
    val updated_at: String,
)

/** GET /libraries/{id}/items/position: [offset] is the item's 0-based row in
 *  the `/items` listing for the same parameters, [total] that listing's rows. */
@JsonClass(generateAdapter = true)
data class LibraryItemPosition(
    val offset: Int,
    val total: Int = 0,
)
