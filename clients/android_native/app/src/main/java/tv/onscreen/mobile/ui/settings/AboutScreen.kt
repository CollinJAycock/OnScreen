package tv.onscreen.mobile.ui.settings

import android.content.pm.PackageManager
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AboutScreen(
    onBack: () -> Unit,
    vm: SettingsViewModel = hiltViewModel(),
) {
    val context = LocalContext.current
    val username by vm.username.collectAsState(initial = null)
    val serverUrl by vm.serverUrl.collectAsState(initial = null)

    // Pull versionName/versionCode from PackageManager rather than
    // BuildConfig — avoids forcing buildFeatures.buildConfig=true on
    // the module just for a two-line surface, and matches what the
    // OS itself reports in app-info.
    val versionLabel = remember {
        try {
            val info = context.packageManager.getPackageInfo(context.packageName, 0)
            val name = info.versionName ?: "?"
            @Suppress("DEPRECATION")
            val code = info.longVersionCode
            "$name ($code)"
        } catch (_: PackageManager.NameNotFoundException) {
            "unknown"
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("About") },
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
                .padding(24.dp),
        ) {
            Text("OnScreen", style = MaterialTheme.typography.headlineMedium)
            Spacer(Modifier.height(4.dp))
            Text(
                "Phone client",
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            Spacer(Modifier.height(24.dp))
            InfoRow("Version", versionLabel)
            if (!username.isNullOrBlank()) {
                Spacer(Modifier.height(12.dp))
                InfoRow("Signed in as", username!!)
            }
            if (!serverUrl.isNullOrBlank()) {
                Spacer(Modifier.height(12.dp))
                InfoRow("Server", serverUrl!!)
            }
        }
    }
}

@Composable
private fun InfoRow(label: String, value: String) {
    Column {
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(2.dp))
        Text(value, style = MaterialTheme.typography.bodyLarge)
    }
}
