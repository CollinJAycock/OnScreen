package tv.onscreen.android.i18n

import android.app.LocaleManager
import android.content.Context
import android.content.res.Configuration
import android.content.res.Resources
import android.os.Build
import android.os.LocaleList
import android.os.SystemClock
import androidx.annotation.ChecksSdkIntAtLeast
import androidx.annotation.RequiresApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import tv.onscreen.android.R
import java.util.Locale

/**
 * Applies the app's interface language ([AppLanguage]) from minSdk 24 up.
 * MainActivity is a FragmentActivity, so AppCompat's per-app locale (which
 * backports through AppCompatActivity only) doesn't cover it; this is the
 * small part of it the app needs, with no new dependency.
 *
 *  - **API 33+:** the platform's per-app language (LocaleManager). Android
 *    keeps the choice, applies it to every context of the app (activities,
 *    the Application, the media service) and recreates the activity when it
 *    changes. res/xml/locales_config.xml lists the languages for the
 *    system's own per-app language setting.
 *  - **API 24-32** (Fire TV is Android 9 and 11): the choice is kept in
 *    SharedPreferences, read synchronously at attach time. MainActivity and
 *    the media service wrap their base context with it ([wrap], a
 *    createConfigurationContext that overrides only the locale), the
 *    Application's resources are updated in place ([applyToApplication],
 *    also after every configuration change, which resets them), and
 *    MainActivity recreates itself on a change ([changes]).
 *
 * "Device language" is no choice: an empty per-app locale list on 33+, no
 * stored value below.
 */
object AppLocale {
    private const val PREFS = "app_language"
    private const val KEY_CHOICE = "ui_language"

    /** How long a recreate after a Settings pick counts as that pick's. */
    private const val KEEP_SCREEN_MS = 10_000L

    @get:ChecksSdkIntAtLeast(api = 33)
    private val platform: Boolean get() = Build.VERSION.SDK_INT >= 33

    private val _changes = MutableStateFlow(0)

    /**
     * Bumped each time the language is changed below API 33, where the
     * platform doesn't recreate the screen itself: MainActivity recreates
     * when the language it shows is no longer [current].
     */
    val changes: StateFlow<Int> = _changes

    @Volatile
    private var keepScreenUntil = 0L

    /**
     * Startup (Application.onCreate). Below API 33, applies a stored choice
     * to the Application's resources. On 33+, moves a choice stored before
     * the device updated to Android 13 into the platform's setting.
     */
    fun install(app: Context) {
        val stored = storedChoice(app)
        if (platform) {
            if (stored != null) {
                if (platformChoice(app) == null) setPlatformChoice(app, stored)
                prefs(app).edit().remove(KEY_CHOICE).apply()
            }
            return
        }
        if (stored != null) applyToApplication(app)
    }

    /** Application.onConfigurationChanged: a configuration change puts the
     *  Application's resources back on the device's language below API 33. */
    fun onConfigurationChanged(app: Context) {
        if (!platform && storedChoice(app) != null) applyToApplication(app)
    }

    /** This device's choice, or null to follow the device's language. */
    fun choice(context: Context): String? =
        if (platform) platformChoice(context) else storedChoice(context)

    /**
     * The language the app's strings are in right now: "en", "es" or
     * "zh-Hans". Read from the strings themselves (R.string.app_language_tag
     * differs per language folder), so it is what the screen shows however
     * Android matched the device's languages to the app's.
     */
    fun current(context: Context): String =
        AppLanguage.supported(context.getString(R.string.app_language_tag)) ?: AppLanguage.ENGLISH

    /**
     * Switch to [tag] (null = the device's language) and keep it on this
     * device. [keepScreen]: a pick in Settings, whose screen stays up across
     * the recreate it causes (MainActivity otherwise resets a restored
     * activity to Home).
     */
    fun setChoice(context: Context, tag: String?, keepScreen: Boolean = false) {
        val next = AppLanguage.supported(tag)
        if (next == choice(context)) return
        if (keepScreen) keepScreenUntil = SystemClock.elapsedRealtime() + KEEP_SCREEN_MS
        if (platform) {
            setPlatformChoice(context, next)
            return
        }
        // commit, not apply: the recreated activity reads it back at once.
        @android.annotation.SuppressLint("ApplySharedPref")
        val stored = prefs(context).edit().apply {
            if (next == null) remove(KEY_CHOICE) else putString(KEY_CHOICE, next)
        }.commit()
        if (!stored) android.util.Log.w("AppLocale", "couldn't store the language choice")
        applyToApplication(context.applicationContext)
        _changes.value = _changes.value + 1
    }

    /** True once, when the activity being recreated is the one a Settings
     *  pick just changed the language of. */
    fun consumeKeepScreen(): Boolean {
        val keep = SystemClock.elapsedRealtime() < keepScreenUntil
        keepScreenUntil = 0L
        return keep
    }

    /** [base] with this device's choice applied, below API 33 (attachBaseContext
     *  of MainActivity and the media service). Unchanged on 33+ or with no
     *  choice. Overrides the locale only: the rest of the configuration (size,
     *  uiMode, which the activity handles itself) keeps following the device. */
    fun wrap(base: Context): Context {
        if (platform) return base
        val tag = storedChoice(base) ?: return base
        val locales = LocaleList(Locale.forLanguageTag(tag))
        LocaleList.setDefault(locales)
        val override = Configuration().apply { setLocales(locales) }
        return base.createConfigurationContext(override)
    }

    private fun prefs(context: Context) =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    private fun storedChoice(context: Context): String? =
        AppLanguage.supported(prefs(context).getString(KEY_CHOICE, null))

    @RequiresApi(33)
    private fun platformChoice(context: Context): String? {
        val list = context.getSystemService(LocaleManager::class.java)?.applicationLocales ?: return null
        if (list.isEmpty) return null
        return AppLanguage.match(listOf(list[0].toLanguageTag()))
    }

    @RequiresApi(33)
    private fun setPlatformChoice(context: Context, tag: String?) {
        context.getSystemService(LocaleManager::class.java)?.applicationLocales =
            if (tag == null) LocaleList.getEmptyLocaleList() else LocaleList.forLanguageTags(tag)
    }

    /** Below API 33: the Application's resources (and the process default
     *  locale, which number formats and CJK glyph choice follow) in the
     *  stored choice, or back on the device's languages. */
    @Suppress("DEPRECATION")
    private fun applyToApplication(app: Context) {
        val tag = storedChoice(app)
        val locales = if (tag != null) LocaleList(Locale.forLanguageTag(tag)) else Resources.getSystem().configuration.locales
        LocaleList.setDefault(locales)
        val res = app.resources
        val config = Configuration(res.configuration).apply { setLocales(locales) }
        res.updateConfiguration(config, res.displayMetrics)
    }
}
