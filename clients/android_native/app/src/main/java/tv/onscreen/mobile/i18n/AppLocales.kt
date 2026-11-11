package tv.onscreen.mobile.i18n

import android.app.LocaleManager
import android.content.Context
import android.content.res.Configuration
import android.content.res.Resources
import android.os.Build
import android.os.LocaleList
import androidx.annotation.RequiresApi
import androidx.core.content.edit
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import tv.onscreen.mobile.R
import java.util.Locale

/**
 * The app's language on the platform, from minSdk 24.
 *
 * - **Android 13+ (API 33):** the platform's per-app language
 *   ([LocaleManager]). The system keeps the choice, applies it to every
 *   context of the process (activities, the Application, services, workers),
 *   recreates the activity when it changes, and lists the app under
 *   Settings › Apps › Language (res/xml/locales_config.xml).
 * - **Android 7–12:** a private preference. [wrap] gives MainActivity a
 *   configuration context in that language (attachBaseContext), and
 *   [localized] does the same for text built from the Application or a
 *   service (notifications, the media session, Android Auto). A change
 *   recreates MainActivity ([storedChoice] is what it watches), and the
 *   process default locale is set to match, as Android 13 does.
 *
 * MainActivity is a FragmentActivity, not an AppCompatActivity, so
 * AppCompat's per-app language would cover Android 13+ only.
 */
object AppLocales {

    private const val PREFS = "onscreen_language"
    private const val KEY = "ui_language"

    @Volatile private var controller: LanguageController? = null

    /** The process-wide controller; Hilt provides the same instance. */
    fun controller(context: Context): LanguageController =
        controller ?: synchronized(this) {
            controller ?: LanguageController(PlatformStore(context.applicationContext)).also { controller = it }
        }

    private val stored = MutableStateFlow<String?>(null)

    /** Android 12 and older: the stored choice. MainActivity recreates when
     *  it no longer matches the one it was created with. */
    val storedChoice: StateFlow<String?> = stored.asStateFlow()

    /** True where the platform keeps and applies the language itself. */
    val platformManaged: Boolean
        get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU

    /** Application.onCreate: Android 12 and older, apply the stored choice to
     *  the process default locale (number and date formats). */
    fun init(context: Context) {
        if (platformManaged) return
        val tag = readStored(context) ?: return
        stored.value = tag
        applyDefault(tag)
    }

    /** MainActivity.attachBaseContext: the activity in the chosen language
     *  (Android 12 and older; Android 13+ needs nothing). */
    fun wrap(base: Context): Context {
        if (platformManaged) return base
        val tag = readStored(base) ?: return base
        return base.createConfigurationContext(overrideFor(tag))
    }

    @Volatile private var localizedCache: Pair<String, Context>? = null

    /**
     * A context whose resources are in the app's language, for text built
     * outside an activity: notifications, the media session, Android Auto,
     * workers. On Android 13+, or with no choice, [context] itself.
     */
    fun localized(context: Context): Context {
        if (platformManaged) return context
        val tag = stored.value ?: readStored(context) ?: return context
        localizedCache?.let { (cachedTag, ctx) -> if (cachedTag == tag) return ctx }
        val ctx = context.applicationContext.createConfigurationContext(overrideFor(tag))
        localizedCache = tag to ctx
        return ctx
    }

    /** The language the app shows, as "en" / "es" / "zh-Hans": what Android
     *  resolved for this app's resources, so it always matches the screen.
     *  Sent as Accept-Language on every API request. */
    fun currentTag(context: Context): String =
        localized(context).getString(R.string.app_language_tag)

    // A delta configuration: only the locale is set, so the context keeps
    // following the device's orientation, size, night mode and font scale.
    private fun overrideFor(tag: String): Configuration =
        Configuration().apply { setLocales(LocaleList(Locale.forLanguageTag(tag))) }

    private fun readStored(context: Context): String? =
        context.applicationContext
            .getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .getString(KEY, null)
            ?.takeIf(AppLanguage::isSupported)

    // LocaleList.setDefault also sets Locale.getDefault() to its first entry.
    private fun applyDefault(tag: String?) {
        LocaleList.setDefault(
            if (tag == null) Resources.getSystem().configuration.locales
            else LocaleList(Locale.forLanguageTag(tag)),
        )
    }

    private class PlatformStore(private val app: Context) : LanguageController.Store {
        override fun get(): String? =
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) Api33.get(app) else readStored(app)

        override fun set(tag: String?) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                Api33.set(app, tag)
                return
            }
            // apply(): the in-memory value is what the recreated activity
            // reads (wrap), and it changes at once.
            app.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit {
                if (tag == null) remove(KEY) else putString(KEY, tag)
            }
            localizedCache = null
            applyDefault(tag)
            stored.value = tag
        }
    }

    @RequiresApi(Build.VERSION_CODES.TIRAMISU)
    private object Api33 {
        fun get(context: Context): String? {
            val list = context.getSystemService(LocaleManager::class.java)?.applicationLocales
            return if (list == null || list.isEmpty) null else AppLanguage.match(list[0])
        }

        fun set(context: Context, tag: String?) {
            context.getSystemService(LocaleManager::class.java)?.applicationLocales =
                if (tag == null) LocaleList.getEmptyLocaleList() else LocaleList.forLanguageTags(tag)
        }
    }
}
