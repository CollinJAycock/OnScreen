' OnScreen API path constants. Mirrors the Go server's router.go
' surface. Keep in sync when bumping the server API version.
'
' Defined as zero-arg functions instead of `const` declarations
' because plain BrightScript (vs. BrighterScript) has no const
' keyword. The function-returning-literal pattern is the standard
' Roku-channel idiom for module-scoped constants.
'
' Paths are joined to the configured server URL by Client.brs; do
' NOT prepend a host here.

function ApiBase() as String
    return "/api/v1"
end function

' Auth
function ApiAuthLogin() as String
    return "/api/v1/auth/login"
end function

function ApiAuthRefresh() as String
    return "/api/v1/auth/refresh"
end function

function ApiAuthLogout() as String
    return "/api/v1/auth/logout"
end function

' Hub / browse
function ApiHub() as String
    return "/api/v1/hub"
end function

function ApiLibraries() as String
    return "/api/v1/libraries"
end function

function ApiSearch() as String
    return "/api/v1/search"
end function

' Items
function ApiItem(id as String) as String
    return "/api/v1/items/" + id
end function

function ApiItemChildren(id as String) as String
    return "/api/v1/items/" + id + "/children"
end function

function ApiItemProgress(id as String) as String
    return "/api/v1/items/" + id + "/progress"
end function

function ApiItemMarkers(id as String) as String
    return "/api/v1/items/" + id + "/markers"
end function

function ApiItemTrickplay(id as String) as String
    return "/api/v1/items/" + id + "/trickplay"
end function

' Asset routes (auth via ?token= query param via the asset-route
' middleware on the Go server; the Roku Video / Poster nodes can't
' attach an Authorization header to their HTTP fetches).
'
' AssetStream takes whichever token the caller has. PlayerScene
' prefers the per-file `stream_token` returned in item-detail
' (24 h TTL, file_id-bound — survives a long movie without
' expiring mid-segment) and falls back to the access token only
' for older server builds that don't ship the field. Roku's Video
' node has no token-refresh hook, so without the per-file token a
' 90-minute movie used to die at the 1 h mark with HTTP 401.
function AssetStream(serverUrl as String, fileId as String, token as String) as String
    return serverUrl + "/media/stream/" + fileId + "?token=" + token
end function

' Pairing endpoints. PairCode mints a PIN + device_token for the
' user to redeem on a phone / laptop; PairPoll long-polls until the
' redemption returns the signed-in token pair. Same flow as the
' Android client's PairingFragment.
function ApiPairCode() as String
    return "/api/v1/auth/pair/code"
end function

function ApiPairPoll() as String
    return "/api/v1/auth/pair/poll"
end function

' Transcode session endpoints. Start POSTs a per-session ffmpeg job
' and returns { session_id, playlist_url, token, … } — the playlist
' URL is relative, so callers must prepend the configured server
' origin before handing it to the Video node. Stop tears down the
' session on player exit so the server doesn't leave the ffmpeg
' process running until the idle-timeout sweep.
function ApiItemTranscode(itemId as String) as String
    return "/api/v1/items/" + itemId + "/transcode"
end function

function ApiTranscodeStop(sessionId as String, token as String) as String
    return "/api/v1/transcode/sessions/" + sessionId + "?token=" + token
end function

function AssetArtwork(serverUrl as String, path as String, width as Integer, accessToken as String) as String
    return serverUrl + "/artwork/" + UrlEncodePath(path) + "?w=" + width.ToStr() + "&token=" + accessToken
end function

' Encode a slash-preserving path segment. Roku has no built-in
' equivalent of JavaScript's encodeURIComponent — write our own
' lightweight one so spaces and other chars don't break the URL.
function UrlEncodePath(path as String) as String
    out = ""
    for i = 0 to Len(path) - 1
        ch = Mid(path, i + 1, 1)
        if (ch >= "a" and ch <= "z") or (ch >= "A" and ch <= "Z") or (ch >= "0" and ch <= "9") or ch = "/" or ch = "-" or ch = "_" or ch = "." or ch = "~"
            out = out + ch
        else
            ' Hex-encode the byte. Asc returns int code; format as %XX.
            code = Asc(ch)
            hexChars = "0123456789ABCDEF"
            hi = (code >> 4) and &Hf
            lo = code and &Hf
            out = out + "%" + Mid(hexChars, hi + 1, 1) + Mid(hexChars, lo + 1, 1)
        end if
    end for
    return out
end function
