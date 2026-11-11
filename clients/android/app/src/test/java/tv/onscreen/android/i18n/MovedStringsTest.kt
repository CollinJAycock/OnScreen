package tv.onscreen.android.i18n

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test
import tv.onscreen.android.R

/**
 * The English text that used to be hardcoded in Kotlin, now string
 * resources: each resolves (with its placeholders filled) to exactly what
 * the code wrote before, and the plurals and placeholders work in Spanish
 * and Chinese.
 */
class MovedStringsTest {

    private fun en(id: Int, vararg args: Any) = TestStrings.string(id, *args)
    private fun es(id: Int, vararg args: Any) = TestStrings.string(id, *args, tag = AppLanguage.SPANISH)
    private fun zh(id: Int, vararg args: Any) = TestStrings.string(id, *args, tag = AppLanguage.CHINESE)
    private fun plural(id: Int, n: Int, tag: String) = TestStrings.resolve(UiText.Plural(id, n), tag)

    @Test
    fun `the English is what the code used to write`() {
        val sec = 7
        val code = 503
        val cases = listOf(
            // Player
            en(R.string.up_next_countdown, sec) to "UP NEXT · ${sec}s",
            en(R.string.up_next_caps) to "UP NEXT",
            en(R.string.track_n, 3) to "Track 3",
            en(R.string.audio_channels, 6) to "6ch",
            en(R.string.playback_error_detail, "ERROR_CODE_IO", "boom", "\nhttp://srv/x") to
                "Playback error ERROR_CODE_IO: boom\nhttp://srv/x",
            en(R.string.playback_error) to "Playback error",
            en(R.string.no_playable_file) to "No playable file",
            en(R.string.playback_failed) to "Playback failed",
            en(R.string.dolby_vision) to "Dolby Vision",
            en(R.string.dolby_vision_unsupported) to "Dolby Vision is not supported on this device.",
            en(R.string.file_damaged_title) to "This file appears to be damaged",
            en(R.string.file_damaged_message) to
                "The integrity check could not decode parts of this file, so playback would " +
                "stall or show broken video. Replace the file with a good copy and re-scan the library.",
            en(R.string.too_many_streams_default) to
                "Your account is already playing as many streams as it can. Stop one on another device to watch here.",
            en(R.string.subtitle_external) to "External",
            // Library
            en(R.string.sort_title_asc) to "Title (A–Z)",
            en(R.string.sort_title_desc) to "Title (Z–A)",
            en(R.string.sort_recently_added) to "Recently added",
            en(R.string.sort_newest_year) to "Newest year",
            en(R.string.sort_oldest_year) to "Oldest year",
            en(R.string.sort_highest_rated) to "Highest rated",
            en(R.string.sort) to "Sort",
            en(R.string.sort_by_title) to "Sort by",
            en(R.string.filter_by_genre_title) to "Filter by genre",
            en(R.string.all_genres) to "All genres",
            en(R.string.error_failed_to_load) to "Failed to load",
            en(R.string.error_load_failed) to "load failed",
            en(R.string.error_failed_to_load_channels) to "Failed to load channels",
            en(R.string.error_failed_to_load_recordings) to "Failed to load recordings",
            // Search
            en(R.string.search_in_scope, "All libraries") to "Search in: All libraries",
            en(R.string.search_error_sign_in) to "Sign in again to search your library",
            en(R.string.search_error_http, code) to "Couldn't search your library (HTTP $code)",
            en(R.string.search_error_offline) to "Couldn't reach the server to search your library",
            // Detail
            en(R.string.play_button_description, "Resume S1 · E2", "Pilot") to "Resume S1 · E2, Pilot",
            en(R.string.music_videos) to "Music Videos",
            en(R.string.appears_on) to "Appears On",
            en(R.string.similar_artists) to "Similar Artists",
            en(R.string.version_n, 2) to "Version 2",
            // Sign-in
            en(R.string.login_enter_credentials) to "Enter a username and password",
            en(R.string.totp_enter_code) to "Enter your code",
            // Settings
            en(R.string.account_unknown_user) to "unknown",
            en(R.string.error_save_failed) to "save failed",
            en(R.string.error_save_failed_http, code) to "Couldn't save (HTTP $code)",
            en(R.string.quality_not_allowed) to "That quality setting isn't allowed",
            en(R.string.lang_system_default) to "System default",
            en(R.string.lang_chinese) to "Chinese",
            en(R.string.scrobble_status_not_linked) to "ListenBrainz · Not linked",
            en(R.string.scrobble_status_linked) to "ListenBrainz · Linked",
            en(R.string.scrobble_status_paused) to "ListenBrainz · Paused",
            en(R.string.scrobble_manage) to "Manage",
            en(R.string.scrobble_link_account) to "Link account",
            en(R.string.scrobble_token_hint) to "ListenBrainz user token",
            en(R.string.scrobble_link_title) to "Link ListenBrainz",
            en(R.string.scrobble_link_message) to
                "When you finish a music track, OnScreen submits a listen to your " +
                "ListenBrainz account. Paste your user token from " +
                "listenbrainz.org/settings (or link from the web or phone app).",
            en(R.string.scrobble_link) to "Link",
            en(R.string.listenbrainz) to "ListenBrainz",
            en(R.string.scrobble_manage_message) to
                "Your ListenBrainz account is linked. Listens are submitted when you finish a music track.",
            en(R.string.scrobble_replace_token) to "Replace token",
            en(R.string.scrobble_unlink) to "Unlink",
            en(R.string.scrobble_link_failed) to "Could not link account",
            en(R.string.scrobble_unlink_failed) to "Could not unlink account",
            en(R.string.playback_settings) to "Playback",
            // Report a problem
            TestStrings.resolve(UiText.Plural(R.plurals.report_note_too_long, 1000)) to
                "Notes can be at most 1000 characters.",
        )
        for ((actual, expected) in cases) {
            assertWithMessage("%s", expected).that(actual).isEqualTo(expected)
        }
    }

    @Test
    fun `the hidden-matches row keeps its English and takes plurals elsewhere`() {
        assertThat(plural(R.plurals.filter_all_hidden, 1, AppLanguage.ENGLISH))
            .isEqualTo("1 match(es) hidden by the type filters above")
        assertThat(plural(R.plurals.filter_all_hidden, 4, AppLanguage.ENGLISH))
            .isEqualTo("4 match(es) hidden by the type filters above")
        assertThat(plural(R.plurals.filter_all_hidden, 1, AppLanguage.SPANISH))
            .isEqualTo("1 coincidencia oculta por los filtros de tipo de arriba")
        assertThat(plural(R.plurals.filter_all_hidden, 4, AppLanguage.SPANISH))
            .isEqualTo("4 coincidencias ocultas por los filtros de tipo de arriba")
        assertThat(plural(R.plurals.filter_all_hidden, 4, AppLanguage.CHINESE))
            .isEqualTo("上方的类型筛选隐藏了 4 个匹配项")
    }

    @Test
    fun `placeholders fill in Spanish and Chinese`() {
        assertThat(es(R.string.up_next_countdown, 9)).isEqualTo("A CONTINUACIÓN · 9 s")
        assertThat(zh(R.string.up_next_countdown, 9)).isEqualTo("即将播放 · 9 秒")
        assertThat(es(R.string.search_in_scope, "Películas")).isEqualTo("Buscar en: Películas")
        assertThat(zh(R.string.search_in_scope, "电影")).isEqualTo("搜索范围：电影")
        assertThat(zh(R.string.audio_channels, 6)).isEqualTo("6 声道")
        assertThat(zh(R.string.play_button_description, "播放 S1 · E2", "试播集")).isEqualTo("播放 S1 · E2，试播集")
        assertThat(es(R.string.language_device_current, "English")).isEqualTo("Idioma del dispositivo (English)")
        assertThat(zh(R.string.language_device_current, "English")).isEqualTo("跟随设备语言（English）")
        assertThat(es(R.string.report_history_open_age, "Problema de audio", "ayer"))
            .isEqualTo("Informaste de “Problema de audio” ayer. Un administrador lo revisará.")
        assertThat(plural(R.plurals.watch_badge_unwatched, 1, AppLanguage.SPANISH)).isEqualTo("1 episodio sin ver")
        assertThat(plural(R.plurals.watch_badge_unwatched, 1_000_000, AppLanguage.SPANISH))
            .isEqualTo("1000000 episodios sin ver")
    }
}
