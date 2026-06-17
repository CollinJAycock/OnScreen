package tv.onscreen.mobile.ui.pair

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel

@Composable
fun PairScreen(
    onPaired: () -> Unit,
    vm: PairViewModel = hiltViewModel(),
) {
    val state by vm.state.collectAsStateWithLifecycle()
    val context = LocalContext.current

    // Credential field state is hoisted here (rather than inside
    // ServerReadyChoice) so it survives the brief LoggingIn detour: a
    // failed login flips ServerReady -> LoggingIn -> ServerReady, which
    // tears down and rebuilds the choice screen. Holding the fields in
    // the always-present root keeps the user's typed username/password
    // intact so they only fix the wrong field, never retype everything.
    var useLdap by rememberSaveable { mutableStateOf(false) }
    var username by rememberSaveable { mutableStateOf("") }
    var password by rememberSaveable { mutableStateOf("") }
    var passwordVisible by rememberSaveable { mutableStateOf(false) }

    LaunchedEffect(state) {
        if (state is PairState.Done) onPaired()
    }
    // SSO bridge: when the VM has a Custom-Tabs URL queued, launch it
    // and immediately ack so a configuration change doesn't re-open
    // the browser. The polling started by startSsoBridge runs in
    // parallel and resolves the auth via the existing PairState.Done
    // path above.
    val ssoUrl by vm.ssoLaunchUrl.collectAsStateWithLifecycle()
    LaunchedEffect(ssoUrl) {
        ssoUrl?.let { url ->
            SsoLauncher.launch(context, url)
            vm.consumeSsoLaunchUrl()
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            // No Scaffold here, so apply the insets ourselves: keep content
            // clear of the status/nav bars and lift it above the IME so the
            // text fields stay visible while typing (the app is edge-to-edge).
            .systemBarsPadding()
            .imePadding()
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

            is PairState.ServerReady -> {
                val providers by vm.providers.collectAsStateWithLifecycle()
                ServerReadyChoice(
                    providers = providers,
                    loginError = s.loginError,
                    useLdap = useLdap,
                    onUseLdapChange = { useLdap = it },
                    username = username,
                    onUsernameChange = { username = it },
                    password = password,
                    onPasswordChange = { password = it },
                    passwordVisible = passwordVisible,
                    onPasswordVisibleChange = { passwordVisible = it },
                    onPair = vm::startPairing,
                    onPasswordLogin = vm::loginWithPassword,
                    onLdapLogin = vm::loginWithLdap,
                    onSsoBridge = { vm.startSsoBridge() },
                )
            }

            PairState.RequestingCode -> Loading("Requesting pairing code…")

            is PairState.WaitingForClaim -> WaitingForClaim(code = s.code, onCancel = vm::reset)

            is PairState.TotpRequired -> TotpEntry(
                error = s.error,
                onSubmit = { code -> vm.verifyTotp(s.challengeToken, code) },
                onCancel = vm::reset,
            )

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
    var url by rememberSaveable { mutableStateOf("") }
    Text("Connect to your server", style = MaterialTheme.typography.titleLarge)
    Spacer(Modifier.height(16.dp))
    OutlinedTextField(
        value = url,
        onValueChange = { url = it },
        singleLine = true,
        label = { Text("Server address") },
        placeholder = { Text("myserver.com  or  192.168.1.50:7070") },
        supportingText = {
            // The viewmodel normalises bare hosts to http://<lan> or
            // https://<public> automatically, so users typing a raw
            // IP or hostname don't have to pick a scheme themselves.
            Text("No need to type http:// or https:// — we'll pick the right one.")
        },
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.Uri,
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(onDone = { onSubmit(url) }),
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
    loginError: String?,
    useLdap: Boolean,
    onUseLdapChange: (Boolean) -> Unit,
    username: String,
    onUsernameChange: (String) -> Unit,
    password: String,
    onPasswordChange: (String) -> Unit,
    passwordVisible: Boolean,
    onPasswordVisibleChange: (Boolean) -> Unit,
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
    val submit = {
        if (useLdap) onLdapLogin(username, password) else onPasswordLogin(username, password)
    }

    if (providers?.ldapEnabled == true) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            TextButton(onClick = { onUseLdapChange(false) }) {
                Text(
                    "Local",
                    color = if (!useLdap) MaterialTheme.colorScheme.primary
                        else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            TextButton(onClick = { onUseLdapChange(true) }) {
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
        onValueChange = onUsernameChange,
        singleLine = true,
        label = { Text("Username") },
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
        modifier = Modifier.widthIn(max = 360.dp),
    )
    Spacer(Modifier.height(8.dp))
    OutlinedTextField(
        value = password,
        onValueChange = onPasswordChange,
        singleLine = true,
        label = { Text("Password") },
        visualTransformation = if (passwordVisible) VisualTransformation.None
            else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.Password,
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(onDone = { submit() }),
        trailingIcon = {
            IconButton(onClick = { onPasswordVisibleChange(!passwordVisible) }) {
                Icon(
                    imageVector = if (passwordVisible) Icons.Filled.Visibility
                        else Icons.Filled.VisibilityOff,
                    contentDescription = if (passwordVisible) "Hide password" else "Show password",
                )
            }
        },
        modifier = Modifier.widthIn(max = 360.dp),
    )
    if (loginError != null) {
        Spacer(Modifier.height(8.dp))
        Text(
            loginError,
            color = MaterialTheme.colorScheme.error,
            style = MaterialTheme.typography.bodySmall,
            modifier = Modifier.widthIn(max = 360.dp),
        )
    }
    Spacer(Modifier.height(12.dp))
    TextButton(onClick = { submit() }) {
        Text(if (useLdap) "Sign in with LDAP" else "Sign in with password")
    }
}

@Composable
private fun TotpEntry(error: String?, onSubmit: (String) -> Unit, onCancel: () -> Unit) {
    var code by rememberSaveable { mutableStateOf("") }
    Text("Two-factor authentication", style = MaterialTheme.typography.titleLarge)
    Spacer(Modifier.height(8.dp))
    Text(
        "Enter the 6-digit code from your authenticator app, or a recovery code.",
        style = MaterialTheme.typography.bodyMedium,
    )
    Spacer(Modifier.height(16.dp))
    OutlinedTextField(
        value = code,
        onValueChange = { code = it },
        singleLine = true,
        label = { Text("Code") },
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.NumberPassword,
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(onDone = { if (code.isNotBlank()) onSubmit(code.trim()) }),
        modifier = Modifier.widthIn(max = 360.dp),
    )
    if (error != null) {
        Spacer(Modifier.height(8.dp))
        Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
    }
    Spacer(Modifier.height(12.dp))
    TextButton(onClick = { if (code.isNotBlank()) onSubmit(code.trim()) }) { Text("Verify") }
    TextButton(onClick = onCancel) { Text("Back") }
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
