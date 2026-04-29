' PairTask runs the pairing loop off the UI thread.
'
' Roku Tasks expose three fields used as a shared event bus with the
' PairScene controller: `pin`, `phase`, `done`. The task writes them
' as the loop progresses and the scene observes the changes to
' update the rendered UI.
'
' Phase progression:
'   creating  -> server returns code, pin field populates
'   waiting   -> polling /auth/pair/poll
'   expired_recycling -> code TTL elapsed, request a fresh one
'   failed    -> network blip; auto-retry after a beat
'   (done = true terminates the task and tells the scene to navigate)
'
' Token persistence happens here too — the scene's onDone observer
' just calls navigateTo("HomeScene") and trusts the prefs are
' already written. Means a stop-mid-flight task doesn't half-write
' tokens, since the only path that writes is "Done detected".

sub init()
    m.top.functionName = "runPairing"
end sub

sub runPairing()
    ' Loop indefinitely until the scene flips control=STOP or we
    ' write done=true. Each iteration is one full pairing cycle:
    ' request a code, poll until done / expired / cancelled.
    while m.top.done <> true
        m.top.phase = "creating"
        codeResp = Client_PostSync(ApiPairCode(), {}, false)
        if codeResp = invalid
            ' Network blip during code creation — surface as failed
            ' and back off before retrying. BrightScript has no
            ' `continue` keyword; sleep and let the outer while
            ' kick the next attempt.
            m.top.failureReason = "Couldn't reach server"
            m.top.phase = "failed"
            sleep(5000)
        else
            m.top.pin = codeResp["pin"]
            deviceToken = codeResp["device_token"]
            pollSeconds = 2
            if codeResp["poll_after"] <> invalid
                pollSeconds = codeResp["poll_after"]
            end if
            ' Clamp the same way the Android client does — tighter
            ' than the server-suggested cadence so the screen
            ' dismisses within ~1 s of the user typing the PIN.
            if pollSeconds < 1 then pollSeconds = 1
            if pollSeconds > 3 then pollSeconds = 3

            m.top.phase = "waiting"
            cycleDone = false
            while cycleDone = false
                sleep(pollSeconds * 1000)
                ' Manual polled-stop check — Tasks don't preempt;
                ' if the scene sets control=STOP we'll see it on
                ' the next outer loop iteration after the sleep.
                ' (BrightScript Tasks have no built-in cancel ctx.)
                result = pollOnce(deviceToken)
                if result = "done"
                    cycleDone = true
                    m.top.done = true
                    return
                else if result = "expired"
                    m.top.phase = "expired_recycling"
                    cycleDone = true
                else if result = "failed"
                    ' Keep polling — a single failed poll could be a
                    ' network blip; the user can hit Cancel to bail.
                    m.top.failureReason = "Couldn't reach server"
                    m.top.phase = "failed"
                end if
            end while
        end if
    end while
end sub

' Poll once. Persists tokens + returns "done" when the user has
' redeemed the PIN; "pending" / "expired" / "failed" otherwise.
function pollOnce(deviceToken as String) as String
    transfer = Client_BuildTransfer(ApiPairPoll(), false)
    if transfer = invalid then return "failed"
    transfer.AddHeader("Authorization", "Bearer " + deviceToken)
    body = transfer.PostFromString("")
    code = transfer.GetFailureReason()
    httpStatus = transfer.GetResponseCode()
    if httpStatus = 200
        envelope = ParseJson(body)
        if envelope = invalid then return "failed"
        pair = envelope.data
        if pair = invalid then return "failed"
        Prefs_SetTokens(pair["access_token"], pair["refresh_token"])
        if pair["username"] <> invalid then Prefs_Set(PrefsKeyUsername(), pair["username"])
        return "done"
    end if
    if httpStatus = 202 then return "pending"
    if httpStatus = 410 then return "expired"
    return "failed"
end function
