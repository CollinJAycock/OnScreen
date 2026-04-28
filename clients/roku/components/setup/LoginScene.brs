' LoginScene controller. POST /api/v1/auth/login with the entered
' username + password; on a successful TokenPair, persist via Prefs
' and route to HomeScene. Errors render inline in red.

sub init()
    m.usernameField = m.top.findNode("usernameField")
    m.passwordField = m.top.findNode("passwordField")
    m.signInBtn = m.top.findNode("signInBtn")
    m.changeServerBtn = m.top.findNode("changeServerBtn")
    m.error = m.top.findNode("error")

    m.signInBtn.observeField("buttonSelected", "onSignInPressed")
    m.changeServerBtn.observeField("buttonSelected", "onChangeServerPressed")
    m.usernameField.setFocus(true)
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

    Prefs_SetTokens(pair["access_token"], pair["refresh_token"])
    Prefs_Set(PrefsKeyUsername(), pair["username"])

    getMainScene().callFunc("navigateTo", "HomeScene")
end sub

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
