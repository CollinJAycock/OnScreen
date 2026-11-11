' See SettingsScene.xml. Sign out and Forget server revoke the session on
' its server first (LogoutTask, which also clears the tokens), then go to
' Sign in or to server setup. Playback rows load from GET
' /users/me/preferences; OK on one moves it to its next value and saves
' straight away: languages through PUT /users/me/preferences, forced
' subtitles and the quality limits through PUT /users/me/quality-profile
' (which replaces the whole profile, so every field is sent). One save of
' each kind in flight; a change made meanwhile is sent when it lands.
'
' Language: OK moves the row to its next choice (Device language →
' English → Español). MainScene switches the channel at once, mounting
' Settings again in the new language with the focus back on the row, and
' saves the choice to the account (setUiLanguage); when the account didn't
' take it (a server before v3) the note under the row says it stays on
' this Roku.

sub init()
    m.signOutBtn = m.top.findNode("signOutBtn")
    m.forgetBtn = m.top.findNode("forgetBtn")
    m.logout = invalid
    m.after = ""
    ' The account button Up from the Language row goes back to.
    m.lastAccountBtn = m.signOutBtn

    m.top.findNode("heading").text = I18n_T("nav.settings")
    m.top.findNode("accountHeading").text = I18n_T("settings.account")
    m.top.findNode("insecureLine").text = I18n_T("settings.insecure")
    m.signOutBtn.text = I18n_T("settings.signOut")
    m.forgetBtn.text = I18n_T("settings.forgetServer")
    m.top.findNode("accountHelp").text = I18n_T("settings.accountHelp")
    m.top.findNode("aboutHeading").text = I18n_T("settings.about")
    m.top.findNode("privacyLine").text = I18n_TP("settings.privacyLine", { url: "https://onscreen.wolverscreen.com/privacy" })
    m.top.findNode("sourceLine").text = I18n_TP("settings.sourceLine", { url: "github.com/CollinJAycock/OnScreen" })
    m.top.findNode("languageHeading").text = I18n_T("language.section")
    m.top.findNode("playbackHeading").text = I18n_T("settings.playback")

    user = Prefs_Get(PrefsKeyUsername())
    if Prefs_IsNonEmptyString(user)
        m.top.findNode("userLine").text = I18n_TP("settings.signedInAs", { username: user })
    else
        m.top.findNode("userLine").text = I18n_T("settings.signedIn")
    end if
    url = Prefs_GetServerUrl()
    if url = invalid then url = ""
    m.top.findNode("serverLine").text = I18n_TP("settings.serverLine", { url: url })
    m.top.findNode("insecureLine").visible = (LCase(Left(url, 7)) = "http://")
    m.top.findNode("versionLine").text = I18n_TP("settings.versionLine", { version: CreateObject("roAppInfo").GetVersion() })

    m.signOutBtn.observeField("buttonSelected", "onSignOutPressed")
    m.forgetBtn.observeField("buttonSelected", "onForgetPressed")
    m.signOutBtn.setFocus(true)
    m.top.initialFocus = m.signOutBtn

    m.rows = []
    m.rowIndex = 0
    m.loaded = false
    m.values = {}
    m.saving = { lang: invalid, quality: invalid }
    m.dirty = { lang: false, quality: false }
    buildPlaybackRows()
    buildLanguageRow()
    m.prefsTask = m.top.findNode("prefsTask")
    m.prefsTask.observeField("state", "onPrefsLoaded")
    m.prefsTask.control = "RUN"
end sub

' ── Playback ─────────────────────────────────────────────────────────

' The rows, top to bottom: { key, title, options, kind ("lang" | "quality") }.
function playbackRowDefs() as Object
    defs = [
        { key: "preferred_audio_lang", title: I18n_T("settings.audioLanguage"), options: SettingsPrefs_LangOptions(), kind: "lang" },
        { key: "preferred_subtitle_lang", title: I18n_T("settings.subtitleLanguage"), options: SettingsPrefs_SubOptions(), kind: "lang" },
        { key: "forced_subtitles_only", title: I18n_T("settings.forcedOnly"), options: [{ value: false, label: I18n_T("common.off") }, { value: true, label: I18n_T("common.on") }], kind: "quality" }
    ]
    for each q in SettingsPrefs_QualityRows()
        defs.push({ key: q.field, title: q.title, options: q.options, kind: "quality" })
    end for
    return defs
end function

sub buildPlaybackRows()
    holder = m.top.findNode("playbackRows")
    y = 0
    for each d in playbackRowDefs()
        r = addRow(holder, y, d.title)
        r.button.observeField("buttonSelected", "onRowSelected")
        m.rows.push({ def: d, button: r.button, value: r.value })
        y = y + 80
    end for
end sub

' One row: a full-width button with the title on its left and the value
' on its right. Returns { button, value }.
function addRow(holder as Object, y as Integer, titleText as String) as Object
    g = holder.createChild("Group")
    g.translation = [0, y]
    btn = g.createChild("OsButton")
    btn.minWidth = 800
    btn.maxWidth = 800
    btn.text = " "
    title = g.createChild("Label")
    title.translation = [28, 0]
    title.height = 68
    title.vertAlign = "center"
    title.text = titleText
    value = g.createChild("Label")
    value.translation = [28, 0]
    value.width = 744
    value.height = 68
    value.horizAlign = "right"
    value.vertAlign = "center"
    value.color = "0xE8E8EEFF"
    value.text = "..."
    return { button: btn, value: value }
end function

sub onPrefsLoaded()
    if m.prefsTask.state <> "done" then return
    p = m.prefsTask.result
    if p = invalid then p = {}
    for each row in m.rows
        k = row.def.key
        v = p[k]
        if k = "forced_subtitles_only" then v = (v = true)
        m.values[k] = v
        showRowValue(row)
    end for
    rating = p.max_content_rating
    if SettingsPrefs_IsStr(rating) and rating <> ""
        m.top.findNode("ratingLine").text = I18n_TP("settings.ratingLimit", { rating: rating })
    else
        m.top.findNode("ratingLine").text = I18n_T("settings.noRatingLimit")
    end if
    m.loaded = true
end sub

sub showRowValue(row as Object)
    k = row.def.key
    if k = "forced_subtitles_only"
        if m.values[k] = true then row.value.text = I18n_T("common.on") else row.value.text = I18n_T("common.off")
        return
    end if
    row.value.text = SettingsPrefs_ValueLabel(row.def.options, m.values[k], k)
end sub

sub onRowSelected()
    if not m.loaded then return
    row = m.rows[m.rowIndex]
    k = row.def.key
    if k = "forced_subtitles_only"
        m.values[k] = not (m.values[k] = true)
    else
        opts = row.def.options
        nxt = SettingsPrefs_Next(opts, SettingsPrefs_IndexOf(opts, m.values[k]))
        m.values[k] = opts[nxt].value
    end if
    showRowValue(row)
    save(row.def.kind)
end sub

sub save(kind as String)
    if m.saving[kind] <> invalid
        m.dirty[kind] = true
        return
    end if
    m.dirty[kind] = false
    t = createObject("roSGNode", "ApiCallTask")
    t.method = "PUT"
    if kind = "lang"
        t.path = ApiPreferences()
        t.body = SettingsPrefs_LangBody(langValue("preferred_audio_lang"), langValue("preferred_subtitle_lang"))
    else
        t.path = ApiQualityProfile()
        t.body = SettingsPrefs_QualityBody(m.values, m.values.forced_subtitles_only = true)
    end if
    t.context = { kind: kind }
    t.observeField("state", "onSaveDone")
    m.saving[kind] = t
    t.control = "RUN"
end sub

function langValue(key as String) as String
    v = m.values[key]
    if SettingsPrefs_IsStr(v) then return v
    return ""
end function

sub onSaveDone(msg as Object)
    t = msg.getRoSGNode()
    if t = invalid or t.state <> "done" then return
    kind = t.context.kind
    m.saving[kind] = invalid
    res = t.result
    if res = invalid or res.ok <> true then print "[settings] save "; kind; " failed"
    if m.dirty[kind] then save(kind)
end sub

sub onSignOutPressed()
    Menu_Show("signOut", I18n_T("settings.signOutTitle"), I18n_T("settings.signOutBody"), [I18n_T("settings.signOut"), I18n_T("common.cancel")])
end sub

sub onForgetPressed()
    Menu_Show("forget", I18n_T("settings.forgetTitle"), I18n_T("settings.forgetBody"), [I18n_T("settings.forgetServer"), I18n_T("common.cancel")])
end sub

sub onMenuChoice(menuId as String, index as Integer)
    if index <> 0
        if menuId = "forget" then m.forgetBtn.setFocus(true) else m.signOutBtn.setFocus(true)
        return
    end if
    if m.logout <> invalid then return
    m.after = menuId
    if menuId = "signOut" then m.signOutBtn.text = I18n_T("common.signingOut") else m.forgetBtn.text = I18n_T("common.signingOut")
    m.logout = createObject("roSGNode", "LogoutTask")
    m.logout.observeField("state", "onLogoutDone")
    m.logout.control = "RUN"
end sub

sub onLogoutDone()
    if m.logout = invalid or m.logout.state <> "done" then return
    m.logout = invalid
    ' LogoutTask cleared the tokens; Forget server also drops the address.
    Prefs_ClearAuth()
    if m.after = "forget"
        Prefs_Delete(PrefsKeyServerUrl())
        getMainScene().callFunc("navigateTo", "ServerSetupScene")
    else
        getMainScene().callFunc("navigateTo", "LoginScene")
    end if
end sub

' Left / Right between the two buttons and over to the Playback rows,
' Up / Down along the rows; Down from the buttons to the Language row, Up
' back, Right from it to the Playback rows; Back is MainScene's (to Home).
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if m.langButton.hasFocus()
        if key = "up" then m.lastAccountBtn.setFocus(true)
        if key = "right" then focusRow(m.rowIndex)
        return key = "up" or key = "down" or key = "left" or key = "right"
    end if
    if key = "down" and (m.signOutBtn.hasFocus() or m.forgetBtn.hasFocus())
        if m.forgetBtn.hasFocus() then m.lastAccountBtn = m.forgetBtn else m.lastAccountBtn = m.signOutBtn
        m.langButton.setFocus(true)
        return true
    end if
    if focusedRow() >= 0
        i = focusedRow()
        if key = "up" and i > 0 then focusRow(i - 1)
        if key = "down" and i < m.rows.Count() - 1 then focusRow(i + 1)
        if key = "left" then m.forgetBtn.setFocus(true)
        return key = "up" or key = "down" or key = "left"
    end if
    if key = "right" and m.forgetBtn.hasFocus()
        focusRow(m.rowIndex)
        return true
    end if
    if key = "right" and m.signOutBtn.hasFocus()
        m.forgetBtn.setFocus(true)
        return true
    end if
    if key = "left" and m.forgetBtn.hasFocus()
        m.signOutBtn.setFocus(true)
        return true
    end if
    return false
end function

function focusedRow() as Integer
    for i = 0 to m.rows.Count() - 1
        if m.rows[i].button.hasFocus() then return i
    end for
    return -1
end function

sub focusRow(i as Integer)
    m.rowIndex = i
    m.rows[i].button.setFocus(true)
end sub

' ── Language ─────────────────────────────────────────────────────────

' The row's choices in order: "" (the device's language), "en", "es".
function languageChoices() as Object
    return ["", "en", "es"]
end function

' This Roku's choice ("" = the device's language).
function languageChoice() as String
    v = Prefs_Get(PrefsKeyUiLanguage())
    if I18n_IsLanguage(v) then return v
    return ""
end function

sub buildLanguageRow()
    r = addRow(m.top.findNode("languageRow"), 0, I18n_T("language.label"))
    m.langButton = r.button
    m.langValue = r.value
    m.langButton.observeField("buttonSelected", "onLanguageSelected")
    showLanguage()
end sub

' The row's value ("Device language (English)", "English", "Español"),
' "· saving…" while the account save is out, and the note when it failed.
sub showLanguage()
    choice = languageChoice()
    if choice = ""
        v = I18n_TP("language.deviceCurrent", { language: I18n_LanguageName(I18n_Resolve("", I18n_DeviceLocale())) })
    else
        v = I18n_LanguageName(choice)
    end if
    saveState = m.global.i18nSave
    if not Prefs_IsString(saveState) then saveState = ""
    if saveState = "saving" then v = I18n_TP("common.valueSaving", { value: v })
    m.langValue.text = v
    note = m.top.findNode("languageNote")
    note.text = I18n_T("language.saveFailed")
    note.visible = (saveState = "failed")
end sub

sub onLanguageSelected()
    choices = languageChoices()
    cur = languageChoice()
    idx = 0
    for i = 0 to choices.Count() - 1
        if choices[i] = cur then idx = i
    end for
    nxt = choices[(idx + 1) mod choices.Count()]
    ' MainScene mounts Settings again, drawn in the new language; this hands
    ' the focus back to the row (onRestoreState).
    m.top.focusState = { focus: "language" }
    getMainScene().callFunc("setUiLanguage", nxt)
end sub

' MainScene: the account save landed (m.global.i18nSave has the result).
sub onLanguageSave()
    showLanguage()
end sub

sub onRestoreState()
    rs = m.top.restoreState
    if rs = invalid or not Prefs_IsString(rs.focus) or rs.focus <> "language" then return
    m.top.initialFocus = m.langButton
    m.langButton.setFocus(true)
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
