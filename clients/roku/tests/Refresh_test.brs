' Unit tests for the token-refresh helpers in source/api/Client.brs: how a
' Task that lost a refresh race tells the pair was rotated by another one
' (instead of clearing the fresh session), and the AuthGate ticket.
'
' brs has no roRegistrySection, so this suite doesn't load Prefs.brs: the
' Prefs_* functions Client.brs calls are faked below over m.fakeRegistry.

sub Main()
    m.fakeRegistry = { refresh_token: "old" }

    runBool("rotated: unchanged token", Client_RefreshRotated("old", false), false)
    m.fakeRegistry.refresh_token = "new"
    runBool("rotated: another Task stored a new pair", Client_RefreshRotated("old", false), true)
    runBool("rotated: settling sees it at once", Client_RefreshRotated("old", true), true)
    m.fakeRegistry.Delete("refresh_token")
    runBool("rotated: a cleared session is not a rotation", Client_RefreshRotated("old", false), false)
    m.fakeRegistry.refresh_token = ""
    runBool("rotated: an empty token is not a rotation", Client_RefreshRotated("old", false), false)

    runBool("ticket: a held refresh", Client_IsTicket(3), true)
    runBool("ticket: another Task holds it", Client_IsTicket(0), false)
    runBool("ticket: callFunc answered nothing", Client_IsTicket(invalid), false)
    runBool("ticket: not a number", Client_IsTicket("1"), false)

    ' No m.global here (as on the render thread before MainScene): ungated.
    runBool("gate: none outside SceneGraph", Client_AuthGate() = invalid, true)

    print "DONE: Refresh_test"
end sub

function PrefsKeyRefreshToken() as String
    return "refresh_token"
end function

function Prefs_Get(key as String) as Dynamic
    return m.fakeRegistry[key]
end function

function Prefs_IsString(v as Dynamic) as Boolean
    if v = invalid then return false
    t = Type(v)
    return t = "String" or t = "roString"
end function

function Prefs_IsNonEmptyString(v as Dynamic) as Boolean
    return Prefs_IsString(v) and v <> ""
end function

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
