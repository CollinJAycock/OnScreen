' LoginScene controller. POST /api/v1/auth/login with the entered
' username + password; on a successful TokenPair, persist via Prefs
' and route to HomeScene. Errors render inline in red.

sub init()
    m.usernameField = m.top.findNode("usernameField")
    m.usernameLabel = m.top.findNode("usernameLabel")
    m.passwordField = m.top.findNode("passwordField")
    m.passwordLabel = m.top.findNode("passwordLabel")
    m.signInBtn = m.top.findNode("signInBtn")
    m.changeServerBtn = m.top.findNode("changeServerBtn")
    m.pairBtn = m.top.findNode("pairBtn")
    m.error = m.top.findNode("error")
    m.totpLabel = m.top.findNode("totpLabel")
    m.totpField = m.top.findNode("totpField")
    m.verifyBtn = m.top.findNode("verifyBtn")
    m.challengeToken = ""

    m.signInBtn.observeField("buttonSelected", "onSignInPressed")
    m.changeServerBtn.observeField("buttonSelected", "onChangeServerPressed")
    m.pairBtn.observeField("buttonSelected", "onPairPressed")
    m.verifyBtn.observeField("buttonSelected", "onVerifyPressed")
    m.usernameField.setFocus(true)
end sub

sub onPairPressed()
    getMainScene().callFunc("navigateTo", "PairScene")
end sub

sub onSignInPressed()
    username = m.usernameField.text
    password = m.passwordField.text
    if username = invalid then username = ""
    if password = invalid then password = ""
    if username = "" or password = ""
        showError("Enter both a username and a password")
        return
    end if

    m.signInBtn.text = "Signing in..."
    pair = Client_PostSync(ApiAuthLogin(), {
        username: username
        password: password
    }, false)
    m.signInBtn.text = "Sign in"

    if pair = invalid
        showError("Login failed — check your username, password, and server URL")
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

' Hide the username/password rows and reveal the code entry.
sub enterTotpMode()
    m.error.visible = false
    m.usernameLabel.visible = false
    m.usernameField.visible = false
    m.passwordLabel.visible = false
    m.passwordField.visible = false
    m.signInBtn.visible = false
    m.totpLabel.visible = true
    m.totpField.visible = true
    m.verifyBtn.visible = true
    m.totpField.setFocus(true)
end sub

sub onVerifyPressed()
    code = m.totpField.text
    if code = invalid then code = ""
    if code = ""
        showError("Enter your authentication code")
        return
    end if

    m.verifyBtn.text = "Verifying..."
    pair = Client_PostSync(ApiTotpVerify(), {
        login_challenge_token: m.challengeToken
        code: code
    }, false)
    m.verifyBtn.text = "Verify"

    if pair = invalid or pair["access_token"] = invalid or pair["access_token"] = ""
        showError("That code didn't match — try again")
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
    Prefs_Delete(PrefsKeyServerUrl())
    Prefs_ClearAuth()
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
