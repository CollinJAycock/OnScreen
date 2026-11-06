' ServerSetupScene controller. Wires the on-screen keyboard +
' Connect button to a /health/live probe and, on success, persists
' the URL and hands off to the LoginScene via MainScene's router.
'
' The probe runs in an ApiCallTask: roUrlTransfer can't be created on the
' render thread (CreateObject returns invalid there), so a synchronous
' probe from this scene crashed the channel on the first Connect.

sub init()
    m.keyboard = m.top.findNode("keyboard")
    m.connectBtn = m.top.findNode("connectBtn")
    m.error = m.top.findNode("error")
    ' URL the user has explicitly agreed to use over cleartext http://
    ' to a non-local host (second Connect press on the same URL).
    m.insecureConfirmed = ""

    m.connectBtn.observeField("buttonSelected", "onConnectPressed")
    m.keyboard.setFocus(true)
    m.top.initialFocus = m.keyboard
end sub

' Down from the keyboard reaches Connect and Up goes back: the Keyboard node
' leaves those presses at its edges to its parent, and nothing else moves
' the focus between the two.
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "down" and m.keyboard.isInFocusChain()
        m.connectBtn.setFocus(true)
        return true
    end if
    if key = "up" and m.connectBtn.hasFocus()
        m.keyboard.setFocus(true)
        return true
    end if
    return false
end function

sub onConnectPressed()
    url = m.keyboard.text
    if url = invalid then url = ""
    url = StringStripTrailingSlash(StringTrim(url))
    if url = ""
        showError("Server URL can't be empty")
        return
    end if
    if not (Left(url, 7) = "http://" or Left(url, 8) = "https://")
        showError("URL must start with http:// or https://")
        return
    end if
    ' Plain http:// on the LAN is the common self-hosted setup and stays
    ' allowed. Plain http:// to a public host would send the password,
    ' refresh token and pairing token across the internet in cleartext,
    ' so require a deliberate second Connect press.
    if Left(url, 7) = "http://" and not StringIsLocalNetworkHost(StringUrlHost(url)) and m.insecureConfirmed <> url
        m.insecureConfirmed = url
        showError("Not encrypted: " + url + " is not on your local network, so your password and sign-in tokens would be sent in plain text. Use https://, or press Connect again to continue anyway.")
        return
    end if

    ' Persist before probing so Client.brs can read the URL out of
    ' Prefs (BuildTransfer reads from there, not from a parameter).
    ' If the probe fails we don't roll back — the user will retry
    ' Connect and overwrite, which is the simpler UX.
    Prefs_Set(PrefsKeyServerUrl(), url)

    ' One probe at a time (a second Connect press while one is out).
    if m.probe <> invalid then return
    m.connectBtn.text = "Connecting..."
    m.error.visible = false
    ' GET /health/live and accept any 2xx. The endpoint is public: no auth.
    m.probe = createObject("roSGNode", "ApiCallTask")
    m.probe.path = "/health/live"
    m.probe.auth = false
    m.probe.context = { url: url }
    m.probe.observeField("state", "onProbeDone")
    m.probe.control = "RUN"
end sub

sub onProbeDone()
    if m.probe = invalid or m.probe.state <> "done" then return
    res = m.probe.result
    url = m.probe.context.url
    m.probe = invalid
    m.connectBtn.text = "Connect"
    if res <> invalid and res.ok = true
        getMainScene().callFunc("navigateTo", "LoginScene")
    else
        showError("Couldn't reach " + url + " — check the URL and that OnScreen is running")
    end if
end sub

sub showError(msg as String)
    m.error.text = msg
    m.error.visible = true
end sub

' Walk up the parent chain to the root MainScene so we can call
' navigateTo() on it. SceneGraph doesn't expose `getRoot()` directly;
' walking parents is the documented pattern.
function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function

' String helpers (StringTrim, StringStripTrailingSlash) live in
' source/util/Strings.brs so they're testable standalone via brs.
