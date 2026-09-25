' ServerSetupScene controller. Wires the on-screen keyboard +
' Connect button to a /health/live probe and, on success, persists
' the URL and hands off to the LoginScene via MainScene's router.
'
' The probe is intentionally synchronous here — the user pressed
' Connect and is staring at a "Connecting..." button; blocking the
' UI for a sub-second HTTP call beats the complexity of running it
' in a Task node for this single-shot.

sub init()
    m.keyboard = m.top.findNode("keyboard")
    m.connectBtn = m.top.findNode("connectBtn")
    m.error = m.top.findNode("error")
    ' URL the user has explicitly agreed to use over cleartext http://
    ' to a non-local host (second Connect press on the same URL).
    m.insecureConfirmed = ""

    m.connectBtn.observeField("buttonSelected", "onConnectPressed")
    m.keyboard.setFocus(true)
end sub

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

    m.connectBtn.text = "Connecting..."
    healthy = probeHealth()
    m.connectBtn.text = "Connect"

    if healthy
        getMainScene().callFunc("navigateTo", "LoginScene")
    else
        showError("Couldn't reach " + url + " — check the URL and that OnScreen is running")
    end if
end sub

' GET /health/live and accept any 2xx response. The endpoint is
' public — no auth needed — so we set auth=false.
function probeHealth() as Boolean
    transfer = Client_BuildTransfer("/health/live", false)
    if transfer = invalid then return false
    transfer.GetToString()
    code = transfer.GetResponseCode()
    return code >= 200 and code < 300
end function

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
