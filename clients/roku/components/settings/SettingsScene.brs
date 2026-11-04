' See SettingsScene.xml. Sign out and Forget server revoke the session on
' its server first (LogoutTask, which also clears the tokens), then go to
' Sign in or to server setup. Playback rows load from GET
' /users/me/preferences; OK on one moves it to its next value and saves
' straight away: languages through PUT /users/me/preferences, forced
' subtitles and the quality limits through PUT /users/me/quality-profile
' (which replaces the whole profile, so every field is sent). One save of
' each kind in flight; a change made meanwhile is sent when it lands.

sub init()
    m.signOutBtn = m.top.findNode("signOutBtn")
    m.forgetBtn = m.top.findNode("forgetBtn")
    m.logout = invalid
    m.after = ""

    user = Prefs_Get(PrefsKeyUsername())
    if Prefs_IsNonEmptyString(user)
        m.top.findNode("userLine").text = "Signed in as " + user
    else
        m.top.findNode("userLine").text = "Signed in"
    end if
    url = Prefs_GetServerUrl()
    if url = invalid then url = ""
    m.top.findNode("serverLine").text = "Server: " + url
    m.top.findNode("insecureLine").visible = (LCase(Left(url, 7)) = "http://")
    m.top.findNode("versionLine").text = "OnScreen for Roku " + CreateObject("roAppInfo").GetVersion()

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
    m.prefsTask = m.top.findNode("prefsTask")
    m.prefsTask.observeField("state", "onPrefsLoaded")
    m.prefsTask.control = "RUN"
end sub

' ── Playback ─────────────────────────────────────────────────────────

' The rows, top to bottom: { key, title, options, kind ("lang" | "quality") }.
function playbackRowDefs() as Object
    defs = [
        { key: "preferred_audio_lang", title: "Audio language", options: SettingsPrefs_LangOptions(), kind: "lang" },
        { key: "preferred_subtitle_lang", title: "Subtitle language", options: SettingsPrefs_SubOptions(), kind: "lang" },
        { key: "forced_subtitles_only", title: "Forced subtitles only", options: [{ value: false, label: "Off" }, { value: true, label: "On" }], kind: "quality" }
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
        g = holder.createChild("Group")
        g.translation = [0, y]
        btn = g.createChild("OsButton")
        btn.minWidth = 800
        btn.maxWidth = 800
        btn.text = " "
        btn.observeField("buttonSelected", "onRowSelected")
        title = g.createChild("Label")
        title.translation = [28, 0]
        title.height = 68
        title.vertAlign = "center"
        title.text = d.title
        value = g.createChild("Label")
        value.translation = [28, 0]
        value.width = 744
        value.height = 68
        value.horizAlign = "right"
        value.vertAlign = "center"
        value.color = "0xE8E8EEFF"
        value.text = "..."
        m.rows.push({ def: d, button: btn, value: value })
        y = y + 80
    end for
end sub

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
        m.top.findNode("ratingLine").text = "Content rating limit: " + rating + ". Set by an administrator."
    else
        m.top.findNode("ratingLine").text = "No content rating limit."
    end if
    m.loaded = true
end sub

sub showRowValue(row as Object)
    k = row.def.key
    if k = "forced_subtitles_only"
        if m.values[k] = true then row.value.text = "On" else row.value.text = "Off"
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
    Menu_Show("signOut", "Sign out?", "Your session on this TV ends. The server address is kept.", ["Sign out", "Cancel"])
end sub

sub onForgetPressed()
    Menu_Show("forget", "Forget this server?", "You will be signed out and asked for a server address again.", ["Forget server", "Cancel"])
end sub

sub onMenuChoice(menuId as String, index as Integer)
    if index <> 0
        if menuId = "forget" then m.forgetBtn.setFocus(true) else m.signOutBtn.setFocus(true)
        return
    end if
    if m.logout <> invalid then return
    m.after = menuId
    if menuId = "signOut" then m.signOutBtn.text = "Signing out..." else m.forgetBtn.text = "Signing out..."
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
' Up / Down along the rows; Back is MainScene's (to Home).
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
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

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
