package tv.onscreen.mobile.ui.settings

import androidx.compose.foundation.clickable
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import tv.onscreen.mobile.R
import tv.onscreen.mobile.cast.ChromecastCaps
import tv.onscreen.mobile.i18n.AppLanguage
import tv.onscreen.mobile.playback.ReplayGain
import tv.onscreen.mobile.playback.ReplayGainMode
import java.text.DecimalFormatSymbols
import java.util.Locale

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onOpenAbout: () -> Unit,
    onOpenSecurity: () -> Unit,
    onOpenScrobble: () -> Unit,
    onOpenPlaybackQuality: () -> Unit = {},
    vm: SettingsViewModel = hiltViewModel(),
) {
    val downloadOnWifiOnly by vm.downloadOnWifiOnly.collectAsStateWithLifecycle(initialValue = true)
    val warnOnCellularStream by vm.warnOnCellularStream.collectAsStateWithLifecycle(initialValue = true)
    val username by vm.username.collectAsStateWithLifecycle(initialValue = null)
    val serverUrl by vm.serverUrl.collectAsStateWithLifecycle(initialValue = null)
    val replayGainMode by vm.replayGainMode.collectAsStateWithLifecycle(initialValue = ReplayGainMode.OFF)
    val replayGainPreampDb by vm.replayGainPreampDb.collectAsStateWithLifecycle(initialValue = 0.0)
    val castQuality by vm.castQuality.collectAsStateWithLifecycle(initialValue = ChromecastCaps.Quality.AUTO)
    val languageChoice by vm.languageChoice.collectAsStateWithLifecycle()
    val languageSaveFailed by vm.languageSaveFailed.collectAsStateWithLifecycle()

    var showSignOutConfirm by remember { mutableStateOf(false) }
    var showDisconnectConfirm by remember { mutableStateOf(false) }
    var showLanguagePicker by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.settings)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back))
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
        ) {
            SectionHeader(stringResource(R.string.settings_section_language))

            LanguageRow(
                choice = languageChoice,
                saveFailed = languageSaveFailed,
                onClick = { showLanguagePicker = true },
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader(stringResource(R.string.settings_section_network))

            ToggleRow(
                title = stringResource(R.string.settings_wifi_only_title),
                description = stringResource(R.string.settings_wifi_only_desc),
                checked = downloadOnWifiOnly,
                onChange = vm::setDownloadOnWifiOnly,
            )

            ToggleRow(
                title = stringResource(R.string.settings_cellular_warn_title),
                description = stringResource(R.string.settings_cellular_warn_desc),
                checked = warnOnCellularStream,
                onChange = vm::setWarnOnCellularStream,
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader(stringResource(R.string.settings_section_playback))

            ActionRow(
                title = stringResource(R.string.settings_quality_title),
                description = stringResource(R.string.settings_quality_desc),
                onClick = onOpenPlaybackQuality,
            )

            ReplayGainSettings(
                mode = replayGainMode,
                preampDb = replayGainPreampDb,
                onModeChange = vm::setReplayGainMode,
                onPreampChange = vm::setReplayGainPreampDb,
            )

            if (tv.onscreen.mobile.BuildConfig.CAST_ENABLED) {
                CastQualitySetting(quality = castQuality, onChange = vm::setCastQuality)
            }

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader(stringResource(R.string.settings_section_account))

            // Identity row — read-only summary so the user can see
            // which account / server they're about to sign out of.
            // Suppress when either field is missing (defensive — auth
            // gating should make that unreachable here).
            if (!username.isNullOrBlank() || !serverUrl.isNullOrBlank()) {
                Column(modifier = Modifier.padding(vertical = 8.dp)) {
                    if (!username.isNullOrBlank()) {
                        Text(
                            stringResource(R.string.settings_signed_in_as, username!!),
                            style = MaterialTheme.typography.bodyLarge,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    if (!serverUrl.isNullOrBlank()) {
                        Text(
                            serverUrl!!,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }

            ActionRow(
                title = stringResource(R.string.settings_sign_out_title),
                description = stringResource(R.string.settings_sign_out_desc),
                onClick = { showSignOutConfirm = true },
            )

            ActionRow(
                title = stringResource(R.string.settings_forget_title),
                description = stringResource(R.string.settings_forget_desc),
                onClick = { showDisconnectConfirm = true },
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader(stringResource(R.string.settings_section_security))

            ActionRow(
                title = stringResource(R.string.settings_two_factor_title),
                description = stringResource(R.string.settings_two_factor_desc),
                onClick = onOpenSecurity,
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader(stringResource(R.string.settings_section_scrobbling))

            ActionRow(
                title = "ListenBrainz",
                description = stringResource(R.string.settings_listenbrainz_desc),
                onClick = onOpenScrobble,
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader(stringResource(R.string.settings_section_about))

            ActionRow(
                title = stringResource(R.string.settings_about_title),
                description = stringResource(R.string.settings_about_desc),
                onClick = onOpenAbout,
            )
        }
    }

    // Both confirms route through ServerPrefs.clearAuth / clearAll.
    // AppNav observes prefs.isLoggedIn and reroutes to /pair the
    // moment auth state flips, so onBack() unwinds the back stack
    // and the nav graph naturally lands on the pair screen.
    if (showSignOutConfirm) {
        AlertDialog(
            onDismissRequest = { showSignOutConfirm = false },
            title = { Text(stringResource(R.string.settings_sign_out_confirm_title)) },
            text = { Text(stringResource(R.string.settings_sign_out_confirm_body)) },
            confirmButton = {
                TextButton(onClick = {
                    showSignOutConfirm = false
                    vm.signOut()
                    onBack()
                }) { Text(stringResource(R.string.settings_sign_out_title)) }
            },
            dismissButton = {
                TextButton(onClick = { showSignOutConfirm = false }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }

    if (showDisconnectConfirm) {
        AlertDialog(
            onDismissRequest = { showDisconnectConfirm = false },
            title = { Text(stringResource(R.string.settings_forget_confirm_title)) },
            text = { Text(stringResource(R.string.settings_forget_confirm_body)) },
            confirmButton = {
                TextButton(onClick = {
                    showDisconnectConfirm = false
                    vm.disconnectServer()
                    onBack()
                }) { Text(stringResource(R.string.settings_forget_confirm)) }
            },
            dismissButton = {
                TextButton(onClick = { showDisconnectConfirm = false }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }

    if (showLanguagePicker) {
        LanguagePickerDialog(
            choice = languageChoice,
            onChoose = { tag ->
                showLanguagePicker = false
                // Switches at once: the activity is recreated in the new
                // language, then the choice is saved to the account.
                if (tag != languageChoice) vm.chooseLanguage(tag)
            },
            onDismiss = { showLanguagePicker = false },
        )
    }
}

/** A language's name for the picker: "Device language" in the app's
 *  language, the others always in their own (never translated). */
@Composable
private fun languageName(tag: String?): String = when (tag) {
    AppLanguage.ENGLISH -> stringResource(R.string.language_name_en)
    AppLanguage.SPANISH -> stringResource(R.string.language_name_es)
    AppLanguage.CHINESE_SIMPLIFIED -> stringResource(R.string.language_name_zh_hans)
    else -> stringResource(R.string.language_device)
}

/** Settings › Language: the current choice, and a note when the last pick
 *  couldn't be saved to the account. */
@Composable
private fun LanguageRow(
    choice: String?,
    saveFailed: Boolean,
    onClick: () -> Unit,
) {
    ActionRow(
        title = stringResource(R.string.language_label),
        description = languageName(choice),
        onClick = onClick,
    )
    if (saveFailed) {
        Text(
            stringResource(R.string.language_save_failed),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.padding(bottom = 8.dp),
        )
    }
}

/** Device language / English / Español / 简体中文. Picking one applies it at
 *  once and closes the dialog. */
@Composable
private fun LanguagePickerDialog(
    choice: String?,
    onChoose: (String?) -> Unit,
    onDismiss: () -> Unit,
) {
    val options = listOf<String?>(null) + AppLanguage.TAGS
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.language_label)) },
        text = {
            Column {
                Text(
                    stringResource(R.string.language_help),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(bottom = 8.dp),
                )
                Column(Modifier.selectableGroup()) {
                    options.forEach { tag ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .selectable(
                                    selected = tag == choice,
                                    onClick = { onChoose(tag) },
                                    role = Role.RadioButton,
                                )
                                .padding(vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(selected = tag == choice, onClick = null)
                            Spacer(Modifier.width(12.dp))
                            Text(languageName(tag), style = MaterialTheme.typography.bodyLarge)
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.close)) }
        },
    )
}

@Composable
private fun SectionHeader(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(bottom = 8.dp),
    )
}

@Composable
private fun ToggleRow(
    title: String,
    description: String,
    checked: Boolean,
    onChange: (Boolean) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Spacer(Modifier.height(2.dp))
            Text(
                description,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
private fun ActionRow(
    title: String,
    description: String,
    onClick: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 12.dp),
    ) {
        Text(title, style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.height(2.dp))
        Text(
            description,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/**
 * ReplayGain for music: Off / Track / Album + a preamp. Mirrors the web
 * player's setting (web/src/lib/replaygain.ts): album falls back to track
 * tags on files without album gain; untagged files always play untouched;
 * the gain is capped by the file's tagged peak so it never clips.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReplayGainSettings(
    mode: ReplayGainMode,
    preampDb: Double,
    onModeChange: (ReplayGainMode) -> Unit,
    onPreampChange: (Double) -> Unit,
) {
    Column(modifier = Modifier.padding(vertical = 8.dp)) {
        Text("ReplayGain", style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.height(2.dp))
        Text(
            stringResource(R.string.settings_replaygain_desc),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        val options = listOf(
            ReplayGainMode.OFF to stringResource(R.string.settings_replaygain_off),
            ReplayGainMode.TRACK to stringResource(R.string.settings_replaygain_track),
            ReplayGainMode.ALBUM to stringResource(R.string.settings_replaygain_album),
        )
        SingleChoiceSegmentedButtonRow(modifier = Modifier.fillMaxWidth()) {
            options.forEachIndexed { i, (value, label) ->
                val description = stringResource(R.string.settings_replaygain_option_cd, label)
                SegmentedButton(
                    selected = mode == value,
                    onClick = { onModeChange(value) },
                    shape = SegmentedButtonDefaults.itemShape(index = i, count = options.size),
                    modifier = Modifier.semantics { contentDescription = description },
                ) { Text(label) }
            }
        }

        // Preamp: -6..+6 dB in 0.5 dB steps. Dragging updates the label only;
        // the setting is written once on release (one DataStore write, not
        // one per frame). Disabled while ReplayGain is off: it has no effect.
        val enabled = mode != ReplayGainMode.OFF
        var dragging by remember { mutableStateOf<Float?>(null) }
        val shown = dragging?.toDouble() ?: preampDb
        val label = formatPreamp(shown)
        Spacer(Modifier.height(12.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                stringResource(R.string.settings_replaygain_preamp),
                style = MaterialTheme.typography.bodyMedium,
                color = if (enabled) {
                    MaterialTheme.colorScheme.onSurface
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant
                },
                modifier = Modifier.weight(1f),
            )
            Text(
                label,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        val min = ReplayGain.UI_PREAMP_MIN_DB.toFloat()
        val max = ReplayGain.UI_PREAMP_MAX_DB.toFloat()
        val intervals = ((ReplayGain.UI_PREAMP_MAX_DB - ReplayGain.UI_PREAMP_MIN_DB) /
            ReplayGain.UI_PREAMP_STEP_DB).toInt()
        val preampDescription = stringResource(R.string.settings_replaygain_preamp_cd)
        Slider(
            value = shown.toFloat().coerceIn(min, max),
            onValueChange = { dragging = it },
            onValueChangeFinished = {
                dragging?.let { onPreampChange(ReplayGain.snapUiPreamp(it.toDouble())) }
                dragging = null
            },
            valueRange = min..max,
            // `steps` counts the stops BETWEEN the ends: 24 half-dB intervals.
            steps = intervals - 1,
            enabled = enabled,
            modifier = Modifier.semantics {
                contentDescription = preampDescription
                stateDescription = label
            },
        )
        Text(
            stringResource(R.string.settings_replaygain_preamp_help),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/**
 * Chromecast quality: which capability profile a cast asks the server for.
 * Auto reads the device's model and capabilities; 4K or HD overrides it for
 * a receiver the detection gets wrong (a TV with Google TV built in reports
 * its panel's model number, which says nothing about 4K).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CastQualitySetting(
    quality: ChromecastCaps.Quality,
    onChange: (ChromecastCaps.Quality) -> Unit,
) {
    Column(modifier = Modifier.padding(vertical = 8.dp)) {
        Text(stringResource(R.string.settings_cast_quality_title), style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.height(2.dp))
        Text(
            stringResource(R.string.settings_cast_quality_desc),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        val options = listOf(
            ChromecastCaps.Quality.AUTO to stringResource(R.string.settings_cast_quality_auto),
            ChromecastCaps.Quality.UHD to "4K",
            ChromecastCaps.Quality.HD to "HD",
        )
        SingleChoiceSegmentedButtonRow(modifier = Modifier.fillMaxWidth()) {
            options.forEachIndexed { i, (value, label) ->
                val description = stringResource(R.string.settings_cast_quality_option_cd, label)
                SegmentedButton(
                    selected = quality == value,
                    onClick = { onChange(value) },
                    shape = SegmentedButtonDefaults.itemShape(index = i, count = options.size),
                    modifier = Modifier.semantics { contentDescription = description },
                ) { Text(label) }
            }
        }
    }
}

/** Preamp label: "+1.5 dB", "0 dB", or a true minus sign for cuts
 *  (U+2212, "\u22123 dB"). Snapped to the 0.5 dB grid. The decimal mark is
 *  the language's ("+1,5 dB" in Spanish); "dB" is a unit, not translated. */
internal fun formatPreamp(db: Double, locale: Locale = Locale.getDefault()): String {
    val v = ReplayGain.snapUiPreamp(db)
    if (v == 0.0) return "0 dB"
    val sign = if (v > 0) "+" else "\u2212"
    val abs = kotlin.math.abs(v)
    val num = if (abs % 1.0 == 0.0) {
        abs.toInt().toString()
    } else {
        abs.toString().replace('.', DecimalFormatSymbols.getInstance(locale).decimalSeparator)
    }
    return "$sign$num dB"
}
