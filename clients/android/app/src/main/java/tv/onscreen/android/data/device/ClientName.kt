package tv.onscreen.android.data.device

import android.os.Build
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Stable, human-readable label for this device in the cross-device
 * picker (`/api/v1/playback/devices`) and the watch_events.client_name
 * column. Sent as the `client_name` field on every progress post so
 * the server's device-list query (last-seen-per-name) picks the TV
 * up the next time another of the user's devices wants to "play on
 * Living Room TV".
 *
 * Server caps the persisted value at 64 chars; we stay well under
 * that so an unusually long `Build.MODEL` doesn't hit the truncation
 * path (the server takes the first 64 silently — fine, but produces
 * less recognisable labels).
 *
 * Heuristics for the platform prefix:
 *   - Fire TV: Build.MODEL has the "AFT…" Amazon SoC prefix
 *   - Google TV: Build.MANUFACTURER is "Google" + a TV form factor
 *   - Everything else: "Android TV"
 *
 * The actual model string ("Pixel 8", "Bravia XR", "AFTKA") is
 * appended so a household with multiple TVs can tell them apart.
 * A future Settings preference will let the operator override this
 * with a custom name (e.g. "Living Room TV") — same shape Plex uses.
 */
@Singleton
class ClientName @Inject constructor(
    // Nullable so tests can construct ClientName(null) without a Context;
    // Hilt injects the (non-null) application context. No default value —
    // Kotlin defaults generate a second constructor, which Dagger rejects.
    @dagger.hilt.android.qualifiers.ApplicationContext
    private val context: android.content.Context?,
) {

    /** Computed once at construction. Build.* is set when zygote
     *  forks the process, so this never changes for a given install. */
    val value: String = compute()

    private fun compute(): String {
        val model = Build.MODEL?.trim().orEmpty()
        val manufacturer = Build.MANUFACTURER?.trim().orEmpty()

        val platform = when {
            // Amazon Fire TV models all start with "AFT" (AFTKA, AFTMM,
            // AFTKMST12, etc.). Manufacturer is "Amazon".
            manufacturer.equals("Amazon", ignoreCase = true) ||
                model.startsWith("AFT") -> "Fire TV"
            else -> "Android TV"
        }

        // Some manufacturers report MODEL with the manufacturer baked in
        // ("Sony Bravia XR-65A95K"). Avoid double-printing in that case.
        val modelLooksRedundant = manufacturer.isNotEmpty() &&
            model.contains(manufacturer, ignoreCase = true)

        val suffix = when {
            model.isBlank() -> ""
            modelLooksRedundant -> " — $model"
            manufacturer.isBlank() -> " — $model"
            else -> " — $manufacturer $model"
        }

        // Per-install disambiguator: two identical sticks (same
        // manufacturer + model — common: one per TV) used to produce the
        // SAME client_name, so cross-device "play here" fired on both and
        // the device picker listed only one. ANDROID_ID is stable per
        // install and differs across devices.
        val installTag = context?.let {
            try {
                android.provider.Settings.Secure.getString(
                    it.contentResolver, android.provider.Settings.Secure.ANDROID_ID,
                )?.takeLast(4)
            } catch (_: Exception) {
                null
            }
        }
        val tag = if (installTag.isNullOrBlank()) "" else " #$installTag"

        // Cap defensively at 60 chars (server caps at 64). The install tag
        // survives the cap — it's the part that disambiguates.
        val combined = ((platform + suffix).take(60 - tag.length)) + tag
        return combined.ifBlank { platform }
    }
}
