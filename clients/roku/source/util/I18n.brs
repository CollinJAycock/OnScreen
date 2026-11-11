' The channel's interface language (docs/i18n.md, stage 5): English and
' Spanish. Roku's built-in tr() follows only the device's locale, so the
' channel looks its text up in catalogs of its own: flat JSON in
' pkg:/i18n/en.json and es.json, "key": "text", with the same
' {placeholders} and _one / _other plural keys as the web and TV apps.
' Chinese isn't shipped: Roku's system fonts have no CJK and the package
' limit is 4 MB, so an account set to Chinese sees English here.
'
' Which language (MainScene, at launch and on a change):
'   1. the account's ui_language when it is "en" or "es" (GET
'      /users/me/preferences, read by Home on each visit; remembered on
'      this Roku under PrefsKeyUiLanguage)
'   2. else a choice made in Settings on this Roku (the same key)
'   3. else the device's language: a locale starting "es" (Roku reports
'      es_MX) is Spanish
'   4. else English
'
' Threads: MainScene builds the catalog for the language (English with the
' language's strings over it) and puts it on the global node, with the
' language: m.global.i18nCatalog and m.global.i18nLang (I18n_Install). Any
' component or Task thread that includes this file copies the catalog into
' its own m on its first lookup and again only when i18nLang changes, so a
' lookup is a plain AA read and a Task reads the global node once. Without
' a global node (the brs unit tests, the main thread) the catalog is read
' from the package itself.
'
' Lookup: the language, then English, then the key itself, so a missing
' string shows in English, never blank. Placeholders are {name} with a
' lowercase name; I18n_TP fills them, I18n_TN picks key_one for a count of
' 1 and key_other otherwise (all English and Spanish need) and fills
' {count}. Pure helpers are tested in tests/I18n_test.brs;
' scripts/i18n-check.mjs checks the catalogs against the code.

' The languages this channel ships.
function I18n_Languages() as Object
    return ["en", "es"]
end function

function I18n_IsLanguage(v as Dynamic) as Boolean
    if v = invalid then return false
    if type(v) <> "String" and type(v) <> "roString" then return false
    return v = "en" or v = "es"
end function

' The language for a choice ("en" / "es"; anything else is none) and a Roku
' locale ("es_MX"): the choice, else Spanish for a locale starting "es",
' else English.
function I18n_Resolve(choice as Dynamic, deviceLocale as Dynamic) as String
    if I18n_IsLanguage(choice) then return choice
    if deviceLocale <> invalid and (type(deviceLocale) = "String" or type(deviceLocale) = "roString")
        if LCase(Left(deviceLocale, 2)) = "es" then return "es"
    end if
    return "en"
end function

' What an account's ui_language means on Roku: "en" or "es", else "" (none,
' or a language the channel doesn't ship, such as "zh-Hans").
function I18n_AccountChoice(uiLanguage as Dynamic) as String
    if I18n_IsLanguage(uiLanguage) then return uiLanguage
    return ""
end function

' This Roku's locale, e.g. "en_US" or "es_MX".
function I18n_DeviceLocale() as String
    info = CreateObject("roDeviceInfo")
    if info = invalid then return ""
    return info.GetCurrentLocale()
end function

' A language's own name, for Settings › Language (never translated).
function I18n_LanguageName(lang as String) as String
    if lang = "es" then return "Español"
    return "English"
end function

' Each catalog's path, written out in full so the package's Static Analysis
' sees both files used.
function I18n_CatalogPath(lang as String) as String
    if lang = "es" then return "pkg:/i18n/es.json"
    return "pkg:/i18n/en.json"
end function

function I18n_ReadCatalog(lang as String) as Object
    raw = ReadAsciiFile(I18n_CatalogPath(lang))
    if raw = invalid or raw = "" then return {}
    parsed = ParseJson(raw)
    if parsed = invalid or type(parsed) <> "roAssociativeArray" then return {}
    return parsed
end function

' English with lang's strings over it; an empty or missing one keeps the
' English.
function I18n_BuildCatalog(lang as String) as Object
    cat = I18n_ReadCatalog("en")
    if lang = "en" then return cat
    over = I18n_ReadCatalog(lang)
    for each k in over
        v = over[k]
        if (type(v) = "String" or type(v) = "roString") and v <> "" then cat[k] = v
    end for
    return cat
end function

' MainScene: make lang the channel's language. The catalog goes on the
' global node before the language does, so a reader that sees the new
' language gets its catalog. Every component picks it up at its next
' lookup; MainScene mounts the screen again so it is drawn in it.
sub I18n_Install(lang as String)
    cat = I18n_BuildCatalog(lang)
    g = m.global
    if g <> invalid
        if not g.hasField("i18nLang") then g.addFields({ i18nLang: "", i18nCatalog: {}, i18nEpoch: 0, i18nSave: "" })
        g.i18nCatalog = cat
        g.i18nLang = lang
    end if
    m.i18nCache = { lang: lang, cat: cat }
end sub

' The channel's language: "en" or "es". The brs tests set m.i18nLang.
function I18n_Lang() as String
    g = m.global
    if g <> invalid
        l = g.i18nLang
        if I18n_IsLanguage(l) then return l
    end if
    if I18n_IsLanguage(m.i18nLang) then return m.i18nLang
    return "en"
end function

' This component's copy of the catalog for the current language.
function I18n_Catalog() as Object
    lang = I18n_Lang()
    c = m.i18nCache
    if c <> invalid and c.lang = lang then return c.cat
    cat = invalid
    g = m.global
    if g <> invalid then cat = g.i18nCatalog
    if cat = invalid or type(cat) <> "roAssociativeArray" or cat.Count() = 0 then cat = I18n_BuildCatalog(lang)
    m.i18nCache = { lang: lang, cat: cat }
    return cat
end function

' The text for key.
function I18n_T(key as String) as String
    v = I18n_Catalog()[key]
    if v = invalid then return key
    return v
end function

' The text for key with its {placeholders} filled from params.
function I18n_TP(key as String, params as Object) as String
    return I18n_Fill(I18n_T(key), params)
end function

' key_one for a count of 1, else key_other, with {count} and params filled.
function I18n_TN(key as String, count as Integer, params = invalid as Dynamic) as String
    suffix = "_other"
    if count = 1 then suffix = "_one"
    p = { count: count }
    if params <> invalid and type(params) = "roAssociativeArray" then p.Append(params)
    return I18n_Fill(I18n_T(key + suffix), p)
end function

' text with each {name} replaced by params[name]. Placeholder names are
' lowercase, as an AA literal's keys may come back from for each.
function I18n_Fill(text as String, params as Dynamic) as String
    if params = invalid or type(params) <> "roAssociativeArray" then return text
    out = text
    for each name in params
        out = out.Replace("{" + LCase(name) + "}", I18n_Str(params[name]))
    end for
    return out
end function

function I18n_Str(v as Dynamic) as String
    if v = invalid then return ""
    t = type(v)
    if t = "String" or t = "roString" then return v
    if t = "Integer" or t = "roInt" or t = "roInteger" or t = "LongInteger" or t = "roLongInteger" then return v.ToStr()
    if t = "Float" or t = "roFloat" or t = "Double" or t = "roDouble" then return Str(v).Trim()
    if t = "Boolean" or t = "roBoolean"
        if v then return "true"
        return "false"
    end if
    return ""
end function

' A decimal as Str() writes it, in the language's style: "7.5" is "7,5" in
' Spanish.
function I18n_Decimal(s as String) as String
    if I18n_Lang() = "es" then return s.Replace(".", ",")
    return s
end function
