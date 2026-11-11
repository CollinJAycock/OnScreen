package tv.onscreen.android.i18n

import org.w3c.dom.Element
import tv.onscreen.android.R
import java.io.File
import java.util.Locale
import javax.xml.parsers.DocumentBuilderFactory

/**
 * The app's string resources read straight from res/values*, for JVM tests:
 * resolves [UiText] the way Resources does (String.format with the
 * language's locale, the CLDR plural form), so a test can check the real
 * English, Spanish or Chinese text without a device. The unit tests run
 * with the module directory (app/) as the working directory.
 */
object TestStrings {

    /** The language folders: tag → res folder. */
    val FOLDERS = linkedMapOf(
        AppLanguage.ENGLISH to "values",
        AppLanguage.SPANISH to "values-es",
        AppLanguage.CHINESE to "values-b+zh+Hans",
    )

    class Catalog(
        /** name → text, for every <string>. */
        val strings: Map<String, String>,
        /** name → (quantity → text), for every <plurals>. */
        val plurals: Map<String, Map<String, String>>,
        /** The names marked translatable="false". */
        val untranslatable: Set<String>,
        /** The files the catalog was read from, by name. */
        val files: List<String>,
    )

    val res: File by lazy {
        listOf(File("src/main/res"), File("app/src/main/res")).firstOrNull { it.isDirectory }
            ?: error("res/ not found from ${File(".").absolutePath}")
    }

    private val catalogs = HashMap<String, Catalog>()

    fun catalog(tag: String): Catalog = catalogs.getOrPut(tag) { load(FOLDERS.getValue(tag)) }

    private fun load(folder: String): Catalog {
        val dir = File(res, folder)
        val strings = LinkedHashMap<String, String>()
        val plurals = LinkedHashMap<String, Map<String, String>>()
        val untranslatable = HashSet<String>()
        val files = dir.listFiles { f -> f.name.startsWith("strings") && f.name.endsWith(".xml") }
            .orEmpty().sortedBy { it.name }
        val builder = DocumentBuilderFactory.newInstance().newDocumentBuilder()
        for (file in files) {
            val root = builder.parse(file).documentElement
            val children = root.childNodes
            for (i in 0 until children.length) {
                val el = children.item(i) as? Element ?: continue
                val name = el.getAttribute("name")
                if (el.getAttribute("translatable") == "false") untranslatable += name
                when (el.tagName) {
                    "string" -> strings[name] = androidText(el.textContent)
                    "plurals" -> {
                        val items = LinkedHashMap<String, String>()
                        val kids = el.getElementsByTagName("item")
                        for (j in 0 until kids.length) {
                            val item = kids.item(j) as Element
                            items[item.getAttribute("quantity")] = androidText(item.textContent)
                        }
                        plurals[name] = items
                    }
                }
            }
        }
        return Catalog(strings, plurals, untranslatable, files.map { it.name })
    }

    /** Resource text as Resources returns it: escapes undone, whitespace
     *  runs collapsed (the strings here are never quoted). */
    private fun androidText(raw: String): String {
        val collapsed = raw.trim().replace(Regex("\\s+"), " ")
        val out = StringBuilder()
        var i = 0
        while (i < collapsed.length) {
            val c = collapsed[i]
            if (c == '\\' && i + 1 < collapsed.length) {
                when (val n = collapsed[i + 1]) {
                    'n' -> out.append('\n')
                    't' -> out.append('\t')
                    'u' -> {
                        out.append(collapsed.substring(i + 2, i + 6).toInt(16).toChar())
                        i += 4
                    }
                    else -> out.append(n)
                }
                i += 2
            } else {
                out.append(c)
                i++
            }
        }
        return out.toString()
    }

    /** Resource id → name, from the generated R classes. */
    private val names: Map<Int, String> by lazy {
        (R.string::class.java.fields + R.plurals::class.java.fields)
            .associate { it.getInt(null) to it.name }
    }

    fun nameOf(id: Int): String = names[id] ?: error("no resource name for id $id")

    /** The CLDR plural category of [n] in [tag] (integers). */
    fun pluralCategory(tag: String, n: Int): String = when (tag) {
        AppLanguage.CHINESE -> "other"
        AppLanguage.SPANISH -> when {
            n == 1 -> "one"
            n != 0 && n % 1_000_000 == 0 -> "many"
            else -> "other"
        }
        else -> if (n == 1) "one" else "other"
    }

    /** The text of string [id] in [tag], unformatted (falls back to English
     *  as Android does). */
    fun raw(id: Int, tag: String = AppLanguage.ENGLISH): String {
        val name = nameOf(id)
        return catalog(tag).strings[name] ?: catalog(AppLanguage.ENGLISH).strings.getValue(name)
    }

    /** [text] as the app shows it in [tag]. */
    fun resolve(text: UiText, tag: String = AppLanguage.ENGLISH): String {
        val locale = Locale.forLanguageTag(tag)
        return text.resolveWith(
            string = { id, args ->
                val fmt = raw(id, tag)
                if (args.isEmpty()) fmt else String.format(locale, fmt, *args)
            },
            plural = { id, count, args ->
                val name = nameOf(id)
                val forms = catalog(tag).plurals[name] ?: catalog(AppLanguage.ENGLISH).plurals.getValue(name)
                val fmt = forms[pluralCategory(tag, count)] ?: forms.getValue("other")
                String.format(locale, fmt, *args)
            },
        )
    }

    /** String [id] with [args] in [tag], as getString(id, args) gives it. */
    fun string(id: Int, vararg args: Any, tag: String = AppLanguage.ENGLISH): String =
        resolve(UiText.Res(id, args.toList()), tag)
}
