' See SettingsScene.xml. Sign out and Forget server revoke the session on
' its server first (LogoutTask, which also clears the tokens), then go to
' Sign in or to server setup.

sub init()
    m.signOutBtn = m.top.findNode("signOutBtn")
    m.forgetBtn = m.top.findNode("forgetBtn")
    m.logout = invalid
    m.after = ""

    user = Prefs_Get(PrefsKeyUsername())
    if Prefs_IsNonEmptyString(user)
        m.top.findNode("userLine").text = "Signed in as " + user
    else
        m.top.findNode("userLine").text = "Signed in"
    end if
    url = Prefs_GetServerUrl()
    if url = invalid then url = ""
    m.top.findNode("serverLine").text = "Server: " + url
    m.top.findNode("insecureLine").visible = (LCase(Left(url, 7)) = "http://")
    m.top.findNode("versionLine").text = "OnScreen for Roku " + CreateObject("roAppInfo").GetVersion()

    m.signOutBtn.observeField("buttonSelected", "onSignOutPressed")
    m.forgetBtn.observeField("buttonSelected", "onForgetPressed")
    m.signOutBtn.setFocus(true)
    m.top.initialFocus = m.signOutBtn
end sub

sub onSignOutPressed()
    Menu_Show("signOut", "Sign out?", "Your session on this TV ends. The server address is kept.", ["Sign out", "Cancel"])
end sub

sub onForgetPressed()
    Menu_Show("forget", "Forget this server?", "You will be signed out and asked for a server address again.", ["Forget server", "Cancel"])
end sub

sub onMenuChoice(menuId as String, index as Integer)
    if index <> 0
        if menuId = "forget" then m.forgetBtn.setFocus(true) else m.signOutBtn.setFocus(true)
        return
    end if
    if m.logout <> invalid then return
    m.after = menuId
    if menuId = "signOut" then m.signOutBtn.text = "Signing out..." else m.forgetBtn.text = "Signing out..."
    m.logout = createObject("roSGNode", "LogoutTask")
    m.logout.observeField("state", "onLogoutDone")
    m.logout.control = "RUN"
end sub

sub onLogoutDone()
    if m.logout = invalid or m.logout.state <> "done" then return
    m.logout = invalid
    ' LogoutTask cleared the tokens; Forget server also drops the address.
    Prefs_ClearAuth()
    if m.after = "forget"
        Prefs_Delete(PrefsKeyServerUrl())
        getMainScene().callFunc("navigateTo", "ServerSetupScene")
    else
        getMainScene().callFunc("navigateTo", "LoginScene")
    end if
end sub

' Left / Right between the two buttons; Back is MainScene's (to Home).
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "right" and m.signOutBtn.hasFocus()
        m.forgetBtn.setFocus(true)
        return true
    end if
    if key = "left" and m.forgetBtn.hasFocus()
        m.signOutBtn.setFocus(true)
        return true
    end if
    return false
end function

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
