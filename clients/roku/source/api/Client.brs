' OnScreen API client wrapping roUrlTransfer.
'
' Two paths:
'   - Synchronous (Client_GetSync, Client_PostSync) — block the
'     calling task on the response. Fine for setup / login flows
'     where the user is staring at a "Connecting..." spinner anyway.
'   - Async via message port (Client_StartAsync) — the caller hands
'     us a message port; we fire the request and return the
'     transfer object. The calling Task node then waits for an
'     roUrlEvent on the port. This is the only pattern that works
'     inside SceneGraph Tasks (BrightScript Tasks are cooperative —
'     blocking calls freeze the UI).
'
' All requests get the Authorization header injected from the
' stored bearer token (Prefs.brs). Login / refresh skip auth
' explicitly via the `auth` argument so they don't carry the
' previous (expired) token.
'
' SSL / cert validation: roUrlTransfer's defaults are sane on RokuOS
' 11+ — system trust store, hostname verification on. No equivalent
' to the OCSP soft-fail dance the Android client needed; Roku's
' firmware doesn't enforce strict OCSP and the chain check is
' identical to what every Roku channel uses.

function HttpOk() as Integer
    return 200
end function

function HttpUnauthorized() as Integer
    return 401
end function

' Build a roUrlTransfer pointed at the configured server with the
' bearer header already attached (when auth=true and a token
' exists). The caller fires the request via AsyncGetToString /
' GetToString / PostFromString and reads the response.
function Client_BuildTransfer(path as String, auth as Boolean) as Object
    serverUrl = Prefs_GetServerUrl()
    if serverUrl = invalid or serverUrl = "" then return invalid

    transfer = CreateObject("roUrlTransfer")
    transfer.SetUrl(serverUrl + path)
    transfer.SetCertificatesFile("common:/certs/ca-bundle.crt")
    transfer.AddHeader("Accept", "application/json")
    transfer.AddHeader("Content-Type", "application/json")

    if auth
        token = Prefs_GetAccessToken()
        if token <> invalid and token <> ""
            transfer.AddHeader("Authorization", "Bearer " + token)
        end if
    end if

    return transfer
end function

' Synchronous GET. Returns the parsed JSON envelope unwrapped to
' .data, or invalid on failure (network, non-200, parse error).
' Avoid in any Task node — blocking calls there hang the UI.
function Client_GetSync(path as String, auth as Boolean) as Dynamic
    transfer = Client_BuildTransfer(path, auth)
    if transfer = invalid then return invalid

    raw = transfer.GetToString()
    code = transfer.GetResponseCode()
    if code <> HttpOk() then return invalid
    return Json_UnwrapData(Json_Parse(raw))
end function

' Synchronous POST with a JSON body. Same caveats as GetSync.
function Client_PostSync(path as String, body as Object, auth as Boolean) as Dynamic
    transfer = Client_BuildTransfer(path, auth)
    if transfer = invalid then return invalid

    raw = transfer.PostFromString(FormatJson(body))
    code = transfer.GetResponseCode()
    if code <> HttpOk() then return invalid
    return Json_UnwrapData(Json_Parse(raw))
end function

' Async GET. Caller provides a message port (typically the Task
' node's port); we wire it onto the transfer and fire the request.
' Caller waits on the port for an roUrlEvent — when it arrives,
' GetResponseCode + GetString on `transfer` gives the result.
'
' We return the transfer object so the caller holds a reference
' (the BrightScript GC would otherwise free it mid-flight and
' silently drop the response).
function Client_StartAsync(path as String, port as Object, auth as Boolean) as Object
    transfer = Client_BuildTransfer(path, auth)
    if transfer = invalid then return invalid

    transfer.SetMessagePort(port)
    transfer.AsyncGetToString()
    return transfer
end function

' Convenience: drain a response from an roUrlEvent + transfer pair
' the caller pulled off their port. Returns unwrapped JSON or
' invalid.
function Client_DrainAsync(event as Object) as Dynamic
    if event = invalid then return invalid
    if event.GetResponseCode() <> HttpOk() then return invalid
    return Json_UnwrapData(Json_Parse(event.GetString()))
end function
