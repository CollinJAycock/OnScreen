package tv.onscreen.android.i18n

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test
import java.io.File

/**
 * The translations against English (res/values): every translatable string
 * and plural is in values-es and values-b+zh+Hans with the same
 * placeholders, nothing else is, and the per-language marker and the
 * locale config agree with [AppLanguage]. Android lint's MissingTranslation
 * checks presence too; this also checks the placeholders, which lint only
 * warns about, and runs with the unit tests.
 */
class StringResourcesTest {

    private val en = TestStrings.catalog(AppLanguage.ENGLISH)
    private val translations = listOf(AppLanguage.SPANISH, AppLanguage.CHINESE)

    /** `%1$s`, `%2$d`, `%d`, `%s` — `%%` is a literal percent, not one. */
    private fun placeholders(text: String): List<String> =
        Regex("""%(?:(\d+)\$)?([sd])""").findAll(text.replace("%%", ""))
            .mapIndexed { i, m -> "${m.groupValues[1].ifEmpty { (i + 1).toString() }}${m.groupValues[2]}" }
            .sorted()
            .toList()

    @Test
    fun `every translation has the same files as English`() {
        for (tag in translations) {
            assertWithMessage("%s", tag).that(TestStrings.catalog(tag).files).containsExactlyElementsIn(en.files)
        }
    }

    @Test
    fun `every translatable string is translated with the same placeholders`() {
        for (tag in translations) {
            val tr = TestStrings.catalog(tag)
            for ((name, text) in en.strings) {
                if (name in en.untranslatable) continue
                val translated = tr.strings[name]
                assertWithMessage("%s", "$tag is missing $name").that(translated).isNotNull()
                assertWithMessage("%s", "$tag/$name placeholders")
                    .that(placeholders(translated!!)).isEqualTo(placeholders(text))
                assertWithMessage("%s", "$tag/$name is empty").that(translated.isNotBlank()).isTrue()
            }
        }
    }

    @Test
    fun `every plural is translated with an other form and the same placeholders`() {
        for (tag in translations) {
            val tr = TestStrings.catalog(tag)
            for ((name, forms) in en.plurals) {
                val translated = tr.plurals[name]
                assertWithMessage("%s", "$tag is missing plurals $name").that(translated).isNotNull()
                assertWithMessage("%s", "$tag/$name needs other").that(translated!!).containsKey("other")
                val expected = placeholders(forms.getValue("other"))
                for ((quantity, text) in translated) {
                    assertWithMessage("%s", "$tag/$name[$quantity] placeholders").that(placeholders(text)).isEqualTo(expected)
                }
            }
            // Chinese has one form; Spanish one, many and other (CLDR).
            val quantities = if (tag == AppLanguage.CHINESE) setOf("other") else setOf("one", "many", "other")
            for ((name, forms) in tr.plurals) {
                assertWithMessage("%s", "$tag/$name forms").that(forms.keys).containsExactlyElementsIn(quantities)
            }
        }
        for ((name, forms) in en.plurals) {
            assertWithMessage("%s", "en/$name forms").that(forms.keys).containsExactly("one", "other")
        }
    }

    @Test
    fun `translations hold no key English lacks, and nothing marked untranslatable`() {
        for (tag in translations) {
            val tr = TestStrings.catalog(tag)
            assertWithMessage("%s", "$tag strings").that(en.strings.keys).containsAtLeastElementsIn(tr.strings.keys)
            assertWithMessage("%s", "$tag plurals").that(en.plurals.keys).containsAtLeastElementsIn(tr.plurals.keys)
            for (name in en.untranslatable) {
                assertWithMessage("%s", "$tag translates $name").that(tr.strings).doesNotContainKey(name)
            }
        }
    }

    @Test
    fun `names, services and formats are marked untranslatable`() {
        assertThat(en.untranslatable).containsAtLeast("app_name", "server_url_hint", "dolby_vision", "listenbrainz")
        assertThat(en.strings["app_name"]).isEqualTo("OnScreen")
    }

    @Test
    fun `each folder names its own language`() {
        for ((tag, _) in TestStrings.FOLDERS) {
            assertThat(TestStrings.catalog(tag).strings["app_language_tag"]).isEqualTo(tag)
        }
        assertThat(TestStrings.FOLDERS.keys.toList()).isEqualTo(AppLanguage.SUPPORTED)
    }

    @Test
    fun `the locale config lists the shipped languages`() {
        val xml = File(TestStrings.res, "xml/locales_config.xml").readText()
        val listed = Regex("""android:name="([^"]+)"""").findAll(xml).map { it.groupValues[1] }.toList()
        assertThat(listed).isEqualTo(AppLanguage.SUPPORTED)
    }

    @Test
    fun `Chinese uses full-width punctuation`() {
        val zh = TestStrings.catalog(AppLanguage.CHINESE)
        val cjk = "\\p{IsHan}"
        // A comma, colon, full stop, question or exclamation mark, or a
        // parenthesis right after a Chinese character should be full-width.
        val halfWidth = Regex("$cjk[,:;?!()]|$cjk\\.(?!\\.)|[\"']")
        val texts = zh.strings + zh.plurals.flatMap { (n, f) -> f.map { (q, t) -> "$n[$q]" to t } }
        for ((name, text) in texts) {
            assertWithMessage("%s", "zh/$name: $text").that(halfWidth.containsMatchIn(text)).isFalse()
        }
    }

    @Test
    fun `Chinese keeps a space between Chinese and Latin letters or digits`() {
        val zh = TestStrings.catalog(AppLanguage.CHINESE)
        val joined = Regex("\\p{IsHan}[A-Za-z0-9]|[A-Za-z0-9]\\p{IsHan}")
        val texts = zh.strings + zh.plurals.flatMap { (n, f) -> f.map { (q, t) -> "$n[$q]" to t } }
        for ((name, text) in texts) {
            assertWithMessage("%s", "zh/$name: $text").that(joined.containsMatchIn(text)).isFalse()
        }
    }
}
