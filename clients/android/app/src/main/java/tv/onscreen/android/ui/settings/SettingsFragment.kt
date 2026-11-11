package tv.onscreen.android.ui.settings

import android.app.AlertDialog
import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.model.QualityProfile
import tv.onscreen.android.data.model.QualityProfileBody
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.i18n.AppLanguage
import tv.onscreen.android.i18n.AppLocale
import tv.onscreen.android.ui.MainActivity
import tv.onscreen.android.ui.NavigationDestination
import tv.onscreen.android.ui.common.dismissOnViewDestroyed
import tv.onscreen.android.ui.common.focusableOnTv
import javax.inject.Inject

@AndroidEntryPoint
class SettingsFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: SettingsViewModel
    private var currentPrefs: UserPreferences = UserPreferences()

    companion object {
        // ISO 639-2/T three-letter codes — the SAME values the web settings
        // page writes and the closest match to ffprobe's stream languages.
        // This picker used to write 639-1 two-letter codes, which nothing
        // else in the system produces or consumes: a web-set "eng" rendered
        // here as "System default", and a TV-set "en" then overwrote it.
        private val LANGUAGE_OPTIONS = listOf(
            null to R.string.lang_system_default,
            "eng" to R.string.lang_english,
            "spa" to R.string.lang_spanish,
            "fra" to R.string.lang_french,
            "deu" to R.string.lang_german,
            "ita" to R.string.lang_italian,
            "por" to R.string.lang_portuguese,
            "jpn" to R.string.lang_japanese,
            "kor" to R.string.lang_korean,
            "zho" to R.string.lang_chinese,
            "rus" to R.string.lang_russian,
            "hin" to R.string.lang_hindi,
            "ara" to R.string.lang_arabic,
        )

        /** Legacy migration for values this screen itself wrote before the
         *  639-2 switch. Applied on READ so an old install displays and
         *  re-saves correctly. */
        private val LEGACY_LANG = mapOf(
            "en" to "eng", "es" to "spa", "fr" to "fra", "de" to "deu",
            "it" to "ita", "pt" to "por", "ja" to "jpn", "ko" to "kor",
            "zh" to "zho", "ru" to "rus", "hi" to "hin", "ar" to "ara",
        )

        private fun canonicalLang(code: String?): String? =
            code?.let { LEGACY_LANG[it.lowercase()] ?: it }

    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View =
        inflater.inflate(R.layout.fragment_settings, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[SettingsViewModel::class.java]

        val accountText = view.findViewById<TextView>(R.id.settings_account)
        val appLanguageBtn = view.findViewById<Button>(R.id.btn_app_language)
        val appLanguageNote = view.findViewById<TextView>(R.id.app_language_note)
        val audioBtn = view.findViewById<Button>(R.id.btn_audio_lang)
        val subtitleBtn = view.findViewById<Button>(R.id.btn_subtitle_lang)
        val ratingBtn = view.findViewById<Button>(R.id.btn_max_rating)
        val changeServerBtn = view.findViewById<Button>(R.id.btn_change_server)
        val logoutBtn = view.findViewById<Button>(R.id.btn_logout)
        val status = view.findViewById<TextView>(R.id.settings_status)
        val scrobbleStatus = view.findViewById<TextView>(R.id.scrobble_status)
        val scrobbleBtn = view.findViewById<Button>(R.id.btn_scrobble)
        val frameRateBtn = view.findViewById<Button>(R.id.btn_match_frame_rate)
        val heightBtn = view.findViewById<Button>(R.id.btn_quality_height)
        val videoKbpsBtn = view.findViewById<Button>(R.id.btn_quality_video_bitrate)
        val audioKbpsBtn = view.findViewById<Button>(R.id.btn_quality_audio_bitrate)
        val codecBtn = view.findViewById<Button>(R.id.btn_quality_codec)

        viewLifecycleOwner.lifecycleScope.launch {
            val username = prefs.username.first()
            val server = prefs.serverUrl.first()
            viewModel.load(username, server)

            viewModel.uiState.collectLatest { state ->
                currentPrefs = state.preferences
                accountText.text = formatAccountLine(state.username, state.serverUrl)
                appLanguageBtn.text = appLanguageLabel(state.appLanguage)
                appLanguageNote.visibility = if (state.languageSaveFailed) View.VISIBLE else View.GONE
                audioBtn.text = labelFor(LANGUAGE_OPTIONS, state.preferences.preferred_audio_lang)
                subtitleBtn.text = labelFor(LANGUAGE_OPTIONS, state.preferences.preferred_subtitle_lang)
                ratingBtn.text = state.preferences.max_content_rating ?: getString(R.string.no_limit)
                // Gate every preference-editing row until a real load
                // succeeded: the form used to be fully interactive over the
                // all-null defaults, and the next save PUT those nulls back —
                // wiping preferences set from other clients.
                val editable = state.prefsLoaded
                audioBtn.isEnabled = editable
                subtitleBtn.isEnabled = editable

                // Playback quality: a value set elsewhere that no option
                // lists shows as itself, never as "no limit".
                val p = QualityProfile.fromPreferences(state.preferences)
                heightBtn.text = QualityProfile.labelOf(heightOptions(p), p.max_video_height).resolve(requireContext())
                videoKbpsBtn.text = QualityProfile.labelOf(videoKbpsOptions(p), p.max_video_bitrate_kbps).resolve(requireContext())
                audioKbpsBtn.text = QualityProfile.labelOf(audioKbpsOptions(p), p.max_audio_bitrate_kbps).resolve(requireContext())
                codecBtn.text = QualityProfile.labelOf(QualityProfile.CODECS, p.preferred_video_codec).resolve(requireContext())
                for (b in listOf(heightBtn, videoKbpsBtn, audioKbpsBtn, codecBtn)) b.isEnabled = editable

                scrobbleStatus.setText(
                    when {
                        !state.scrobbleLinked -> R.string.scrobble_status_not_linked
                        state.scrobbleEnabled -> R.string.scrobble_status_linked
                        else -> R.string.scrobble_status_paused
                    },
                )
                scrobbleBtn.setText(if (state.scrobbleLinked) R.string.scrobble_manage else R.string.scrobble_link_account)

                when {
                    state.error != null -> {
                        status.text = state.error.resolve(requireContext())
                        status.visibility = View.VISIBLE
                    }
                    state.saved -> {
                        // Stays visible until the ViewModel clears `saved` after a
                        // short delay; clearing it here would hide it in the same
                        // frame it appears.
                        status.text = getString(R.string.saved)
                        status.visibility = View.VISIBLE
                    }
                    else -> status.visibility = View.GONE
                }
            }
        }

        // The app's language (v3): Device language, English, Español, 简体中文.
        // A pick switches at once (the screen is recreated in it) and is
        // saved to the account; when that save fails it stays on this TV and
        // the note under the row says so. A device setting too, so it works
        // before the preferences load (and when they can't).
        appLanguageBtn.setOnClickListener {
            val current = viewModel.uiState.value.appLanguage
            val options = listOf<Pair<String?, String>>(null to getString(R.string.language_device)) +
                AppLanguage.SUPPORTED.map { it to AppLanguage.NAMES.getValue(it) }
            showOptionPicker(R.string.language_label, options, current) { tag ->
                if (tag != current) viewModel.chooseLanguage(tag)
            }
        }

        audioBtn.setOnClickListener {
            showOptionPicker(R.string.preferred_audio_language, languageOptions(), canonicalLang(currentPrefs.preferred_audio_lang)) { code ->
                viewModel.savePreferences(currentPrefs.copy(preferred_audio_lang = code))
            }
        }
        subtitleBtn.setOnClickListener {
            showOptionPicker(R.string.preferred_subtitle_language, languageOptions(), canonicalLang(currentPrefs.preferred_subtitle_lang)) { code ->
                viewModel.savePreferences(currentPrefs.copy(preferred_subtitle_lang = code))
            }
        }
        // Read-only: the ceiling is the ADMIN-set parental control. The
        // server's preferences PUT never persisted this field, but the row
        // was wired as a picker that reported "Saved" — a parental control
        // giving false assurance. Display the value; explain on click.
        ratingBtn.setOnClickListener {
            AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
                .setTitle(R.string.max_content_rating)
                .setMessage(R.string.max_rating_admin_managed)
                .setPositiveButton(android.R.string.ok) { d, _ -> d.dismiss() }
                .create()
                .focusableOnTv()
                .dismissOnViewDestroyed(this)
                .show()
        }

        // Playback quality (v2.6): each pick saves the whole profile, the
        // other fields echoed from a fresh read (SettingsViewModel.saveQuality).
        heightBtn.setOnClickListener {
            val p = QualityProfile.fromPreferences(currentPrefs)
            showValuePicker(R.string.quality_max_resolution, heightOptions(p), p.max_video_height) { v ->
                viewModel.saveQuality { it.copy(max_video_height = v) }
            }
        }
        videoKbpsBtn.setOnClickListener {
            val p = QualityProfile.fromPreferences(currentPrefs)
            showValuePicker(R.string.quality_max_video_bitrate, videoKbpsOptions(p), p.max_video_bitrate_kbps) { v ->
                viewModel.saveQuality { it.copy(max_video_bitrate_kbps = v) }
            }
        }
        audioKbpsBtn.setOnClickListener {
            val p = QualityProfile.fromPreferences(currentPrefs)
            showValuePicker(R.string.quality_max_audio_bitrate, audioKbpsOptions(p), p.max_audio_bitrate_kbps) { v ->
                viewModel.saveQuality { it.copy(max_audio_bitrate_kbps = v) }
            }
        }
        codecBtn.setOnClickListener {
            val p = QualityProfile.fromPreferences(currentPrefs)
            showValuePicker(R.string.quality_video_format, QualityProfile.CODECS, p.preferred_video_codec) { v ->
                viewModel.saveQuality { it.copy(preferred_video_codec = v) }
            }
        }

        // A device setting (the TV, not the account): kept in this box's
        // DataStore, read by the player when it starts.
        var matchFrameRate = true
        viewLifecycleOwner.lifecycleScope.launch {
            prefs.matchFrameRate.collectLatest { on ->
                matchFrameRate = on
                frameRateBtn.text = getString(if (on) R.string.setting_on else R.string.setting_off)
            }
        }
        frameRateBtn.setOnClickListener {
            val next = !matchFrameRate
            viewLifecycleOwner.lifecycleScope.launch { prefs.setMatchFrameRate(next) }
        }

        scrobbleBtn.setOnClickListener {
            if (viewModel.uiState.value.scrobbleLinked) showScrobbleManage() else showLinkDialog()
        }

        // Both teardown flows run on the ACTIVITY scope, not the view scope.
        // logout() flips isLoggedIn while server_url is still set — exactly
        // the predicate MainActivity's mid-session logout watcher navigates
        // to LOGIN on — and that navigation destroys THIS fragment's view,
        // cancelling a view-scoped coroutine mid-flow: clearAll() and the
        // SERVER_SETUP navigation were abandoned, landing the user on Login
        // for the server they were trying to leave (with its URL still
        // persisted). The activity outlives the fragment swap, so the full
        // sequence completes and the last navigation wins.
        changeServerBtn.setOnClickListener {
            confirm(R.string.change_server, R.string.confirm_change_server) {
                val act = activity as? MainActivity ?: return@confirm
                act.lifecycleScope.launch {
                    viewModel.logout()
                    prefs.clearAll()
                    act.navigateTo(NavigationDestination.SERVER_SETUP)
                }
            }
        }

        logoutBtn.setOnClickListener {
            confirm(R.string.log_out, R.string.confirm_log_out) {
                val act = activity as? MainActivity ?: return@confirm
                act.lifecycleScope.launch {
                    viewModel.logout()
                    act.navigateTo(NavigationDestination.LOGIN)
                }
            }
        }

        // Fire TV / D-pad: a plain Fragment opens with nothing focused, so the
        // first remote press is swallowed establishing focus instead of acting
        // on a control. Land focus on the first setting so the remote works on
        // the very first press. Posted so the view is laid out before we ask.
        // (Also where focus lands after a language change recreates the screen.)
        appLanguageBtn.post { appLanguageBtn.requestFocus() }
    }

    /** The Language row: the language picked on this device (or synced from
     *  the account), named in itself, else "Device language (English)". */
    private fun appLanguageLabel(choice: String?): String {
        choice?.let { AppLanguage.NAMES[it] }?.let { return it }
        val shown = AppLanguage.NAMES.getValue(AppLocale.current(requireContext()))
        return getString(R.string.language_device_current, shown)
    }

    /** [LANGUAGE_OPTIONS] with their names in the app's language. */
    private fun languageOptions(): List<Pair<String?, String>> =
        LANGUAGE_OPTIONS.map { (code, name) -> code to getString(name) }

    private fun formatAccountLine(username: String?, server: String?): String {
        val u = username ?: getString(R.string.account_unknown_user)
        val s = server ?: ""
        return if (s.isNotEmpty()) "$u · $s" else u
    }

    private fun labelFor(options: List<Pair<String?, Int>>, code: String?): String {
        val canon = canonicalLang(code)
        // Unknown-but-set values display AS the code — falling back to the
        // first option claimed "System default" for a perfectly good value
        // set from another client.
        return options.firstOrNull { it.first == canon }?.second?.let(::getString)
            ?: (code ?: getString(options.first().second))
    }

    private fun confirm(titleRes: Int, messageRes: Int, onConfirm: () -> Unit) {
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(titleRes)
            .setMessage(messageRes)
            // isAdded: the dialog outlives fragment replacement (it's on the
            // activity window), so a click can arrive after the SCREEN_OFF
            // home reset detached us — dismissOnViewDestroyed closes that
            // window, and this guard covers a dismiss already in flight.
            .setPositiveButton(titleRes) { d, _ -> d.dismiss(); if (isAdded) onConfirm() }
            .setNegativeButton(R.string.cancel) { d, _ -> d.dismiss() }
            .create()
            .focusableOnTv()
            .dismissOnViewDestroyed(this)
            .show()
    }

    private fun showOptionPicker(
        titleRes: Int,
        options: List<Pair<String?, String>>,
        currentCode: String?,
        onSelect: (String?) -> Unit,
    ) {
        val labels = options.map { it.second }.toTypedArray()
        val checked = options.indexOfFirst { it.first == currentCode }.coerceAtLeast(0)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(titleRes)
            .setSingleChoiceItems(labels, checked) { d, idx ->
                onSelect(options[idx].first)
                d.dismiss()
            }
            .create()
            .dismissOnViewDestroyed(this)
            .show()
    }

    private fun heightOptions(p: QualityProfileBody) =
        QualityProfile.withStored(QualityProfile.HEIGHTS, p.max_video_height, QualityProfile::heightLabel)

    private fun videoKbpsOptions(p: QualityProfileBody) =
        QualityProfile.withStored(QualityProfile.VIDEO_KBPS, p.max_video_bitrate_kbps, QualityProfile::videoKbpsLabel)

    private fun audioKbpsOptions(p: QualityProfileBody) =
        QualityProfile.withStored(QualityProfile.AUDIO_KBPS, p.max_audio_bitrate_kbps, QualityProfile::audioKbpsLabel)

    /** [showOptionPicker] for a quality option list; picking the current
     *  value again saves nothing. */
    private fun <T> showValuePicker(
        titleRes: Int,
        options: List<QualityProfile.Option<T>>,
        current: T?,
        onSelect: (T?) -> Unit,
    ) {
        val labels = options.map { it.label.resolve(requireContext()) }.toTypedArray()
        val checked = options.indexOfFirst { it.value == current }.coerceAtLeast(0)
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(titleRes)
            .setSingleChoiceItems(labels, checked) { d, idx ->
                d.dismiss()
                val picked = options[idx].value
                if (isAdded && picked != current) onSelect(picked)
            }
            .create()
            .dismissOnViewDestroyed(this)
            .show()
    }

    /** Token-entry dialog. Typing a long token on a remote is slow, so the
     *  message points at the easier web / phone flow too. An empty entry is
     *  ignored. */
    private fun showLinkDialog() {
        val input = EditText(requireContext()).apply {
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            setHint(R.string.scrobble_token_hint)
        }
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.scrobble_link_title)
            .setMessage(R.string.scrobble_link_message)
            .setView(input)
            .setPositiveButton(R.string.scrobble_link) { d, _ ->
                val token = input.text?.toString()?.trim().orEmpty()
                if (token.isNotEmpty()) viewModel.linkListenBrainz(token)
                d.dismiss()
            }
            .setNegativeButton(R.string.cancel) { d, _ -> d.dismiss() }
            .create()
            .focusableOnTv()
            .dismissOnViewDestroyed(this)
            .show()
    }

    /** Linked-state actions: replace the token or unlink. */
    private fun showScrobbleManage() {
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(R.string.listenbrainz)
            .setMessage(R.string.scrobble_manage_message)
            .setPositiveButton(R.string.scrobble_replace_token) { d, _ -> d.dismiss(); showLinkDialog() }
            // Unlink is irreversible (the token is discarded server-side) and
            // sits one D-pad press from the focused button — confirm it, like
            // every other destructive action on this screen.
            .setNegativeButton(R.string.scrobble_unlink) { d, _ ->
                d.dismiss()
                if (isAdded) {
                    confirm(R.string.unlink_listenbrainz, R.string.confirm_unlink_listenbrainz) {
                        viewModel.unlinkListenBrainz()
                    }
                }
            }
            .setNeutralButton(R.string.cancel) { d, _ -> d.dismiss() }
            .create()
            .focusableOnTv()
            .dismissOnViewDestroyed(this)
            .show()
    }
}
