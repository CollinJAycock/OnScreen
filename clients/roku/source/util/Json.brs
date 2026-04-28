' JSON helpers. BrightScript ships ParseJson / FormatJson built-in;
' these wrap them in a way that mirrors the rest of our codebase
' (returns invalid on failure rather than throwing) and adds the
' "unwrap envelope" pattern used by every OnScreen API response.
'
' All OnScreen endpoints respond with `{ "data": <payload> }` or
' `{ "data": <payload>, "meta": {...} }`. Json_UnwrapData strips the
' outer envelope so callers don't write `.data` on every access.

function Json_Parse(raw as String) as Dynamic
    if raw = "" then return invalid
    parsed = ParseJson(raw)
    return parsed
end function

function Json_UnwrapData(parsed as Dynamic) as Dynamic
    if parsed = invalid then return invalid
    if type(parsed) <> "roAssociativeArray" then return parsed
    if parsed.DoesExist("data") then return parsed["data"]
    return parsed
end function

function Json_UnwrapList(parsed as Dynamic) as Object
    ' List endpoints respond with `{ "data": [...], "meta": {...} }`.
    ' Returns the array (or empty array on any miss). Callers should
    ' iterate the returned roArray directly.
    if parsed = invalid then return []
    if type(parsed) <> "roAssociativeArray" then return []
    if not parsed.DoesExist("data") then return []
    data = parsed["data"]
    if type(data) <> "roArray" then return []
    return data
end function
