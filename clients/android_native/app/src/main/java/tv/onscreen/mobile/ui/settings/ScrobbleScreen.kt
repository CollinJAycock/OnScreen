package tv.onscreen.mobile.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel

private const val LISTENBRAINZ_SETTINGS_URL = "https://listenbrainz.org/settings/"

// Mirrors the web --success badge colour (green pill for a linked account).
private val BadgeOnContainer = Color(0x2634D399)
private val BadgeOnContent = Color(0xFF34D399)

/**
 * Per-user ListenBrainz link screen. Laid out to mirror the web
 * Settings → Scrobbling page: a single card with a status badge in the
 * header, the same explainer copy, numbered link steps, an error banner,
 * and the linked-state actions + hint.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ScrobbleScreen(
    onBack: () -> Unit,
    vm: ScrobbleViewModel = hiltViewModel(),
) {
    val state by vm.state.collectAsState()
    val busy by vm.busy.collectAsState()

    // Revealed when a linked user wants to swap in a new token.
    var replacing by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Scrobbling") },
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
                .padding(16.dp)
                .verticalScroll(rememberScrollState()),
        ) {
            OutlinedCard(modifier = Modifier.fillMaxWidth()) {
                Column(modifier = Modifier.padding(20.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.Top,
                    ) {
                        Text(
                            "ListenBrainz",
                            style = MaterialTheme.typography.titleMedium,
                            modifier = Modifier.weight(1f),
                        )
                        StatusBadge(state)
                    }
                    Spacer(Modifier.height(8.dp))
                    Text(
                        "When you finish a music track, OnScreen submits a listen to " +
                            "your ListenBrainz account. A play counts once you're past " +
                            "half the track, or four minutes — whichever comes first. " +
                            "One-way and best-effort: it never reads anything back and " +
                            "never affects playback.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(20.dp))

                    when (val s = state) {
                        ScrobbleUiState.Loading ->
                            CircularProgressIndicator()

                        is ScrobbleUiState.Unlinked -> {
                            s.error?.let { ErrorBanner(it); Spacer(Modifier.height(12.dp)) }
                            LinkBody(busy = busy, onLink = vm::link, onCancel = null)
                        }

                        is ScrobbleUiState.Linked ->
                            if (replacing) {
                                s.error?.let { ErrorBanner(it); Spacer(Modifier.height(12.dp)) }
                                LinkBody(
                                    busy = busy,
                                    onLink = { token -> replacing = false; vm.link(token) },
                                    onCancel = { replacing = false },
                                )
                            } else {
                                LinkedBody(
                                    enabled = s.enabled,
                                    error = s.error,
                                    busy = busy,
                                    onReplace = { replacing = true },
                                    onUnlink = vm::unlink,
                                )
                            }
                    }
                }
            }
        }
    }
}

/** Status pill mirroring the web badge: Linked / Paused (green) when a
 *  token is set, Off (muted) otherwise. Hidden while loading. */
@Composable
private fun StatusBadge(state: ScrobbleUiState) {
    val (label, on) = when (state) {
        ScrobbleUiState.Loading -> return
        is ScrobbleUiState.Unlinked -> "Off" to false
        is ScrobbleUiState.Linked -> (if (state.enabled) "Linked" else "Paused") to true
    }
    Surface(
        color = if (on) BadgeOnContainer else MaterialTheme.colorScheme.surfaceVariant,
        shape = RoundedCornerShape(50),
    ) {
        Text(
            label.uppercase(),
            style = MaterialTheme.typography.labelSmall,
            color = if (on) BadgeOnContent else MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 4.dp),
        )
    }
}

@Composable
private fun LinkBody(
    busy: Boolean,
    onLink: (String) -> Unit,
    onCancel: (() -> Unit)?,
) {
    val uriHandler = LocalUriHandler.current
    var token by remember { mutableStateOf("") }

    StepRow(1, "Open your ListenBrainz settings and copy your user token.")
    TextButton(onClick = { uriHandler.openUri(LISTENBRAINZ_SETTINGS_URL) }) {
        Text("Open ListenBrainz settings")
    }
    Spacer(Modifier.height(8.dp))
    StepRow(2, "Paste it here and link.")
    Spacer(Modifier.height(8.dp))
    OutlinedTextField(
        value = token,
        onValueChange = { token = it },
        singleLine = true,
        label = { Text("ListenBrainz user token") },
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
        modifier = Modifier.fillMaxWidth(),
    )
    Spacer(Modifier.height(12.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Button(
            onClick = { if (token.isNotBlank()) onLink(token) },
            enabled = !busy && token.isNotBlank(),
        ) { Text(if (busy) "Linking…" else "Link account") }
        if (onCancel != null) {
            OutlinedButton(onClick = onCancel, enabled = !busy) { Text("Cancel") }
        }
    }
}

@Composable
private fun LinkedBody(
    enabled: Boolean,
    error: String?,
    busy: Boolean,
    onReplace: () -> Unit,
    onUnlink: () -> Unit,
) {
    Text(
        if (enabled) {
            "Your ListenBrainz account is linked and listens are being submitted."
        } else {
            "A token is linked but scrobbling is currently off. Replace the token to re-enable it."
        },
        style = MaterialTheme.typography.bodyMedium,
    )
    error?.let { Spacer(Modifier.height(12.dp)); ErrorBanner(it) }
    Spacer(Modifier.height(16.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedButton(onClick = onReplace, enabled = !busy) { Text("Replace token") }
        Button(onClick = onUnlink, enabled = !busy) {
            Text(if (busy) "Unlinking…" else "Unlink account")
        }
    }
    Spacer(Modifier.height(8.dp))
    Text(
        "Your token is stored encrypted and is never shown again. Unlinking removes it.",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

@Composable
private fun StepRow(n: Int, text: String) {
    Row {
        Text(
            "$n.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.width(8.dp))
        Text(text, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun ErrorBanner(msg: String) {
    Surface(
        color = MaterialTheme.colorScheme.errorContainer,
        shape = RoundedCornerShape(8.dp),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(
            msg,
            color = MaterialTheme.colorScheme.onErrorContainer,
            style = MaterialTheme.typography.bodySmall,
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
        )
    }
}
