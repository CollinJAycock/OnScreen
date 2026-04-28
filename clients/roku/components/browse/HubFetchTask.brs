' Task body. Runs on its own thread when the caller flips
' control=RUN; SceneGraph invokes whatever function is named in
' functionName (default "main", but Tasks declared via XML pick
' it up from `script` + the standard `init`/runner contract).
'
' On Task nodes the convention is to have `init()` set
' functionName, then implement the runner. We use synchronous
' Client_GetSync here — fine inside a Task since we're already on
' a background thread; the caller's render thread never waits.

sub init()
    m.top.functionName = "runHubFetch"
end sub

sub runHubFetch()
    parsed = Client_GetSync(API_HUB, true)
    if parsed = invalid
        m.top.result = {}
    else
        m.top.result = parsed
    end if
    ' Marking control=DONE flips Task.state to "done" which the
    ' caller observes. Roku also auto-flips state at function
    ' return; setting control explicitly is belt-and-suspenders.
    m.top.control = "DONE"
end sub
