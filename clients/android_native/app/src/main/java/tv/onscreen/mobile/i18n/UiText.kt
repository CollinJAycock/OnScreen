package tv.onscreen.mobile.i18n

import android.content.Context
import android.content.res.Resources
import androidx.annotation.PluralsRes
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalResources

/**
 * Text that a ViewModel or a pure helper hands to the UI, which resolves it
 * in the app's language when it shows it. A ViewModel never builds English
 * itself: it carries a string resource and its arguments, a plural, or text
 * that is never translated (titles, names and messages from the server).
 *
 * Arguments may themselves be [UiText]; they are resolved first. Plain
 * values are equal by value, so unit tests compare a [UiText] directly
 * (`UiText.res(R.string.bookmark_chapter_n, 3)`) without Android resources.
 */
sealed interface UiText {
    data class Res(@param:StringRes val id: Int, val args: List<Any> = emptyList()) : UiText

    /** A `<plurals>` resource: [count] picks the form, [args] fill it
     *  (by default the count itself, as `%1$d`). */
    data class Plural(
        @param:PluralsRes val id: Int,
        val count: Int,
        val args: List<Any> = listOf(count),
    ) : UiText

    /** Never translated: server data, names, a server's own message. */
    data class Raw(val text: String) : UiText

    companion object {
        fun res(@StringRes id: Int, vararg args: Any): UiText = Res(id, args.toList())

        fun plural(@PluralsRes id: Int, count: Int, vararg args: Any): UiText =
            Plural(id, count, if (args.isEmpty()) listOf(count) else args.toList())

        fun raw(text: String): UiText = Raw(text)
    }
}

/** Resolve outside composition (a notification, a toast, a worker). For
 *  text built from the application context, pass
 *  [AppLocales.localized] (context) so it follows the app's language on
 *  Android 12 and older too. */
fun UiText.asString(context: Context): String = asString(context.resources)

fun UiText.asString(resources: Resources): String = when (this) {
    is UiText.Res ->
        if (args.isEmpty()) resources.getString(id)
        else resources.getString(id, *resolveArgs(args, resources))
    is UiText.Plural -> resources.getQuantityString(id, count, *resolveArgs(args, resources))
    is UiText.Raw -> text
}

/** Resolve in composition, like stringResource. */
@Composable
fun UiText.asString(): String = asString(LocalResources.current)

private fun resolveArgs(args: List<Any>, resources: Resources): Array<Any> =
    args.map { if (it is UiText) it.asString(resources) else it }.toTypedArray()
