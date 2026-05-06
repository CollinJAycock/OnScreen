package tv.onscreen.mobile.ui.playlists

import tv.onscreen.mobile.data.model.SmartPlaylistRules

/**
 * UI-side draft of a smart-playlist rule set. The Compose builder
 * accumulates state into a [SmartPlaylistRulesDraft] across keystrokes;
 * [validate] turns it into either an error string or a server-shaped
 * [SmartPlaylistRules] ready for POST.
 *
 * Pure module — no Compose / Android imports — so the validation
 * grammar lives next to its tests instead of buried inside an
 * @Composable.
 */
data class SmartPlaylistRulesDraft(
    val name: String = "",
    val types: Set<String> = emptySet(),
    val genresCsv: String = "",
    val yearMin: String = "",
    val yearMax: String = "",
    val ratingMin: String = "",
    val limit: String = "",
)

/** Recognised content types — matches the server's library `type` enum
 *  minus the meta types (`mixed`, `dvr`) which don't carry items the
 *  smart-playlist evaluator can return.
 *
 *  The picker chips iterate this list, so order = display order. */
val SMART_PLAYLIST_TYPES: List<String> = listOf(
    "movie", "show", "episode", "anime", "music", "track", "book", "audiobook", "podcast",
)

object SmartPlaylistValidator {

    /** Result of validating a [SmartPlaylistRulesDraft]. Either an
     *  Error with a user-facing message OR an Ok with the server-
     *  shaped payload + the playlist name. The Compose layer disables
     *  the Save button on Error and shows the message inline. */
    sealed class Result {
        data class Ok(val name: String, val rules: SmartPlaylistRules) : Result()
        data class Error(val message: String) : Result()
    }

    /**
     * Validate a draft. Empty strings on numeric fields = "no
     * constraint" (parsed to null). Out-of-range or unparseable
     * numerics → Error. Empty `types` is allowed but will produce
     * an empty playlist server-side; we flag this as a soft warning
     * via a separate predicate ([isLikelyEmpty]) the UI can surface
     * without blocking save.
     */
    fun validate(draft: SmartPlaylistRulesDraft): Result {
        val name = draft.name.trim()
        if (name.isEmpty()) return Result.Error("Name is required")
        if (name.length > 100) return Result.Error("Name is too long (max 100 chars)")

        val yearMin = parseOptionalInt(draft.yearMin)
            ?: return Result.Error("Year (min) must be a whole number")
        val yearMax = parseOptionalInt(draft.yearMax)
            ?: return Result.Error("Year (max) must be a whole number")
        if (yearMin.value != null && (yearMin.value < 1800 || yearMin.value > 2100)) {
            return Result.Error("Year (min) is out of range")
        }
        if (yearMax.value != null && (yearMax.value < 1800 || yearMax.value > 2100)) {
            return Result.Error("Year (max) is out of range")
        }
        if (yearMin.value != null && yearMax.value != null && yearMin.value > yearMax.value) {
            return Result.Error("Year (min) must be ≤ year (max)")
        }

        val rating = parseOptionalDouble(draft.ratingMin)
            ?: return Result.Error("Rating (min) must be a number")
        if (rating.value != null && (rating.value < 0.0 || rating.value > 10.0)) {
            return Result.Error("Rating (min) must be between 0 and 10")
        }

        val limit = parseOptionalInt(draft.limit)
            ?: return Result.Error("Limit must be a whole number")
        if (limit.value != null && (limit.value < 1 || limit.value > 500)) {
            return Result.Error("Limit must be 1–500")
        }

        // Genres CSV → trimmed non-empty list. The web client uses the
        // same shape; users type "Action, Drama" and the server does an
        // OR-within match.
        val genres = draft.genresCsv
            .split(',')
            .map { it.trim() }
            .filter { it.isNotEmpty() }

        return Result.Ok(
            name = name,
            rules = SmartPlaylistRules(
                types = draft.types.toList(),
                genres = genres,
                year_min = yearMin.value,
                year_max = yearMax.value,
                rating_min = rating.value,
                limit = limit.value,
            ),
        )
    }

    /** True when the draft would resolve to an empty playlist. Server
     *  treats empty-types specially — it returns nothing rather than
     *  defaulting to "every item". The UI uses this to surface a
     *  pre-save hint. */
    fun isLikelyEmpty(draft: SmartPlaylistRulesDraft): Boolean = draft.types.isEmpty()

    // ── Parser helpers ────────────────────────────────────────────────────

    /** Parse an optional integer field. Returns:
     *   - Parsed(null) for empty / whitespace input ("no constraint"),
     *   - Parsed(n) for valid input,
     *   - null when the input is non-empty but unparseable.
     *
     *  Wrap the value so callers can distinguish "no constraint" from
     *  "invalid input" without overloading null. */
    internal data class Parsed<T>(val value: T?)

    private fun parseOptionalInt(s: String): Parsed<Int>? {
        val t = s.trim()
        if (t.isEmpty()) return Parsed(null)
        val v = t.toIntOrNull() ?: return null
        return Parsed(v)
    }

    private fun parseOptionalDouble(s: String): Parsed<Double>? {
        val t = s.trim()
        if (t.isEmpty()) return Parsed(null)
        val v = t.toDoubleOrNull() ?: return null
        return Parsed(v)
    }
}
