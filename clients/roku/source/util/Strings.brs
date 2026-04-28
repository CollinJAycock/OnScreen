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
