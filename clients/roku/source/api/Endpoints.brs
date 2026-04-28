' OnScreen API path constants. Mirrors the Go server's router.go
' surface. Keep in sync when bumping the server API version.
'
' Paths are joined to the configured server URL by Client.brs; do
' NOT prepend a host here.

const API_BASE = "/api/v1"

' Auth
const API_AUTH_LOGIN = "/api/v1/auth/login"
const API_AUTH_REFRESH = "/api/v1/auth/refresh"
const API_AUTH_LOGOUT = "/api/v1/auth/logout"

' Hub / browse
const API_HUB = "/api/v1/hub"
const API_LIBRARIES = "/api/v1/libraries"
const API_SEARCH = "/api/v1/search"

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
function AssetStream(serverUrl as String, fileId as String, accessToken as String) as String
    return serverUrl + "/media/stream/" + fileId + "?token=" + accessToken
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
