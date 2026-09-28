package tv.onscreen.mobile.ui.settings

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
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
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import tv.onscreen.mobile.playback.ReplayGain
import tv.onscreen.mobile.playback.ReplayGainMode

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onOpenAbout: () -> Unit,
    onOpenSecurity: () -> Unit,
    onOpenScrobble: () -> Unit,
    vm: SettingsViewModel = hiltViewModel(),
) {
    val downloadOnWifiOnly by vm.downloadOnWifiOnly.collectAsStateWithLifecycle(initialValue = true)
    val warnOnCellularStream by vm.warnOnCellularStream.collectAsStateWithLifecycle(initialValue = true)
    val username by vm.username.collectAsStateWithLifecycle(initialValue = null)
    val serverUrl by vm.serverUrl.collectAsStateWithLifecycle(initialValue = null)
    val replayGainMode by vm.replayGainMode.collectAsStateWithLifecycle(initialValue = ReplayGainMode.OFF)
    val replayGainPreampDb by vm.replayGainPreampDb.collectAsStateWithLifecycle(initialValue = 0.0)

    var showSignOutConfirm by remember { mutableStateOf(false) }
    var showDisconnectConfirm by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
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
            SectionHeader("Network")

            ToggleRow(
                title = "Download only over Wi-Fi",
                description = "Defer downloads until the device is on Wi-Fi or another unmetered network. Saves cellular data but pauses queued downloads when off Wi-Fi.",
                checked = downloadOnWifiOnly,
                onChange = vm::setDownloadOnWifiOnly,
            )

            ToggleRow(
                title = "Warn before streaming on cellular",
                description = "Confirm before starting video playback on a metered connection. Music and direct-play audio are unaffected.",
                checked = warnOnCellularStream,
                onChange = vm::setWarnOnCellularStream,
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader("Playback")

            ReplayGainSettings(
                mode = replayGainMode,
                preampDb = replayGainPreampDb,
                onModeChange = vm::setReplayGainMode,
                onPreampChange = vm::setReplayGainPreampDb,
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader("Account")

            // Identity row — read-only summary so the user can see
            // which account / server they're about to sign out of.
            // Suppress when either field is missing (defensive — auth
            // gating should make that unreachable here).
            if (!username.isNullOrBlank() || !serverUrl.isNullOrBlank()) {
                Column(modifier = Modifier.padding(vertical = 8.dp)) {
                    if (!username.isNullOrBlank()) {
                        Text(
                            "Signed in as $username",
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
                title = "Sign out",
                description = "Clear your session on this device. The server URL is kept so you can sign in again with the same account.",
                onClick = { showSignOutConfirm = true },
            )

            ActionRow(
                title = "Forget server",
                description = "Remove the server URL and all session state. Use when switching to a different OnScreen deployment.",
                onClick = { showDisconnectConfirm = true },
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader("Security")

            ActionRow(
                title = "Two-factor authentication",
                description = "Add a code from an authenticator app to your password login.",
                onClick = onOpenSecurity,
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader("Scrobbling")

            ActionRow(
                title = "ListenBrainz",
                description = "Submit a listen when you finish a music track.",
                onClick = onOpenScrobble,
            )

            Spacer(Modifier.height(16.dp))
            HorizontalDivider()
            Spacer(Modifier.height(16.dp))

            SectionHeader("About")

            ActionRow(
                title = "About OnScreen",
                description = "App version, build, and connected server.",
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
            title = { Text("Sign out?") },
            text = { Text("You'll need to sign in again to continue using OnScreen on this device.") },
            confirmButton = {
                TextButton(onClick = {
                    showSignOutConfirm = false
                    vm.signOut()
                    onBack()
                }) { Text("Sign out") }
            },
            dismissButton = {
                TextButton(onClick = { showSignOutConfirm = false }) { Text("Cancel") }
            },
        )
    }

    if (showDisconnectConfirm) {
        AlertDialog(
            onDismissRequest = { showDisconnectConfirm = false },
            title = { Text("Forget server?") },
            text = { Text("This removes the server URL and all session state. You'll start over from the server-URL prompt.") },
            confirmButton = {
                TextButton(onClick = {
                    showDisconnectConfirm = false
                    vm.disconnectServer()
                    onBack()
                }) { Text("Forget") }
            },
            dismissButton = {
                TextButton(onClick = { showDisconnectConfirm = false }) { Text("Cancel") }
            },
        )
    }
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
            "Even out loudness between music tracks using their ReplayGain tags. Album keeps an " +
                "album\u2019s own dynamics and falls back to track gain for singles. Untagged files " +
                "play unchanged, and the gain never pushes a track into clipping.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        val options = listOf(
            ReplayGainMode.OFF to "Off",
            ReplayGainMode.TRACK to "Track",
            ReplayGainMode.ALBUM to "Album",
        )
        SingleChoiceSegmentedButtonRow(modifier = Modifier.fillMaxWidth()) {
            options.forEachIndexed { i, (value, label) ->
                SegmentedButton(
                    selected = mode == value,
                    onClick = { onModeChange(value) },
                    shape = SegmentedButtonDefaults.itemShape(index = i, count = options.size),
                    modifier = Modifier.semantics { contentDescription = "ReplayGain $label" },
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
                "Preamp",
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
                contentDescription = "ReplayGain preamp"
                stateDescription = label
            },
        )
        Text(
            "Added to every tagged track\u2019s gain. A boost stops where the track would clip.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** Preamp label: "+1.5 dB", "0 dB", or a true minus sign for cuts
 *  (U+2212, "\u22123 dB"). Snapped to the 0.5 dB grid. */
internal fun formatPreamp(db: Double): String {
    val v = ReplayGain.snapUiPreamp(db)
    if (v == 0.0) return "0 dB"
    val sign = if (v > 0) "+" else "\u2212"
    val abs = kotlin.math.abs(v)
    val num = if (abs % 1.0 == 0.0) abs.toInt().toString() else abs.toString()
    return "$sign$num dB"
}
