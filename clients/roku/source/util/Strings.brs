' Pure string helpers. BrightScript's standard library is bare; we
' implement the few functions we need across multiple scenes here
' so call sites stay small and the helpers are unit-testable
' (scene controllers depend on findNode and aren't loadable
' standalone in the brs interpreter).

' Trim leading + trailing spaces. BrightScript has no built-in
' trim. Tabs and newlines are intentionally NOT stripped — we use
' this for user input from the on-screen keyboard which only
' produces spaces.
function StringTrim(s as String) as String
    if s = invalid then return ""
    startIdx = 1
    endIdx = Len(s)
    while startIdx <= endIdx and Mid(s, startIdx, 1) = " "
        startIdx = startIdx + 1
    end while
    while endIdx >= startIdx and Mid(s, endIdx, 1) = " "
        endIdx = endIdx - 1
    end while
    if endIdx < startIdx then return ""
    return Mid(s, startIdx, endIdx - startIdx + 1)
end function

' Drop one trailing slash. Used to normalise the server URL the
' user typed (`http://x/` → `http://x`) so path concatenation in
' Client.brs doesn't produce double slashes. Idempotent —
' "http://x" → "http://x".
function StringStripTrailingSlash(s as String) as String
    if s = "" then return s
    if Right(s, 1) = "/" then return Left(s, Len(s) - 1)
    return s
end function

function StringEndsWith(s as String, suffix as String) as Boolean
    if Len(suffix) > Len(s) then return false
    return Right(s, Len(suffix)) = suffix
end function

' Lower-cased host of a URL: "http://u@Host:7070/x" -> "host",
' "http://[fd00::1]:7070" -> "fd00::1". "" when there's no "://".
function StringUrlHost(url as String) as String
    i = Instr(1, url, "://")
    if i = 0 then return ""
    rest = Mid(url, i + 3)
    for each sep in ["/", "?", "#"]
        j = Instr(1, rest, sep)
        if j > 0 then rest = Left(rest, j - 1)
    end for
    ' Drop userinfo (everything up to the last "@").
    at = Instr(1, rest, "@")
    while at > 0
        rest = Mid(rest, at + 1)
        at = Instr(1, rest, "@")
    end while
    if Left(rest, 1) = "["
        j = Instr(1, rest, "]")
        if j = 0 then return ""
        return LCase(Mid(rest, 2, j - 2))
    end if
    j = Instr(1, rest, ":")
    if j > 0 then rest = Left(rest, j - 1)
    return LCase(rest)
end function

function stringIsDigits(s as String) as Boolean
    if s = "" or Len(s) > 3 then return false
    for k = 1 to Len(s)
        c = Asc(Mid(s, k, 1))
        if c < 48 or c > 57 then return false
    end for
    return true
end function

' True when `host` can only be a local-network / loopback address:
' RFC1918, CGNAT (Tailscale), link-local and loopback IPv4; ULA,
' link-local and loopback IPv6; single-label names and the usual
' local suffixes. Plain http:// to such a host is the normal self-
' hosted LAN setup; plain http:// to anything else sends the password
' and refresh token across the internet in cleartext. Mirrors
' isLocalNetworkHost in the Tizen/webOS clients.
function StringIsLocalNetworkHost(host as String) as Boolean
    h = LCase(host)
    if Right(h, 1) = "." then h = Left(h, Len(h) - 1)
    if h = "" then return false
    if h = "localhost" or StringEndsWith(h, ".localhost") then return true
    for each suffix in [".local", ".lan", ".home.arpa", ".internal"]
        if StringEndsWith(h, suffix) then return true
    end for
    if Instr(1, h, ":") > 0
        if h = "::1" then return true
        if Instr(1, h, ":") <> 5 then return false
        p2 = Left(h, 2)
        p3 = Left(h, 3)
        return p2 = "fc" or p2 = "fd" or p3 = "fe8" or p3 = "fe9" or p3 = "fea" or p3 = "feb"
    end if
    parts = h.Split(".")
    if parts.Count() = 4 and stringIsDigits(parts[0]) and stringIsDigits(parts[1]) and stringIsDigits(parts[2]) and stringIsDigits(parts[3])
        a = Val(parts[0])
        b = Val(parts[1])
        return a = 10 or a = 127 or (a = 172 and b >= 16 and b <= 31) or (a = 192 and b = 168) or (a = 169 and b = 254) or (a = 100 and b >= 64 and b <= 127)
    end if
    ' Single-label names ("nas") only resolve via local DNS.
    return Instr(1, h, ".") = 0
end function
