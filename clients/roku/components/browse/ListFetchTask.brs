sub init()
    m.top.functionName = "runListFetch"
end sub

sub runListFetch()
    p = m.top.path
    if p = invalid or p = ""
        m.top.result = []
        m.top.control = "DONE"
        return
    end if
    parsed = Client_GetSync(p, true)
    if parsed = invalid
        m.top.result = []
    else
        m.top.result = parsed
    end if
    m.top.control = "DONE"
end sub
