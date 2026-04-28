sub init()
    m.top.functionName = "runItemFetch"
end sub

sub runItemFetch()
    id = m.top.itemId
    if id = invalid or id = ""
        m.top.result = {}
        m.top.control = "DONE"
        return
    end if
    parsed = Client_GetSync(ApiItem(id), true)
    if parsed = invalid
        m.top.result = {}
    else
        m.top.result = parsed
    end if
    m.top.control = "DONE"
end sub
