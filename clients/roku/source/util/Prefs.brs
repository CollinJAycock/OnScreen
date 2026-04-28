' Persistent prefs backed by roRegistrySection. Roku's registry is a
' per-channel key/value store that survives reboots and channel
' updates — equivalent to AndroidX DataStore on the Android client
' or tauri-plugin-store on the desktop.
'
' Keys are scoped under the "OnScreen" section so a future second
' OnScreen-related channel (e.g., a Roku TV-only variant) couldn't
' accidentally collide.
'
' SECURITY NOTE: roRegistrySection isn't encrypted. The refresh
' token sits as plaintext in NVRAM, readable by the channel itself
' (and only that channel — Roku enforces section isolation). Same
' threat model the Android client documented before its keychain
' migration; revisit if Roku ever adds a Keystore equivalent.

const PREFS_SECTION = "OnScreen"
const PREFS_KEY_SERVER_URL = "server_url"
const PREFS_KEY_ACCESS_TOKEN = "access_token"
const PREFS_KEY_REFRESH_TOKEN = "refresh_token"
const PREFS_KEY_USERNAME = "username"

function Prefs_Get(key as String) as Dynamic
    section = CreateObject("roRegistrySection", PREFS_SECTION)
    if section.Exists(key) then return section.Read(key)
    return invalid
end function

function Prefs_Set(key as String, value as String) as Boolean
    section = CreateObject("roRegistrySection", PREFS_SECTION)
    section.Write(key, value)
    return section.Flush()
end function

function Prefs_Delete(key as String) as Boolean
    section = CreateObject("roRegistrySection", PREFS_SECTION)
    section.Delete(key)
    return section.Flush()
end function

' Convenience helpers — typed accessors for the well-known keys
' rather than scattering string literals across the codebase.

function Prefs_GetServerUrl() as Dynamic
    return Prefs_Get(PREFS_KEY_SERVER_URL)
end function

function Prefs_HasServer() as Boolean
    url = Prefs_GetServerUrl()
    return url <> invalid and url <> ""
end function

function Prefs_GetAccessToken() as Dynamic
    return Prefs_Get(PREFS_KEY_ACCESS_TOKEN)
end function

function Prefs_IsLoggedIn() as Boolean
    token = Prefs_GetAccessToken()
    return token <> invalid and token <> ""
end function

function Prefs_SetTokens(access as String, refresh as String) as Boolean
    a = Prefs_Set(PREFS_KEY_ACCESS_TOKEN, access)
    b = Prefs_Set(PREFS_KEY_REFRESH_TOKEN, refresh)
    return a and b
end function

function Prefs_ClearAuth() as Boolean
    Prefs_Delete(PREFS_KEY_ACCESS_TOKEN)
    Prefs_Delete(PREFS_KEY_REFRESH_TOKEN)
    Prefs_Delete(PREFS_KEY_USERNAME)
    return true
end function
