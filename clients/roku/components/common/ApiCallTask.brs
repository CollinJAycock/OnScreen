sub init()
    m.top.functionName = "runApiCall"
end sub

sub runApiCall()
    p = m.top.path
    if p = invalid or p = ""
        m.top.result = ApiResult_From(0, "")
        m.top.control = "DONE"
        return
    end if
    body = invalid
    b = m.top.body
    if b <> invalid and b.Count() > 0 then body = b
    headers = m.top.headers
    if headers <> invalid and headers.Count() = 0 then headers = invalid
    res = Client_RequestSync(m.top.method, p, body, headers, m.top.auth)
    m.top.result = ApiResult_From(res.code, res.raw)
    m.top.control = "DONE"
end sub
