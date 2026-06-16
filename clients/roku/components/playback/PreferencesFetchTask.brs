sub init()
    m.top.functionName = "runPreferencesFetch"
end sub

sub runPreferencesFetch()
    parsed = Client_GetSync(ApiPreferences(), true)
    if parsed = invalid
        m.top.result = {}
    else
        m.top.result = parsed
    end if
    m.top.control = "DONE"
end sub
