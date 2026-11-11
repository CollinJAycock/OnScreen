package tv.onscreen.mobile.ui.player

import org.w3c.dom.Element
import tv.onscreen.mobile.R
import tv.onscreen.mobile.i18n.UiText
import java.io.File
import java.util.Locale
import javax.xml.parsers.DocumentBuilderFactory

/**
 * The app's English text for unit tests, read from src/main/res/values (a JVM
 * unit test has no Android resources): a test resolves the [UiText] or the
 * string id the code produced and asserts the exact English the app shows,
 * so moving a literal into resources is checked to leave it byte-identical.
 */
object EnglishText {

    private val strings = HashMap<String, String>()
    private val plurals = HashMap<String, Map<String, String>>()

    init {
        val dir = File("src/main/res/values")
        val factory = DocumentBuilderFactory.newInstance()
        dir.listFiles { f -> f.name.endsWith(".xml") }.orEmpty().forEach { file ->
            val root = factory.newDocumentBuilder().parse(file).documentElement
            val nodes = root.childNodes
            for (i in 0 until nodes.length) {
                val el = nodes.item(i) as? Element ?: continue
                val name = el.getAttribute("name")
                when (el.tagName) {
                    "string" -> strings[name] = unescape(el.textContent)
                    "plurals" -> {
                        val items = el.getElementsByTagName("item")
                        plurals[name] = (0 until items.length).associate {
                            val item = items.item(it) as Element
                            item.getAttribute("quantity") to unescape(item.textContent)
                        }
                    }
                }
            }
        }
    }

    private val stringNames: Map<Int, String> by lazy { idsOf(R.string::class.java) }

    // By name: R has no plurals class while the app defines none.
    private val pluralNames: Map<Int, String> by lazy {
        runCatching { Class.forName("tv.onscreen.mobile.R\$plurals") }.getOrNull()?.let(::idsOf).orEmpty()
    }

    private fun idsOf(cls: Class<*>): Map<Int, String> =
        cls.fields.associate { it.getInt(null) to it.name }

    fun string(id: Int, vararg args: Any): String {
        val name = stringNames[id] ?: error("no string resource $id")
        val text = strings[name] ?: error("string $name is not in values/")
        return if (args.isEmpty()) text else String.format(Locale.US, text, *args)
    }

    fun plural(id: Int, count: Int, vararg args: Any): String {
        val name = pluralNames[id] ?: error("no plurals resource $id")
        val forms = plurals[name] ?: error("plurals $name is not in values/")
        val text = forms[if (count == 1) "one" else "other"] ?: forms.getValue("other")
        return String.format(Locale.US, text, *args)
    }

    fun of(text: UiText): String = when (text) {
        is UiText.Res -> string(text.id, *resolve(text.args))
        is UiText.Plural -> plural(text.id, text.count, *resolve(text.args))
        is UiText.Raw -> text.text
    }

    /** [SubtitleTracks.label]'s words, in English. */
    val words = SubtitleTracks.Words { id, args -> string(id, *args) }

    private fun resolve(args: List<Any>): Array<Any> =
        args.map { if (it is UiText) of(it) else it }.toTypedArray()

    // The escapes aapt undoes: \' \" \\ \n \t and \\uXXXX.
    private fun unescape(raw: String): String {
        val out = StringBuilder()
        var i = 0
        while (i < raw.length) {
            val c = raw[i]
            if (c == '\\' && i + 1 < raw.length) {
                val n = raw[i + 1]
                when (n) {
                    'n' -> out.append('\n')
                    't' -> out.append('\t')
                    'u' -> {
                        out.append(raw.substring(i + 2, i + 6).toInt(16).toChar())
                        i += 4
                    }
                    else -> out.append(n)
                }
                i += 2
                continue
            }
            out.append(c)
            i++
        }
        return out.toString()
    }
}

/** This text in English, as the app shows it; null stays null. */
fun UiText?.en(): String? = this?.let(EnglishText::of)
