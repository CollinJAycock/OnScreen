package tv.onscreen.android.ui.settings

import android.app.AlertDialog
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
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
import javax.inject.Inject

@AndroidEntryPoint
class SettingsFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs

    private lateinit var viewModel: SettingsViewModel
    private var currentPrefs: UserPreferences = UserPreferences()

    companion object {
        // ISO 639-1 two-letter codes; null = system default.
        private val LANGUAGE_OPTIONS = listOf(
            null to "System default",
            "en" to "English",
            "es" to "Spanish",
            "fr" to "French",
            "de" to "German",
            "it" to "Italian",
            "pt" to "Portuguese",
            "ja" to "Japanese",
            "ko" to "Korean",
            "zh" to "Chinese",
            "ru" to "Russian",
            "hi" to "Hindi",
            "ar" to "Arabic",
        )

        private val RATING_OPTIONS = listOf(
            null to "No limit",
            "G" to "G",
            "PG" to "PG",
            "PG-13" to "PG-13",
            "R" to "R",
            "NC-17" to "NC-17",
            "TV-Y" to "TV-Y",
            "TV-Y7" to "TV-Y7",
            "TV-G" to "TV-G",
            "TV-PG" to "TV-PG",
            "TV-14" to "TV-14",
            "TV-MA" to "TV-MA",
        )
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

        viewLifecycleOwner.lifecycleScope.launch {
            val username = prefs.username.first()
            val server = prefs.serverUrl.first()
            viewModel.load(username, server)

            viewModel.uiState.collectLatest { state ->
                currentPrefs = state.preferences
                accountText.text = formatAccountLine(state.username, state.serverUrl)
                audioBtn.text = labelFor(LANGUAGE_OPTIONS, state.preferences.preferred_audio_lang)
                subtitleBtn.text = labelFor(LANGUAGE_OPTIONS, state.preferences.preferred_subtitle_lang)
                ratingBtn.text = labelFor(RATING_OPTIONS, state.preferences.max_content_rating)

                when {
                    state.error != null -> {
                        status.text = state.error
                        status.visibility = View.VISIBLE
                    }
                    state.saved -> {
                        status.text = getString(R.string.saved)
                        status.visibility = View.VISIBLE
                        viewModel.clearSavedFlag()
                    }
                    else -> status.visibility = View.GONE
                }
            }
        }

        audioBtn.setOnClickListener {
            showOptionPicker(R.string.preferred_audio_language, LANGUAGE_OPTIONS, currentPrefs.preferred_audio_lang) { code ->
                viewModel.savePreferences(currentPrefs.copy(preferred_audio_lang = code))
            }
        }
        subtitleBtn.setOnClickListener {
            showOptionPicker(R.string.preferred_subtitle_language, LANGUAGE_OPTIONS, currentPrefs.preferred_subtitle_lang) { code ->
                viewModel.savePreferences(currentPrefs.copy(preferred_subtitle_lang = code))
            }
        }
        ratingBtn.setOnClickListener {
            showOptionPicker(R.string.max_content_rating, RATING_OPTIONS, currentPrefs.max_content_rating) { code ->
                viewModel.savePreferences(currentPrefs.copy(max_content_rating = code))
            }
        }

        changeServerBtn.setOnClickListener {
            confirm(R.string.change_server, R.string.confirm_change_server) {
                viewLifecycleOwner.lifecycleScope.launch {
                    viewModel.logout()
                    prefs.clearAll()
                    (activity as? MainActivity)?.navigateTo(NavigationDestination.SERVER_SETUP)
                }
            }
        }

        logoutBtn.setOnClickListener {
            confirm(R.string.log_out, R.string.confirm_log_out) {
                viewLifecycleOwner.lifecycleScope.launch {
                    viewModel.logout()
                    (activity as? MainActivity)?.navigateTo(NavigationDestination.LOGIN)
                }
            }
        }
    }

    private fun formatAccountLine(username: String?, server: String?): String {
        val u = username ?: "unknown"
        val s = server ?: ""
        return if (s.isNotEmpty()) "$u · $s" else u
    }

    private fun labelFor(options: List<Pair<String?, String>>, code: String?): String {
        return options.firstOrNull { it.first == code }?.second ?: options.first().second
    }

    private fun confirm(titleRes: Int, messageRes: Int, onConfirm: () -> Unit) {
        AlertDialog.Builder(requireContext(), R.style.PlayerDialog)
            .setTitle(titleRes)
            .setMessage(messageRes)
            .setPositiveButton(titleRes) { d, _ -> d.dismiss(); onConfirm() }
            .setNegativeButton(R.string.cancel) { d, _ -> d.dismiss() }
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
            .show()
    }
}
