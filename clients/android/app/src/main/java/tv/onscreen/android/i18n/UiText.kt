package tv.onscreen.android.i18n

import android.content.Context
import android.content.res.Resources
import androidx.annotation.PluralsRes
import androidx.annotation.StringRes

/**
 * Text a ViewModel or a pure helper hands the UI without resolving it: a
 * string resource with its arguments, a plural, or text that is already
 * what to show (the server's message, a title). The UI resolves it with its
 * own context when it draws, so it is always in the language on screen —
 * a ViewModel outlives the activity recreate a language change causes.
 *
 * Arguments may be UiText themselves (a report's kind inside "You reported
 * “%1$s”…"); they are resolved first.
 */
sealed interface UiText {

    /** Text shown as it is: server data, a server message, a codec name. */
    data class Raw(val text: String) : UiText

    /** A string resource; [args] fill its `%1$s` / `%1$d` placeholders. */
    data class Res(@param:StringRes val id: Int, val args: List<Any>) : UiText {
        constructor(@StringRes id: Int, vararg args: Any) : this(id, args.toList())
    }

    /** A plural resource picked by [count]; [args] fill its placeholders
     *  (by default the count itself). */
    data class Plural(@param:PluralsRes val id: Int, val count: Int, val args: List<Any> = listOf(count)) : UiText

    /** [parts] joined with [separator] (" · " in a label). */
    data class Join(val parts: List<UiText>, val separator: String) : UiText

    fun resolve(context: Context): String = resolve(context.resources)

    fun resolve(res: Resources): String = resolveWith(
        string = { id, args -> if (args.isEmpty()) res.getString(id) else res.getString(id, *args) },
        plural = { id, count, args -> res.getQuantityString(id, count, *args) },
    )

    /**
     * Resolve with lookups other than Resources (the unit tests read the
     * string XML). [string] and [plural] get the arguments already resolved.
     */
    fun resolveWith(
        string: (Int, Array<Any>) -> String,
        plural: (Int, Int, Array<Any>) -> String,
    ): String = when (this) {
        is Raw -> text
        is Res -> string(id, args.map { it.resolveArg(string, plural) }.toTypedArray())
        is Plural -> plural(id, count, args.map { it.resolveArg(string, plural) }.toTypedArray())
        is Join -> parts.joinToString(separator) { it.resolveWith(string, plural) }
    }

    companion object {
        /** [parts] as one label: the part itself when there is just one. */
        fun join(parts: List<UiText>, separator: String): UiText =
            parts.singleOrNull() ?: Join(parts, separator)
    }
}

private fun Any.resolveArg(
    string: (Int, Array<Any>) -> String,
    plural: (Int, Int, Array<Any>) -> String,
): Any = if (this is UiText) resolveWith(string, plural) else this
