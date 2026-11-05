package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * One entry of a library listing's A-Z jump index
 * (`GET /libraries/{id}/items/letters`, v2.7): the [letter] ("#" for
 * titles that don't start with A-Z, otherwise "A".."Z"), how many items
 * start with it, and [offset], the 0-based row of the first of them in
 * the `/items` listing asked for with the same parameters. Entries come in
 * listing order and only for letters that have items.
 */
@JsonClass(generateAdapter = true)
data class LibraryLetter(
    val letter: String,
    val count: Int = 0,
    val offset: Int = 0,
)
