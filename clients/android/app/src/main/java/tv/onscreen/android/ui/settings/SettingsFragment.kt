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
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.data.prefs.ServerPrefs
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
            null to "System default",
            "eng" to "English",
            "spa" to "Spanish",
            "fra" to "French",
            "deu" to "German",
            "ita" to "Italian",
            "por" to "Portuguese",
            "jpn" to "Japanese",
            "kor" to "Korean",
            "zho" to "Chinese",
            "rus" to "Russian",
            "hin" to "Hindi",
            "ara" to "Arabic",
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
        val audioBtn = view.findViewById<Button>(R.id.btn_audio_lang)
        val subtitleBtn = view.findViewById<Button>(R.id.btn_subtitle_lang)
        val ratingBtn = view.findViewById<Button>(R.id.btn_max_rating)
        val changeServerBtn = view.findViewById<Button>(R.id.btn_change_server)
        val logoutBtn = view.findViewById<Button>(R.id.btn_logout)
        val status = view.findViewById<TextView>(R.id.settings_status)
        val scrobbleStatus = view.findViewById<TextView>(R.id.scrobble_status)
        val scrobbleBtn = view.findViewById<Button>(R.id.btn_scrobble)

        viewLifecycleOwner.lifecycleScope.launch {
            val username = prefs.username.first()
            val server = prefs.serverUrl.first()
            viewModel.load(username, server)

            viewModel.uiState.collectLatest { state ->
                currentPrefs = state.preferences
                accountText.text = formatAccountLine(state.username, state.serverUrl)
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

                scrobbleStatus.text = when {
                    !state.scrobbleLinked -> "ListenBrainz · Not linked"
                    state.scrobbleEnabled -> "ListenBrainz · Linked"
                    else -> "ListenBrainz · Paused"
                }
                scrobbleBtn.text = if (state.scrobbleLinked) "Manage" else "Link account"

                when {
                    state.error != null -> {
                        status.text = state.error
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

        audioBtn.setOnClickListener {
            showOptionPicker(R.string.preferred_audio_language, LANGUAGE_OPTIONS, canonicalLang(currentPrefs.preferred_audio_lang)) { code ->
                viewModel.savePreferences(currentPrefs.copy(preferred_audio_lang = code))
            }
        }
        subtitleBtn.setOnClickListener {
            showOptionPicker(R.string.preferred_subtitle_language, LANGUAGE_OPTIONS, canonicalLang(currentPrefs.preferred_subtitle_lang)) { code ->
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
        audioBtn.post { audioBtn.requestFocus() }
    }

    private fun formatAccountLine(username: String?, server: String?): String {
        val u = username ?: "unknown"
        val s = server ?: ""
        return if (s.isNotEmpty()) "$u · $s" else u
    }

    private fun labelFor(options: List<Pair<String?, String>>, code: String?): String {
        val canon = canonicalLang(code)
        // Unknown-but-set values display AS the code — falling back to the
        // first option claimed "System default" for a perfectly good value
        // set from another client.
        return options.firstOrNull { it.first == canon }?.second ?: (code ?: options.first().second)
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

    /** Token-entry dialog. Typing a long token on a remote is slow, so the
     *  message points at the easier web / phone flow too. An empty entry is
     *  ignored. */
    private fun showLinkDialog() {
        val input = EditText(requireContext()).apply {
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            hint = "ListenBrainz user token"
        }
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle("Link ListenBrainz")
            .setMessage(
                "When you finish a music track, OnScreen submits a listen to your " +
                    "ListenBrainz account. Paste your user token from " +
                    "listenbrainz.org/settings (or link from the web or phone app).",
            )
            .setView(input)
            .setPositiveButton("Link") { d, _ ->
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
            .setTitle("ListenBrainz")
            .setMessage("Your ListenBrainz account is linked. Listens are submitted when you finish a music track.")
            .setPositiveButton("Replace token") { d, _ -> d.dismiss(); showLinkDialog() }
            // Unlink is irreversible (the token is discarded server-side) and
            // sits one D-pad press from the focused button — confirm it, like
            // every other destructive action on this screen.
            .setNegativeButton("Unlink") { d, _ ->
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
