' PairScene controller. Drives the device-pairing UI and consumes
' the PairTask's emitted events to update the PIN / status / done
' transitions.

sub init()
    m.urlLabel = m.top.findNode("url")
    m.pin = m.top.findNode("pin")
    m.status = m.top.findNode("status")
    m.cancelBtn = m.top.findNode("cancelBtn")
    m.task = m.top.findNode("pairTask")

    m.task.observeField("phase", "onPhaseChanged")
    m.task.observeField("pin", "onPinChanged")
    m.task.observeField("done", "onDone")
    m.cancelBtn.observeField("buttonSelected", "onCancel")

    serverUrl = Prefs_GetServerUrl()
    if serverUrl <> invalid then m.urlLabel.text = serverUrl + "/pair"

    m.task.control = "RUN"
    m.cancelBtn.setFocus(true)
end sub

sub onPhaseChanged()
    phase = m.task.phase
    if phase = "creating"
        m.status.text = "Generating code…"
    else if phase = "waiting"
        m.status.text = "Waiting for sign-in…"
    else if phase = "expired_recycling"
        m.status.text = "Code expired — refreshing…"
    else if phase = "failed"
        msg = m.task.failureReason
        if msg = invalid or msg = "" then msg = "Couldn't reach server"
        m.status.text = msg + " — retrying…"
    end if
end sub

sub onPinChanged()
    if m.task.pin <> invalid and m.task.pin <> "" then m.pin.text = m.task.pin
end sub

sub onDone()
    if m.task.done <> true then return
    ' PairTask persists tokens itself before flipping `done` so the
    ' main thread doesn't have to coordinate prefs writes against
    ' the task's lifecycle. All we do here is route to home.
    getMainScene().callFunc("navigateTo", "HomeScene")
end sub

sub onCancel()
    ' Stop the task so its poll loop doesn't keep firing after we
    ' leave the scene. Then fall back to the password login flow.
    m.task.control = "STOP"
    getMainScene().callFunc("navigateTo", "LoginScene")
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
