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
    path = ApiSearch() + "?q=" + encoded + "&limit=30"
    lib = m.top.libraryId
    if lib <> invalid and lib <> "" then path = path + "&library_id=" + lib
    parsed = Client_GetSync(path, true)
    if parsed = invalid
        m.top.result = []
    else
        m.top.result = parsed
    end if
    m.top.control = "DONE"
end sub
