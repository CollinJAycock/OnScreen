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
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onOpenAbout: () -> Unit,
    vm: SettingsViewModel = hiltViewModel(),
) {
    val downloadOnWifiOnly by vm.downloadOnWifiOnly.collectAsState(initial = true)
    val warnOnCellularStream by vm.warnOnCellularStream.collectAsState(initial = true)
    val username by vm.username.collectAsState(initial = null)
    val serverUrl by vm.serverUrl.collectAsState(initial = null)

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
                        )
                    }
                    if (!serverUrl.isNullOrBlank()) {
                        Text(
                            serverUrl!!,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
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

