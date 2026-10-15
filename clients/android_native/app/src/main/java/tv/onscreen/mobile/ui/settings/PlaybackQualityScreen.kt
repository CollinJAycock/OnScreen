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
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState

/**
 * Settings → Playback quality. The server applies this profile on every
 * device — at home or away, a video over it is converted down — so it is the
 * account's setting, not this phone's. Options match the web app.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PlaybackQualityScreen(
    onBack: () -> Unit,
    vm: PlaybackQualityViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsStateWithLifecycle()
    var editing by remember { mutableStateOf<QualityProfile.Field?>(null) }

    val context = LocalContext.current
    LaunchedEffect(ui.message) {
        val msg = ui.message ?: return@LaunchedEffect
        android.widget.Toast.makeText(context, msg, android.widget.Toast.LENGTH_SHORT).show()
        vm.consumeMessage()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Playback quality") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        when {
            ui.loading -> LoadingState(modifier = Modifier.padding(padding))
            ui.loadError != null -> ErrorState(
                message = ui.loadError,
                onRetry = vm::load,
                modifier = Modifier.padding(padding),
            )
            else -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(padding)
                    .verticalScroll(rememberScrollState())
                    .padding(16.dp),
            ) {
                Text(
                    "Applies to every device you watch on. Anything above these limits is converted " +
                        "down by the server, at home or away.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(8.dp))
                QualityProfile.Field.entries.forEach { field ->
                    val value = ui.values[field]
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable(enabled = !ui.saving) { editing = field }
                            .padding(vertical = 12.dp),
                    ) {
                        Text(field.title, style = MaterialTheme.typography.bodyLarge)
                        Spacer(Modifier.height(2.dp))
                        Text(
                            QualityProfile.label(field, value),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                if (ui.saving) {
                    Spacer(Modifier.height(8.dp))
                    CircularProgressIndicator(modifier = Modifier.align(Alignment.CenterHorizontally))
                }
            }
        }
    }

    editing?.let { field ->
        val current = ui.values[field]
        AlertDialog(
            onDismissRequest = { editing = null },
            title = { Text(field.title) },
            text = {
                Column {
                    QualityProfile.options(field, current).forEach { opt ->
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            modifier = Modifier
                                .fillMaxWidth()
                                .selectable(
                                    selected = opt.value == current,
                                    role = Role.RadioButton,
                                    onClick = {
                                        editing = null
                                        vm.set(field, opt.value)
                                    },
                                )
                                .padding(vertical = 8.dp),
                        ) {
                            RadioButton(selected = opt.value == current, onClick = null)
                            Spacer(Modifier.padding(start = 8.dp))
                            Text(opt.label, style = MaterialTheme.typography.bodyLarge)
                        }
                    }
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = { editing = null }) { Text("Cancel") } },
        )
    }
}
