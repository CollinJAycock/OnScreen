' Normalise an HTTP response into the shape the scenes read:
'
'   { code, ok, data, errorCode, errorMessage }
'
' OnScreen answers success with `{ "data": ... }` (or 204 No Content) and
' failure with `{ "error": { "code", "message", "request_id" } }`. code is
' the HTTP status (or roUrlTransfer's negative curl code on a network
' failure); ok is any 2xx; data is the unwrapped payload (invalid on 204 /
' failure); errorCode / errorMessage are "" unless the server sent an error
' envelope. Pure — unit-tested in tests/ApiResult_test.brs. Depends on
' Json.brs.
function ApiResult_From(code as Integer, raw as Dynamic) as Object
    result = { code: code, ok: code >= 200 and code < 300, data: invalid, errorCode: "", errorMessage: "" }
    if raw = invalid then return result
    if type(raw) <> "String" and type(raw) <> "roString" then return result
    if raw = "" then return result
    parsed = Json_Parse(raw)
    if parsed = invalid then return result
    if result.ok
        result.data = Json_UnwrapData(parsed)
        return result
    end if
    if type(parsed) = "roAssociativeArray"
        err = parsed["error"]
        if err <> invalid and type(err) = "roAssociativeArray"
            if err.code <> invalid and (type(err.code) = "String" or type(err.code) = "roString") then result.errorCode = err.code
            if err.message <> invalid and (type(err.message) = "String" or type(err.message) = "roString") then result.errorMessage = err.message
        end if
    end if
    return result
end function
