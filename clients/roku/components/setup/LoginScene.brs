' LoginScene controller. POST /api/v1/auth/login with the entered
' username + password; on a successful TokenPair, persist via Prefs
' and route to HomeScene. Errors render inline in red.
'
' Layout for a remote: a column of buttons (Sign in with another device
' first, then Username, Password, Sign in, Change server) and one Keyboard
' under them that shows only while a field is being edited. A field button
' shows its value (the password as dots). Up / Down move between buttons;
' Back or Down at the keyboard's bottom edge closes it; Back on the buttons
' is left to MainScene, which exits from this root screen.
'
' Requests run in an ApiCallTask (m.request): roUrlTransfer can't be
' created on the render thread.

sub init()
    m.pairBtn = m.top.findNode("pairBtn")
    m.usernameBtn = m.top.findNode("usernameBtn")
    m.passwordBtn = m.top.findNode("passwordBtn")
    m.signInBtn = m.top.findNode("signInBtn")
    m.totpBtn = m.top.findNode("totpBtn")
    m.verifyBtn = m.top.findNode("verifyBtn")
    m.changeServerBtn = m.top.findNode("changeServerBtn")
    m.orLabel = m.top.findNode("orLabel")
    m.keyboard = m.top.findNode("keyboard")
    m.error = m.top.findNode("error")

    m.values = { username: "", password: "", totp: "" }
    ' The field the keyboard is editing ("" = none).
    m.editing = ""
    m.challengeToken = ""

    m.pairBtn.observeField("buttonSelected", "onPairPressed")
    m.usernameBtn.observeField("buttonSelected", "onUsernamePressed")
    m.passwordBtn.observeField("buttonSelected", "onPasswordPressed")
    m.signInBtn.observeField("buttonSelected", "onSignInPressed")
    m.totpBtn.observeField("buttonSelected", "onTotpPressed")
    m.verifyBtn.observeField("buttonSelected", "onVerifyPressed")
    m.changeServerBtn.observeField("buttonSelected", "onChangeServerPressed")
    m.keyboard.observeField("text", "onKeyboardText")

    m.rows = [m.pairBtn, m.usernameBtn, m.passwordBtn, m.signInBtn, m.changeServerBtn]
    updateFieldLabels()
    m.pairBtn.setFocus(true)
    m.top.initialFocus = m.pairBtn
end sub

sub onPairPressed()
    getMainScene().callFunc("navigateTo", "PairScene")
end sub

sub onUsernamePressed()
    startEdit("username")
end sub

sub onPasswordPressed()
    startEdit("password")
end sub

sub onTotpPressed()
    startEdit("totp")
end sub

' Show the keyboard for one field, holding that field's current value.
sub startEdit(field as String)
    m.editing = field
    m.keyboard.textEditBox.secureMode = (field = "password")
    m.keyboard.text = m.values[field]
    m.keyboard.visible = true
    m.keyboard.setFocus(true)
end sub

' Close the keyboard and give the focus back to the field's button.
sub finishEdit()
    field = m.editing
    m.editing = ""
    m.keyboard.visible = false
    btn = fieldButton(field)
    if btn <> invalid then btn.setFocus(true)
end sub

function fieldButton(field as String) as Dynamic
    if field = "username" then return m.usernameBtn
    if field = "password" then return m.passwordBtn
    if field = "totp" then return m.totpBtn
    return invalid
end function

sub onKeyboardText()
    if m.editing = "" then return
    t = m.keyboard.text
    if not Prefs_IsString(t) then t = ""
    m.values[m.editing] = t
    updateFieldLabels()
end sub

sub updateFieldLabels()
    m.usernameBtn.text = "Username: " + fieldDisplay(m.values.username, false)
    m.passwordBtn.text = "Password: " + fieldDisplay(m.values.password, true)
    m.totpBtn.text = "Code: " + fieldDisplay(m.values.totp, false)
end sub

function fieldDisplay(v as String, secret as Boolean) as String
    if v = "" then return "(not set)"
    if secret then return String(Len(v), "•")
    return v
end function

function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if m.editing <> ""
        ' The keyboard keeps the presses it uses; what reaches here is Back,
        ' or a move off one of its edges.
        if key = "back" or key = "down" or key = "up"
            finishEdit()
            return true
        end if
        return false
    end if
    if key <> "up" and key <> "down" then return false
    i = focusedRow()
    if i < 0 then return false
    if key = "down" then i = i + 1 else i = i - 1
    if i < 0 or i >= m.rows.Count() then return true
    m.rows[i].setFocus(true)
    return true
end function

function focusedRow() as Integer
    for i = 0 to m.rows.Count() - 1
        if m.rows[i].hasFocus() then return i
    end for
    return -1
end function

sub onSignInPressed()
    username = m.values.username
    password = m.values.password
    if username = "" or password = ""
        showError("Enter both a username and a password")
        return
    end if

    if m.request <> invalid then return
    m.signInBtn.text = "Signing in..."
    m.error.visible = false
    startRequest("login", ApiAuthLogin(), { username: username, password: password })
end sub

' POST path with body on an ApiCallTask; onRequestDone gets the result.
sub startRequest(kind as String, path as String, body as Object)
    m.request = createObject("roSGNode", "ApiCallTask")
    m.request.method = "POST"
    m.request.path = path
    m.request.body = body
    m.request.auth = false
    m.request.context = { kind: kind }
    m.request.observeField("state", "onRequestDone")
    m.request.control = "RUN"
end sub

sub onRequestDone()
    if m.request = invalid or m.request.state <> "done" then return
    res = m.request.result
    kind = m.request.context.kind
    m.request = invalid
    if kind = "login"
        m.signInBtn.text = "Sign in"
        onLoginResult(res)
    else
        m.verifyBtn.text = "Verify"
        onVerifyResult(res)
    end if
end sub

' The message for a failed sign-in step: the server's own message when it
' sent one, else what the status says.
function requestError(res as Dynamic, fallback as String) as String
    if res = invalid or res.code <= 0 then return "Couldn't reach the server — check the URL and your network"
    if res.code = 429 then return "Too many attempts — wait a minute and try again"
    if Prefs_IsNonEmptyString(res.errorMessage) and res.code <> 401 then return res.errorMessage
    return fallback
end function

sub onLoginResult(res as Dynamic)
    pair = invalid
    if res <> invalid and res.ok = true then pair = res.data
    if pair = invalid or type(pair) <> "roAssociativeArray"
        showError(requestError(res, "Login failed — check your username and password"))
        return
    end if

    ' Password OK but a second factor is owed — switch to the TOTP step.
    if pair["totp_required"] = true
        challenge = pair["login_challenge_token"]
        if challenge = invalid then challenge = ""
        m.challengeToken = challenge
        enterTotpMode()
        return
    end if

    if not persistAndGoHome(pair)
        showError("Login failed — the server sent an incomplete response")
    end if
end sub

' Password OK but a second factor is owed: swap the password rows for the
' code entry and open the keyboard on it.
sub enterTotpMode()
    m.error.visible = false
    m.orLabel.visible = false
    m.pairBtn.visible = false
    m.usernameBtn.visible = false
    m.passwordBtn.visible = false
    m.signInBtn.visible = false
    m.totpBtn.visible = true
    m.verifyBtn.visible = true
    m.rows = [m.totpBtn, m.verifyBtn, m.changeServerBtn]
    m.top.initialFocus = m.totpBtn
    startEdit("totp")
end sub

sub onVerifyPressed()
    code = m.values.totp
    if code = ""
        showError("Enter your authentication code")
        return
    end if

    if m.request <> invalid then return
    m.verifyBtn.text = "Verifying..."
    m.error.visible = false
    startRequest("totp", ApiTotpVerify(), { login_challenge_token: m.challengeToken, code: code })
end sub

sub onVerifyResult(res as Dynamic)
    pair = invalid
    if res <> invalid and res.ok = true then pair = res.data
    if pair = invalid or type(pair) <> "roAssociativeArray" or not Prefs_IsNonEmptyString(pair["access_token"])
        showError(requestError(res, "That code didn't match — try again"))
        return
    end if

    if not persistAndGoHome(pair)
        showError("Verification failed — the server sent an incomplete response")
    end if
end sub

' Persist the token pair and route Home. Guards every value before it
' crosses into Prefs (whose setters declare `as String`): a partial
' response with an invalid access_token / refresh_token would be a
' Type Mismatch crash, not a clean failure. Returns false (without
' navigating) when the required tokens are missing so the caller can
' surface an error; the username is optional and coerced to "".
function persistAndGoHome(pair as Object) as Boolean
    access = pair["access_token"]
    refresh = pair["refresh_token"]
    if not Prefs_IsNonEmptyString(access) or not Prefs_IsNonEmptyString(refresh) then return false

    assetTok = pair["asset_token"]
    if not Prefs_IsString(assetTok) then assetTok = ""
    username = pair["username"]
    if not Prefs_IsString(username) then username = ""

    Prefs_SetTokens(access, refresh, assetTok)
    Prefs_Set(PrefsKeyUsername(), username)

    getMainScene().callFunc("navigateTo", "HomeScene")
    return true
end function

sub onChangeServerPressed()
    ' Revoke any leftover session on the CURRENT server before its URL is
    ' forgotten (no-op when no refresh token is stored). The revoke needs
    ' the URL and token, so it finishes on its Task before they're cleared.
    if m.logout <> invalid then return
    if not Prefs_IsNonEmptyString(Prefs_Get(PrefsKeyRefreshToken()))
        forgetServer()
        return
    end if
    m.changeServerBtn.text = "Signing out..."
    m.logout = createObject("roSGNode", "LogoutTask")
    m.logout.observeField("state", "onLogoutDone")
    m.logout.control = "RUN"
end sub

sub onLogoutDone()
    if m.logout = invalid or m.logout.state <> "done" then return
    m.logout = invalid
    forgetServer()
end sub

sub forgetServer()
    Prefs_ClearAuth()
    Prefs_Delete(PrefsKeyServerUrl())
    getMainScene().callFunc("navigateTo", "ServerSetupScene")
end sub

sub showError(msg as String)
    m.error.text = msg
    m.error.visible = true
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
