package tv.onscreen.mobile.i18n

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * The string resources, checked as files: every translatable key of values/
 * exists in values-es/ and values-b+zh+Hans/, in the same file, with the
 * same placeholders, and each plural has the forms its language uses. The
 * apps' catalog tests check the same for the web and TV catalogs.
 */
class StringResourcesTest {

    private class Entry(val file: String, val translatable: Boolean, val forms: Map<String, String>)

    private val res: File = listOf(File("src/main/res"), File("app/src/main/res")).first { it.isDirectory }

    private val folders = mapOf(
        AppLanguage.ENGLISH to "values",
        AppLanguage.SPANISH to "values-es",
        AppLanguage.CHINESE_SIMPLIFIED to "values-b+zh+Hans",
    )

    /** CLDR plural forms Android uses for each language. */
    private val pluralForms = mapOf(
        AppLanguage.ENGLISH to setOf("one", "other"),
        AppLanguage.SPANISH to setOf("one", "many", "other"),
        AppLanguage.CHINESE_SIMPLIFIED to setOf("other"),
    )

    // A string is "" in the forms map; a plural has one entry per quantity.
    private fun load(folder: String): Map<String, Entry> {
        val out = linkedMapOf<String, Entry>()
        val factory = DocumentBuilderFactory.newInstance()
        val files = File(res, folder).listFiles { f -> f.name.endsWith(".xml") }.orEmpty().sortedBy { it.name }
        for (f in files) {
            val root = factory.newDocumentBuilder().parse(f).documentElement
            val children = root.childNodes
            for (i in 0 until children.length) {
                val el = children.item(i) as? Element ?: continue
                if (el.tagName != "string" && el.tagName != "plurals") continue
                val name = el.getAttribute("name")
                about("$folder: $name is defined twice").that(out).doesNotContainKey(name)
                val forms = if (el.tagName == "string") {
                    mapOf("" to el.textContent)
                } else {
                    val items = el.getElementsByTagName("item")
                    (0 until items.length).associate {
                        val item = items.item(it) as Element
                        item.getAttribute("quantity") to item.textContent
                    }
                }
                out[name] = Entry(f.name, el.getAttribute("translatable") != "false", forms)
            }
        }
        return out
    }

    private val placeholder = Regex("""%(?:(\d+)\$)?[-#+ 0,(]*\d*(?:\.\d+)?([sdfxXcegob%])""")

    private fun placeholders(text: String): List<String> =
        placeholder.findAll(text)
            .filter { it.groupValues[2] != "%" }
            .map { "${it.groupValues[1]}$${it.groupValues[2]}" }
            .sorted()
            .toList()

    private val english by lazy { load("values") }

    // The message is not a format string: resource text holds "%1$s".
    private fun about(message: String) = assertWithMessage("%s", message)

    @Test
    fun `every translatable key is in every language, in the same file, with the same placeholders`() {
        for ((lang, folder) in folders - AppLanguage.ENGLISH) {
            val translated = load(folder)
            for ((name, en) in english) {
                if (!en.translatable) {
                    about("$folder: $name is not translatable").that(translated).doesNotContainKey(name)
                    continue
                }
                val tr = translated[name]
                about("$folder: missing $name (values/${en.file})").that(tr).isNotNull()
                tr!!
                about("$folder: $name belongs in ${en.file}").that(tr.file).isEqualTo(en.file)
                val want = placeholders(en.forms["other"] ?: en.forms.getValue(""))
                for ((quantity, text) in tr.forms) {
                    about("$folder: placeholders of $name${if (quantity.isEmpty()) "" else "[$quantity]"}")
                        .that(placeholders(text)).isEqualTo(want)
                }
                if (en.forms.keys != setOf("")) {
                    about("$folder: plural forms of $name")
                        .that(tr.forms.keys).isEqualTo(pluralForms.getValue(lang))
                }
            }
            for (name in translated.keys) {
                about("$folder: $name isn't in values/").that(english).containsKey(name)
            }
        }
    }

    @Test
    fun `English plurals have one and other, and every form keeps the placeholders`() {
        for ((name, en) in english) {
            if (en.forms.keys == setOf("")) continue
            about("plural forms of $name").that(en.forms.keys).isEqualTo(pluralForms.getValue("en"))
            val want = placeholders(en.forms.getValue("other"))
            for ((quantity, text) in en.forms) {
                about("placeholders of $name[$quantity]").that(placeholders(text)).isEqualTo(want)
            }
        }
    }

    @Test
    fun `each folder names its own language, which is what the app sends as Accept-Language`() {
        for ((lang, folder) in folders) {
            about(folder).that(load(folder)["app_language_tag"]?.forms?.get("")).isEqualTo(lang)
        }
        val config = File(res, "xml/locales_config.xml").readText()
        val tags = Regex("""android:name="([^"]+)"""").findAll(config).map { it.groupValues[1] }.toList()
        assertThat(tags).containsExactlyElementsIn(AppLanguage.TAGS).inOrder()
    }

    @Test
    fun `Chinese uses full-width punctuation after Chinese text`() {
        val cjk = Regex("""[㐀-鿿][,.:;?!]""")
        for ((name, entry) in load(folders.getValue(AppLanguage.CHINESE_SIMPLIFIED))) {
            for (text in entry.forms.values) {
                about("zh-Hans $name: \"$text\"").that(cjk.containsMatchIn(text)).isFalse()
            }
        }
    }
}
