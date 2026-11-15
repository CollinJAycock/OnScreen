' See AuthGate.xml. Runs on the render thread (callFunc from the Tasks).

sub init()
    m.ticket = 0
    m.heldFor = invalid
end sub

' A ticket (> 0) when the caller now holds the refresh, 0 when another
' Task does. A hold older than AuthGate_StaleMs is taken over: its Task
' was stopped mid-request and will never call endRefresh.
function tryBeginRefresh(unused as Dynamic) as Integer
    if m.top.refreshing and m.heldFor <> invalid and m.heldFor.TotalMilliseconds() < AuthGate_StaleMs() then return 0
    m.ticket = m.ticket + 1
    m.heldFor = CreateObject("roTimespan")
    m.top.refreshing = true
    return m.ticket
end function

' Releases the gate, unless it was taken over since `ticket` got it.
function endRefresh(ticket as Dynamic) as Boolean
    t = type(ticket)
    if (t = "Integer" or t = "roInt" or t = "roInteger") and ticket = m.ticket
        m.top.refreshing = false
        m.heldFor = invalid
    end if
    return true
end function

' Longer than one refresh can take (Client_RequestTimeoutMs, 20 s).
function AuthGate_StaleMs() as Integer
    return 30000
end function
