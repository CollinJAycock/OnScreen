package tv.onscreen.mobile.ui.settings

import android.app.Activity
import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.content.Context
import android.graphics.BitmapFactory
import android.os.Build
import android.os.PersistableBundle
import android.util.Base64
import android.view.WindowManager
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
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
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SecurityScreen(
    onBack: () -> Unit,
    vm: SecurityViewModel = hiltViewModel(),
) {
    val state by vm.state.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()

    // FLAG_SECURE while the TOTP seed or the recovery codes are on screen.
    //
    // Both are login credentials the server will not reissue: SetupTOTP
    // returns ErrTOTPAlreadyEnabled once 2FA is on, so a captured seed
    // discloses material otherwise unobtainable from the running app, and a
    // recovery code is accepted in place of a TOTP at the login challenge.
    // Capture is the platform DEFAULT — FLAG_SECURE is the opt-out — and this
    // screen's own step 1 tells the user to scan the QR in an authenticator
    // app, i.e. to background OnScreen, at which point system_server snapshots
    // the visible window and renders it in the task switcher.
    //
    // Scoped to the two sensitive states rather than the whole screen so the
    // rest of Settings still screenshots normally for support.
    val sensitive = state is SecurityUiState.Enrolling || state is SecurityUiState.Recovery
    val view = LocalView.current
    DisposableEffect(sensitive, view) {
        val window = (view.context as? Activity)?.window
        if (sensitive) {
            window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
            // API 33+ also has an explicit recents opt-out; FLAG_SECURE
            // already covers the snapshot, this is belt and braces for OEMs
            // that special-case it.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                (view.context as? Activity)?.setRecentsScreenshotEnabled(false)
            }
        }
        onDispose {
            window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                (view.context as? Activity)?.setRecentsScreenshotEnabled(true)
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Security") },
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
                .padding(24.dp)
                .verticalScroll(rememberScrollState()),
        ) {
            Text("Two-factor authentication", style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(8.dp))
            Text(
                "Protect your password login with a code from an authenticator app.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(24.dp))

            when (val s = state) {
                SecurityUiState.Loading -> CircularProgressIndicator()

                is SecurityUiState.Disabled -> {
                    s.error?.let { ErrorText(it) }
                    Button(onClick = vm::startEnrol, enabled = !busy) {
                        if (busy) {
                            CircularProgressIndicator(
                                modifier = Modifier.size(18.dp),
                                strokeWidth = 2.dp,
                            )
                            Spacer(Modifier.width(8.dp))
                        }
                        Text("Enable 2FA")
                    }
                }

                is SecurityUiState.Enrolling -> EnrollBody(
                    secret = s.secret,
                    qrPngBase64 = s.qrPngBase64,
                    error = s.error,
                    busy = busy,
                    onConfirm = vm::confirm,
                    onCancel = vm::finishEnrol,
                )

                is SecurityUiState.Recovery -> RecoveryBody(
                    codes = s.codes,
                    onDone = vm::finishEnrol,
                )

                is SecurityUiState.Enabled -> EnabledBody(
                    recoveryRemaining = s.recoveryRemaining,
                    error = s.error,
                    busy = busy,
                    onDisable = vm::disable,
                )
            }
        }
    }
}

@Composable
private fun EnrollBody(
    secret: String,
    qrPngBase64: String?,
    error: String?,
    busy: Boolean,
    onConfirm: (String) -> Unit,
    onCancel: () -> Unit,
) {
    var code by rememberSaveable { mutableStateOf("") }

    Text(
        "1. Scan this QR in your authenticator app, or enter the key manually.",
        style = MaterialTheme.typography.bodyMedium,
    )
    Spacer(Modifier.height(12.dp))

    val qr = remember(qrPngBase64) {
        qrPngBase64?.let {
            runCatching {
                val bytes = Base64.decode(it, Base64.DEFAULT)
                BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap()
            }.getOrNull()
        }
    }
    if (qr != null) {
        Image(bitmap = qr, contentDescription = "2FA QR code", modifier = Modifier.size(220.dp))
        Spacer(Modifier.height(12.dp))
    }
    Text(secret, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodyLarge)

    Spacer(Modifier.height(20.dp))
    Text("2. Enter the 6-digit code to confirm.", style = MaterialTheme.typography.bodyMedium)
    Spacer(Modifier.height(8.dp))
    OutlinedTextField(
        value = code,
        onValueChange = { code = it },
        singleLine = true,
        label = { Text("Code") },
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.NumberPassword,
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(onDone = { if (code.isNotBlank()) onConfirm(code) }),
    )
    error?.let { Spacer(Modifier.height(8.dp)); ErrorText(it) }
    Spacer(Modifier.height(12.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Button(
            onClick = { if (code.isNotBlank()) onConfirm(code) },
            enabled = !busy && code.isNotBlank(),
        ) {
            if (busy) {
                CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(8.dp))
            }
            Text("Confirm")
        }
        OutlinedButton(onClick = onCancel, enabled = !busy) { Text("Cancel") }
    }
}

@Composable
private fun RecoveryBody(codes: List<String>, onDone: () -> Unit) {
    val context = LocalView.current.context
    var copied by remember { mutableStateOf(false) }

    Text(
        "2FA is on. Save these recovery codes — each works once and they " +
            "won't be shown again.",
        style = MaterialTheme.typography.bodyMedium,
    )
    Spacer(Modifier.height(16.dp))
    codes.forEach { c ->
        Text(c, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.height(4.dp))
    }
    Spacer(Modifier.height(20.dp))
    // Give them a way to save these that isn't a screenshot. Without a copy
    // affordance the natural response to "save these" is a screen capture into
    // shared media storage, where any app holding READ_MEDIA_IMAGES can read
    // bypass codes for the account. EXTRA_IS_SENSITIVE keeps the values out of
    // the clipboard preview toast on Android 13+ and hints to the OS not to
    // sync them.
    OutlinedButton(
        onClick = {
            val clip = ClipData.newPlainText("OnScreen recovery codes", codes.joinToString("\n"))
            clip.description.extras = PersistableBundle().apply {
                putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true)
            }
            (context.getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager)
                ?.setPrimaryClip(clip)
            copied = true
        },
    ) { Text(if (copied) "Copied" else "Copy codes") }
    Spacer(Modifier.height(12.dp))
    Button(onClick = onDone) { Text("I've saved them") }
}

@Composable
private fun EnabledBody(
    recoveryRemaining: Int,
    error: String?,
    busy: Boolean,
    onDisable: (String) -> Unit,
) {
    var code by rememberSaveable { mutableStateOf("") }

    Text(
        "Two-factor authentication is on. $recoveryRemaining recovery " +
            (if (recoveryRemaining == 1) "code" else "codes") + " remaining.",
        style = MaterialTheme.typography.bodyMedium,
    )
    Spacer(Modifier.height(20.dp))
    OutlinedTextField(
        value = code,
        onValueChange = { code = it },
        singleLine = true,
        label = { Text("Code to disable") },
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.NumberPassword,
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(onDone = { if (code.isNotBlank()) onDisable(code) }),
        modifier = Modifier.fillMaxWidth(),
    )
    Text(
        "Enter a current authenticator code or a recovery code to turn 2FA off.",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 4.dp),
    )
    error?.let { Spacer(Modifier.height(8.dp)); ErrorText(it) }
    Spacer(Modifier.height(12.dp))
    Button(
        onClick = { if (code.isNotBlank()) onDisable(code) },
        enabled = !busy && code.isNotBlank(),
    ) {
        if (busy) {
            CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
            Spacer(Modifier.width(8.dp))
        }
        Text("Disable 2FA")
    }
}

@Composable
private fun ErrorText(msg: String) {
    Text(msg, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
    Spacer(Modifier.height(8.dp))
}
