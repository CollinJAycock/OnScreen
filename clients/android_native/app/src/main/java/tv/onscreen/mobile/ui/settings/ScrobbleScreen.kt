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
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import tv.onscreen.mobile.R
import tv.onscreen.mobile.i18n.UiText
import tv.onscreen.mobile.i18n.asString
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
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
    val state by vm.state.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()

    // Revealed when a linked user wants to swap in a new token.
    var replacing by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.scrobble_title)) },
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
                        stringResource(R.string.scrobble_intro),
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

/** Status pill mirroring the web badge: Linked (green) when scrobbling is
 *  active, Paused (tertiary) when a token is set but disabled, Off (muted)
 *  when unlinked. Paused gets its own colour so it doesn't read as active.
 *  Hidden while loading. */
@Composable
private fun StatusBadge(state: ScrobbleUiState) {
    val label = when (state) {
        ScrobbleUiState.Loading -> return
        is ScrobbleUiState.Unlinked -> stringResource(R.string.scrobble_badge_off)
        is ScrobbleUiState.Linked ->
            stringResource(if (state.enabled) R.string.scrobble_badge_linked else R.string.scrobble_badge_paused)
    }
    val (container, content) = when {
        state is ScrobbleUiState.Linked && state.enabled ->
            BadgeOnContainer to BadgeOnContent
        state is ScrobbleUiState.Linked ->
            // Paused: distinct from the green "active" pill and from the
            // muted "Off" pill so the disabled state is obvious at a glance.
            MaterialTheme.colorScheme.tertiaryContainer to
                MaterialTheme.colorScheme.onTertiaryContainer
        else ->
            MaterialTheme.colorScheme.surfaceVariant to
                MaterialTheme.colorScheme.onSurfaceVariant
    }
    Surface(
        color = container,
        shape = RoundedCornerShape(50),
    ) {
        Text(
            label.uppercase(),
            style = MaterialTheme.typography.labelSmall,
            color = content,
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
    var token by rememberSaveable { mutableStateOf("") }
    var tokenVisible by rememberSaveable { mutableStateOf(false) }

    StepRow(1, stringResource(R.string.scrobble_step1))
    TextButton(onClick = { uriHandler.openUri(LISTENBRAINZ_SETTINGS_URL) }) {
        Text(stringResource(R.string.scrobble_open_settings))
    }
    Spacer(Modifier.height(8.dp))
    StepRow(2, stringResource(R.string.scrobble_step2))
    Spacer(Modifier.height(8.dp))
    OutlinedTextField(
        value = token,
        onValueChange = { token = it },
        singleLine = true,
        label = { Text(stringResource(R.string.scrobble_token_label)) },
        visualTransformation = if (tokenVisible) VisualTransformation.None
            else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.Password,
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(onDone = { if (token.isNotBlank()) onLink(token) }),
        trailingIcon = {
            IconButton(onClick = { tokenVisible = !tokenVisible }) {
                Icon(
                    imageVector = if (tokenVisible) Icons.Filled.Visibility
                        else Icons.Filled.VisibilityOff,
                    contentDescription = stringResource(
                        if (tokenVisible) R.string.scrobble_hide_token else R.string.scrobble_show_token,
                    ),
                )
            }
        },
        modifier = Modifier.fillMaxWidth(),
    )
    Spacer(Modifier.height(12.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Button(
            onClick = { if (token.isNotBlank()) onLink(token) },
            enabled = !busy && token.isNotBlank(),
        ) { Text(stringResource(if (busy) R.string.scrobble_linking else R.string.scrobble_link)) }
        if (onCancel != null) {
            OutlinedButton(onClick = onCancel, enabled = !busy) { Text(stringResource(R.string.cancel)) }
        }
    }
}

@Composable
private fun LinkedBody(
    enabled: Boolean,
    error: UiText?,
    busy: Boolean,
    onReplace: () -> Unit,
    onUnlink: () -> Unit,
) {
    Text(
        stringResource(if (enabled) R.string.scrobble_linked_on else R.string.scrobble_linked_paused),
        style = MaterialTheme.typography.bodyMedium,
    )
    error?.let { Spacer(Modifier.height(12.dp)); ErrorBanner(it) }
    Spacer(Modifier.height(16.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedButton(onClick = onReplace, enabled = !busy) { Text(stringResource(R.string.scrobble_replace_token)) }
        Button(onClick = onUnlink, enabled = !busy) {
            Text(stringResource(if (busy) R.string.scrobble_unlinking else R.string.scrobble_unlink))
        }
    }
    Spacer(Modifier.height(8.dp))
    Text(
        stringResource(R.string.scrobble_token_note),
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
private fun ErrorBanner(msg: UiText) {
    Surface(
        color = MaterialTheme.colorScheme.errorContainer,
        shape = RoundedCornerShape(8.dp),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(
            msg.asString(),
            color = MaterialTheme.colorScheme.onErrorContainer,

            style = MaterialTheme.typography.bodySmall,
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp),
        )
    }
}
