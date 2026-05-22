' Persistent prefs backed by roRegistrySection. Roku's registry is a
' per-channel key/value store that survives reboots and channel
' updates — equivalent to AndroidX DataStore on the Android client
' or tauri-plugin-store on the desktop.
'
' Section name + key names live as zero-arg functions (BrightScript
' has no `const` keyword in plain mode; functions returning string
' literals are the standard idiom for module-scoped constants).
' Keys are scoped under the "OnScreen" section so a future second
' OnScreen-related channel (e.g., a Roku TV-only variant) couldn't
' accidentally collide.
'
' SECURITY NOTE: roRegistrySection isn't encrypted. The refresh
' token sits as plaintext in NVRAM, readable by the channel itself
' (and only that channel — Roku enforces section isolation). Same
' threat model the Android client documented before its keychain
' migration; revisit if Roku ever adds a Keystore equivalent.

function PrefsSection() as String
    return "OnScreen"
end function

function PrefsKeyServerUrl() as String
    return "server_url"
end function

function PrefsKeyAccessToken() as String
    return "access_token"
end function

function PrefsKeyRefreshToken() as String
    return "refresh_token"
end function

' purpose=asset token. Carried in `?token=` on asset URLs (artwork,
' stream, subtitles) instead of the general access token, which the
' server now rejects in a URL.
function PrefsKeyAssetToken() as String
    return "asset_token"
end function

function PrefsKeyUsername() as String
    return "username"
end function

function Prefs_Get(key as String) as Dynamic
    section = CreateObject("roRegistrySection", PrefsSection())
    if section.Exists(key) then return section.Read(key)
    return invalid
end function

function Prefs_Set(key as String, value as String) as Boolean
    section = CreateObject("roRegistrySection", PrefsSection())
    section.Write(key, value)
    return section.Flush()
end function

function Prefs_Delete(key as String) as Boolean
    section = CreateObject("roRegistrySection", PrefsSection())
    section.Delete(key)
    return section.Flush()
end function

' Convenience helpers — typed accessors for the well-known keys
' rather than scattering string literals across the codebase.

function Prefs_GetServerUrl() as Dynamic
    return Prefs_Get(PrefsKeyServerUrl())
end function

function Prefs_HasServer() as Boolean
    url = Prefs_GetServerUrl()
    return url <> invalid and url <> ""
end function

function Prefs_GetAccessToken() as Dynamic
    return Prefs_Get(PrefsKeyAccessToken())
end function

' The purpose=asset token for `?token=` on asset URLs. Falls back to
' invalid (empty) when the server predates the asset-token work or the
' user hasn't logged in since the upgrade — callers treat invalid as
' "no token" and let the resulting 401 trigger re-auth.
function Prefs_GetAssetToken() as Dynamic
    return Prefs_Get(PrefsKeyAssetToken())
end function

' String form of the asset token for direct use in URL builders that
' take a `String` param (AssetArtwork / AssetStream). Returns "" when
' unset so callers don't have to coerce invalid at every call site.
function Prefs_GetAssetTokenStr() as String
    t = Prefs_Get(PrefsKeyAssetToken())
    if t = invalid then return ""
    return t
end function

function Prefs_IsLoggedIn() as Boolean
    token = Prefs_GetAccessToken()
    return token <> invalid and token <> ""
end function

' asset may be an empty string when talking to a pre-asset-token
' server; we still write it (clearing any stale value) so a downgrade
' doesn't leave an old asset token behind.
function Prefs_SetTokens(access as String, refresh as String, asset as String) as Boolean
    a = Prefs_Set(PrefsKeyAccessToken(), access)
    b = Prefs_Set(PrefsKeyRefreshToken(), refresh)
    c = Prefs_Set(PrefsKeyAssetToken(), asset)
    return a and b and c
end function

function Prefs_ClearAuth() as Boolean
    Prefs_Delete(PrefsKeyAccessToken())
    Prefs_Delete(PrefsKeyRefreshToken())
    Prefs_Delete(PrefsKeyAssetToken())
    Prefs_Delete(PrefsKeyUsername())
    return true
end function
