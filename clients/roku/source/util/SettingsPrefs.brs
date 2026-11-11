' Settings' Playback and Quality rows: the choices, as the TV web app's
' lib/settingsPrefs.ts has them, and the bodies the server takes. Pure
' (tests/SettingsPrefs_test.brs). An option value of invalid is "none"
' (no preference / no limit), sent as null. Labels come from the catalog
' (source/util/I18n.brs); resolutions, codecs and units aren't translated.

function SettingsPrefs_LangOptions() as Object
    return [
        { value: "", label: I18n_T("prefs.auto") },
        { value: "eng", label: I18n_T("lang.en") },
        { value: "spa", label: I18n_T("lang.es") },
        { value: "fra", label: I18n_T("lang.fr") },
        { value: "deu", label: I18n_T("lang.de") },
        { value: "ita", label: I18n_T("lang.it") },
        { value: "por", label: I18n_T("lang.pt") },
        { value: "jpn", label: I18n_T("lang.ja") },
        { value: "kor", label: I18n_T("lang.ko") },
        { value: "zho", label: I18n_T("lang.zh") },
        { value: "rus", label: I18n_T("lang.ru") },
        { value: "hin", label: I18n_T("lang.hi") },
        { value: "ara", label: I18n_T("lang.ar") }
    ]
end function

' Subtitles add Off ("off") before the languages.
function SettingsPrefs_SubOptions() as Object
    out = [{ value: "off", label: I18n_T("prefs.subsOff") }]
    out.Append(SettingsPrefs_LangOptions())
    return out
end function

function SettingsPrefs_QualityRows() as Object
    bitrates = [{ value: invalid, label: I18n_T("prefs.noLimit") }]
    for each mbps in [40, 20, 12, 8, 4, 2, 1]
        bitrates.push({ value: mbps * 1000, label: mbps.ToStr() + " Mbps" })
    end for
    audio = [{ value: invalid, label: I18n_T("prefs.original") }]
    for each k in [320, 256, 192, 128, 96]
        audio.push({ value: k, label: k.ToStr() + " kbps" })
    end for
    return [
        { field: "max_video_height", title: I18n_T("prefs.maxResolution"), options: [
            { value: invalid, label: I18n_T("prefs.original") },
            { value: 2160, label: "4K" },
            { value: 1080, label: "1080p" },
            { value: 720, label: "720p" },
            { value: 480, label: "480p" }
        ] },
        { field: "max_video_bitrate_kbps", title: I18n_T("prefs.maxVideoBitrate"), options: bitrates },
        { field: "max_audio_bitrate_kbps", title: I18n_T("prefs.maxAudioBitrate"), options: audio },
        { field: "preferred_video_codec", title: I18n_T("prefs.videoFormat"), options: [
            { value: invalid, label: I18n_T("prefs.automatic") },
            { value: "h264", label: I18n_T("prefs.h264") },
            { value: "hevc", label: I18n_T("prefs.hevc") }
        ] }
    ]
end function

' The option index for a stored value: exact first, then (languages) the
' same language by its code ("fre", "fr" and "fra" are French). 0 (the
' "none" option) when nothing matches or nothing is stored.
function SettingsPrefs_IndexOf(options as Object, value as Dynamic) as Integer
    if value = invalid then return 0
    isStr = SettingsPrefs_IsStr(value)
    isNum = SettingsPrefs_IsNum(value)
    if not isStr and not isNum then return 0
    if isStr and value = "" then return 0
    if isNum and value = 0 then return 0
    ' Compared within a kind: numbers parsed from JSON may be Integer,
    ' LongInteger or boxed, and a string never equals a number.
    for i = 0 to options.Count() - 1
        v = options[i].value
        if isStr and SettingsPrefs_IsStr(v) and v = value then return i
        if isNum and SettingsPrefs_IsNum(v) and v = value then return i
    end for
    if isStr
        want = SettingsPrefs_NormLang(value)
        for i = 0 to options.Count() - 1
            v = options[i].value
            if SettingsPrefs_IsStr(v) and v <> "" and SettingsPrefs_NormLang(v) = want then return i
        end for
    end if
    return 0
end function

function SettingsPrefs_IsStr(v as Dynamic) as Boolean
    t = type(v)
    return t = "String" or t = "roString"
end function

function SettingsPrefs_IsNum(v as Dynamic) as Boolean
    t = type(v)
    return t = "Integer" or t = "roInt" or t = "LongInteger" or t = "roLongInteger" or t = "Float" or t = "roFloat" or t = "Double" or t = "roDouble"
end function

' A language code to its ISO 639-2/T three letters, region dropped.
function SettingsPrefs_NormLang(code as String) as String
    c = LCase(code.Trim())
    dash = Instr(1, c, "-")
    if dash > 0 then c = Left(c, dash - 1)
    aliases = { en: "eng", es: "spa", fr: "fra", fre: "fra", de: "deu", ger: "deu", it: "ita", pt: "por", ja: "jpn", ko: "kor", zh: "zho", chi: "zho", ru: "rus", hi: "hin", ar: "ara" }
    a = aliases[c]
    if a <> invalid then return a
    return c
end function

function SettingsPrefs_Next(options as Object, idx as Integer) as Integer
    if options.Count() = 0 then return 0
    return (idx + 1) mod options.Count()
end function

' PUT /users/me/preferences: the two languages ("" = none, sent as null).
function SettingsPrefs_LangBody(audioLang as String, subLang as String) as Object
    body = { preferred_audio_lang: invalid, preferred_subtitle_lang: invalid }
    if audioLang <> "" then body.preferred_audio_lang = audioLang
    if subLang <> "" then body.preferred_subtitle_lang = subLang
    return body
end function

' PUT /users/me/quality-profile replaces the whole profile: every field,
' invalid (null) for no limit. values maps field -> value.
function SettingsPrefs_QualityBody(values as Object, forcedOnly as Boolean) as Object
    body = { forced_subtitles_only: forcedOnly }
    for each row in SettingsPrefs_QualityRows()
        body[row.field] = values[row.field]
    end for
    return body
end function

' What a row shows for a stored value: its option's label, else (a value
' set through the API or another client) the value itself, kept as is
' until the user picks another.
function SettingsPrefs_ValueLabel(options as Object, value as Dynamic, field as String) as String
    idx = SettingsPrefs_IndexOf(options, value)
    if idx > 0 then return options[idx].label
    if SettingsPrefs_IsStr(value) and value <> "" then return value
    if SettingsPrefs_IsNum(value) and value <> 0
        if field = "max_video_height" then return value.ToStr() + "p"
        if field = "max_video_bitrate_kbps" then return I18n_Decimal(Str(Int(value / 100) / 10).Trim()) + " Mbps"
        return value.ToStr() + " kbps"
    end if
    return options[0].label
end function

' The audio track to play for a preferred language: its position in
' audio_streams, or -1 to leave the file's default alone (no preference,
' no track in that language, or the default already is one).
function SettingsPrefs_AudioPosition(streams as Dynamic, lang as Dynamic) as Integer
    if not SettingsPrefs_IsStr(lang) or lang = "" then return -1
    if streams = invalid or type(streams) <> "roArray" or streams.Count() < 2 then return -1
    want = SettingsPrefs_NormLang(lang)
    def = 0
    for i = 0 to streams.Count() - 1
        if streams[i] <> invalid and streams[i].default = true
            def = i
            exit for
        end if
    end for
    if SettingsPrefs_StreamLang(streams[def]) = want then return -1
    for i = 0 to streams.Count() - 1
        if SettingsPrefs_StreamLang(streams[i]) = want then return i
    end for
    return -1
end function

function SettingsPrefs_StreamLang(s as Dynamic) as String
    if s = invalid or type(s) <> "roAssociativeArray" then return ""
    if not SettingsPrefs_IsStr(s.language) or s.language = "" then return ""
    return SettingsPrefs_NormLang(s.language)
end function
