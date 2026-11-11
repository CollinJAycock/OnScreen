' PairScene controller. Drives the device-pairing UI and consumes
' the PairTask's emitted events to update the PIN / status / done
' transitions.

sub init()
    m.urlLabel = m.top.findNode("url")
    m.pin = m.top.findNode("pin")
    m.status = m.top.findNode("status")
    m.cancelBtn = m.top.findNode("cancelBtn")
    m.task = m.top.findNode("pairTask")
    m.top.findNode("title").text = I18n_T("login.signIn")
    m.top.findNode("instructions").text = I18n_T("pair.openUrl")
    m.top.findNode("pinLabel").text = I18n_T("pair.enterPin")
    m.status.text = I18n_T("pair.generating")
    m.cancelBtn.text = I18n_T("pair.usePassword")

    m.task.observeField("phase", "onPhaseChanged")
    m.task.observeField("pin", "onPinChanged")
    m.task.observeField("done", "onDone")
    m.cancelBtn.observeField("buttonSelected", "onCancel")

    serverUrl = Prefs_GetServerUrl()
    if serverUrl <> invalid then m.urlLabel.text = serverUrl + "/pair"

    m.task.control = "RUN"
    ' Which single sign-on the web pair page offers, if any (both probes
    ' are public; a failed one counts as off).
    m.ssoResults = {}
    m.ssoTasks = []
    for each kind in ["oidc", "saml"]
        t = createObject("roSGNode", "ApiCallTask")
        t.path = "/api/v1/auth/" + kind + "/enabled"
        t.auth = false
        t.context = { kind: kind }
        t.observeField("state", "onSsoProbe")
        m.ssoTasks.push(t)
        t.control = "RUN"
    end for
    m.cancelBtn.setFocus(true)
    m.top.initialFocus = m.cancelBtn
end sub

sub onPhaseChanged()
    phase = m.task.phase
    if phase = "creating"
        m.status.text = I18n_T("pair.generating")
    else if phase = "waiting"
        m.status.text = I18n_T("pair.waiting")
    else if phase = "expired_recycling"
        m.status.text = I18n_T("pair.expired")
    else if phase = "failed"
        msg = m.task.failureReason
        if msg = invalid or msg = "" then msg = I18n_T("pair.unreachable")
        m.status.text = I18n_TP("pair.retrying", { reason: msg })
    end if
end sub

sub onSsoProbe(evt as Object)
    t = evt.getRoSGNode()
    if t.state <> "done" then return
    res = t.result
    data = invalid
    if res <> invalid and res.ok = true then data = res.data
    m.ssoResults[t.context.kind] = { data: data }
    if m.ssoResults.Count() < 2 then return
    m.ssoTasks = []
    m.top.findNode("ssoHint").text = ApiResult_SsoHint(m.ssoResults.oidc.data, m.ssoResults.saml.data)
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
