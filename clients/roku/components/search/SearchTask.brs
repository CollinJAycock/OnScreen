sub init()
    m.top.functionName = "runSearch"
end sub

sub runSearch()
    q = m.top.query
    if q = invalid or Len(q) < 2
        m.top.result = []
        m.top.control = "DONE"
        return
    end if
    encoded = UrlEncodePath(q)
    parsed = Client_GetSync(ApiSearch() + "?q=" + encoded + "&limit=30", true)
    if parsed = invalid
        m.top.result = []
    else
        m.top.result = parsed
    end if
    m.top.control = "DONE"
end sub
