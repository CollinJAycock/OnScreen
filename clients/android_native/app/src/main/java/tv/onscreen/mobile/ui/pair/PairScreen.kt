package tv.onscreen.mobile.ui.pair

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel

@Composable
fun PairScreen(
    onPaired: () -> Unit,
    vm: PairViewModel = hiltViewModel(),
) {
    val state by vm.state.collectAsState()
    val context = LocalContext.current

    LaunchedEffect(state) {
        if (state is PairState.Done) onPaired()
    }
    // SSO bridge: when the VM has a Custom-Tabs URL queued, launch it
    // and immediately ack so a configuration change doesn't re-open
    // the browser. The polling started by startSsoBridge runs in
    // parallel and resolves the auth via the existing PairState.Done
    // path above.
    val ssoUrl by vm.ssoLaunchUrl.collectAsState()
    LaunchedEffect(ssoUrl) {
        ssoUrl?.let { url ->
            SsoLauncher.launch(context, url)
            vm.consumeSsoLaunchUrl()
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text("OnScreen", style = MaterialTheme.typography.headlineLarge)
        Spacer(Modifier.height(32.dp))

        when (val s = state) {
            PairState.NeedsServer, PairState.ServerUnreachable, is PairState.Error ->
                ServerEntry(
                    error = (s as? PairState.Error)?.message
                        ?: ("server unreachable".takeIf { s is PairState.ServerUnreachable }),
                    onSubmit = vm::submitServerUrl,
                )

            PairState.CheckingServer -> Loading("Checking server…")

            PairState.ServerReady -> {
                val providers by vm.providers.collectAsState()
                ServerReadyChoice(
                    providers = providers,
                    onPair = vm::startPairing,
                    onPasswordLogin = vm::loginWithPassword,
                    onLdapLogin = vm::loginWithLdap,
                    onSsoBridge = { vm.startSsoBridge() },
                )
            }

            PairState.RequestingCode -> Loading("Requesting pairing code…")

            is PairState.WaitingForClaim -> WaitingForClaim(code = s.code, onCancel = vm::reset)

            PairState.LoggingIn -> Loading("Signing in…")

            PairState.Done -> Loading("Done")
        }
    }
}

@Composable
private fun Loading(label: String) {
    CircularProgressIndicator()
    Spacer(Modifier.height(16.dp))
    Text(label, style = MaterialTheme.typography.bodyLarge)
}

@Composable
private fun ServerEntry(error: String?, onSubmit: (String) -> Unit) {
    var url by remember { mutableStateOf("") }
    Text("Connect to your server", style = MaterialTheme.typography.titleLarge)
    Spacer(Modifier.height(16.dp))
    OutlinedTextField(
        value = url,
        onValueChange = { url = it },
        singleLine = true,
        label = { Text("Server URL") },
        placeholder = { Text("https://onscreen.example.com") },
        modifier = Modifier.widthIn(max = 360.dp),
    )
    Spacer(Modifier.height(16.dp))
    Button(onClick = { onSubmit(url) }) { Text("Continue") }
    if (error != null) {
        Spacer(Modifier.height(12.dp))
        Text(
            error,
            color = MaterialTheme.colorScheme.error,
            style = MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
private fun ServerReadyChoice(
    providers: tv.onscreen.mobile.data.model.AuthProviders?,
    onPair: () -> Unit,
    onPasswordLogin: (String, String) -> Unit,
    onLdapLogin: (String, String) -> Unit,
    onSsoBridge: () -> Unit,
) {
    Text("Sign in", style = MaterialTheme.typography.titleLarge)
    Spacer(Modifier.height(16.dp))
    Button(onClick = onPair) { Text("Pair this phone") }
    // SSO bridge — when the server has OIDC or SAML enabled, offer
    // a one-tap Custom Tabs flow. Behind the scenes: request a pair
    // PIN, open the web /pair page in a Custom Tabs window with the
    // PIN pre-filled and auto=1, the user signs in via the IdP in
    // the same browser tab, the web page auto-claims, and the app's
    // poll-loop receives the token pair. No deep-link callback,
    // no token-in-URL leak.
    if (providers?.needsBrowserPairing == true) {
        Spacer(Modifier.height(8.dp))
        Button(onClick = onSsoBridge) {
            val ssoLabel = listOfNotNull(
                providers.oidc?.takeIf { it.enabled }?.display_name,
                providers.saml?.takeIf { it.enabled }?.display_name,
            ).joinToString(" / ").ifEmpty { "SSO" }
            Text("Sign in with $ssoLabel")
        }
        Spacer(Modifier.height(4.dp))
        Text(
            "Opens an in-app browser tab to your server's web sign-in. " +
                "The phone signs in automatically when you finish.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.widthIn(max = 360.dp),
        )
    }
    Spacer(Modifier.height(8.dp))
    Text("— or —", style = MaterialTheme.typography.bodySmall)
    Spacer(Modifier.height(8.dp))

    // useLdap toggle only renders when the server reports LDAP
    // enabled. Default: local login. Toggling routes the same
    // username/password fields to the LDAP endpoint instead.
    var useLdap by remember { mutableStateOf(false) }
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }

    if (providers?.ldapEnabled == true) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            TextButton(onClick = { useLdap = false }) {
                Text(
                    "Local",
                    color = if (!useLdap) MaterialTheme.colorScheme.primary
                        else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            TextButton(onClick = { useLdap = true }) {
                Text(
                    providers.ldap?.display_name?.takeIf { it.isNotBlank() } ?: "LDAP",
                    color = if (useLdap) MaterialTheme.colorScheme.primary
                        else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }

    OutlinedTextField(
        value = username,
        onValueChange = { username = it },
        singleLine = true,
        label = { Text("Username") },
        modifier = Modifier.widthIn(max = 360.dp),
    )
    Spacer(Modifier.height(8.dp))
    OutlinedTextField(
        value = password,
        onValueChange = { password = it },
        singleLine = true,
        label = { Text("Password") },
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
        modifier = Modifier.widthIn(max = 360.dp),
    )
    Spacer(Modifier.height(12.dp))
    TextButton(onClick = {
        if (useLdap) onLdapLogin(username, password) else onPasswordLogin(username, password)
    }) {
        Text(if (useLdap) "Sign in with LDAP" else "Sign in with password")
    }
}

@Composable
private fun WaitingForClaim(code: String, onCancel: () -> Unit) {
    Text("Open /pair on a laptop", style = MaterialTheme.typography.titleLarge)
    Spacer(Modifier.height(16.dp))
    Text(
        code,
        style = MaterialTheme.typography.displayMedium.copy(fontFamily = FontFamily.Monospace),
    )
    Spacer(Modifier.height(8.dp))
    Text(
        "Sign in to your server in a browser, then enter this code.",
        style = MaterialTheme.typography.bodyMedium,
    )
    Spacer(Modifier.height(24.dp))
    TextButton(onClick = onCancel) { Text("Cancel") }
}
