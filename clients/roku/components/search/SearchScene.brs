' SearchScene controller. Mirrors the Android SearchFragment +
' filter-chip flow: Movies / TV Shows / Episodes / Tracks toggles
' (web defaults: movie + show on, episode + track off), persisted
' to the registry under a dedicated key.
'
' On query change the SearchTask fires after a 300 ms debounce so
' fast typing doesn't flood the search endpoint. Result filtering
' against the active chip set happens client-side so toggling a
' chip is instant — no extra network round-trip.

sub init()
    m.queryField = m.top.findNode("queryField")
    m.results = m.top.findNode("results")
    m.status = m.top.findNode("status")
    m.task = m.top.findNode("searchTask")

    m.chipMovie = m.top.findNode("chipMovie")
    m.chipIndex = 0
    m.chipShow = m.top.findNode("chipShow")
    m.chipEpisode = m.top.findNode("chipEpisode")
    m.chipTrack = m.top.findNode("chipTrack")

    ' Defaults match the web client; load persisted prefs over the
    ' top so a user who toggled episodes on once doesn't have to
    ' do it again on every search.
    m.filters = { movie: true, show: true, episode: false, track: false }
    loadFilters()
    refreshChipLabels()

    m.rawResults = []
    m.debounceTimer = invalid

    m.task.observeField("state", "onSearchState")
    m.queryField.observeField("text", "onQueryChange")
    m.results.observeField("rowItemSelected", "onResultSelected")

    m.chipMovie.observeField("buttonSelected", "onChipMovie")
    m.chipShow.observeField("buttonSelected", "onChipShow")
    m.chipEpisode.observeField("buttonSelected", "onChipEpisode")
    m.chipTrack.observeField("buttonSelected", "onChipTrack")

    m.queryField.setFocus(true)
    m.top.initialFocus = m.queryField
    m.restore = invalid
    m.top.observeField("restoreState", "onRestoreState")
    ' Search in: "" is every library. Lasts as long as the screen (Back
    ' from a result hands it back with the query).
    m.scopeId = ""
    m.scopeName = ""
    m.libraries = invalid
    m.scopeBtn = m.top.findNode("scopeBtn")
    m.scopeBtn.observeField("buttonSelected", "onScopePressed")
end sub

' ── Filter chips ───────────────────────────────────────────────────

sub onChipMovie() : m.filters.movie = not m.filters.movie : afterChipToggle() : end sub
sub onChipShow() : m.filters.show = not m.filters.show : afterChipToggle() : end sub
sub onChipEpisode() : m.filters.episode = not m.filters.episode : afterChipToggle() : end sub
sub onChipTrack() : m.filters.track = not m.filters.track : afterChipToggle() : end sub

sub afterChipToggle()
    refreshChipLabels()
    saveFilters()
    renderResults()
end sub

sub refreshChipLabels()
    m.chipMovie.text = chipLabel("Movies", m.filters.movie)
    m.chipShow.text = chipLabel("TV Shows", m.filters.show)
    m.chipEpisode.text = chipLabel("Episodes", m.filters.episode)
    m.chipTrack.text = chipLabel("Tracks", m.filters.track)
end sub

function chipLabel(name as String, checked as Boolean) as String
    if checked then return "✓ " + name
    return name
end function

sub loadFilters()
    section = CreateObject("roRegistrySection", PrefsSection())
    if section.Exists("search_filter_movie") then m.filters.movie = (section.Read("search_filter_movie") = "1")
    if section.Exists("search_filter_show") then m.filters.show = (section.Read("search_filter_show") = "1")
    if section.Exists("search_filter_episode") then m.filters.episode = (section.Read("search_filter_episode") = "1")
    if section.Exists("search_filter_track") then m.filters.track = (section.Read("search_filter_track") = "1")
end sub

sub saveFilters()
    section = CreateObject("roRegistrySection", PrefsSection())
    section.Write("search_filter_movie", boolStr(m.filters.movie))
    section.Write("search_filter_show", boolStr(m.filters.show))
    section.Write("search_filter_episode", boolStr(m.filters.episode))
    section.Write("search_filter_track", boolStr(m.filters.track))
    section.Flush()
end sub

function boolStr(b as Boolean) as String
    if b then return "1"
    return "0"
end function

' ── Search debounce + query lifecycle ──────────────────────────────

sub onQueryChange()
    ' Reuse a single debounce Timer node across keystrokes — restart
    ' it on each change instead of minting a fresh node every char.
    ' The previous code appendChild'd a new Timer per keystroke and
    ' never removed the old ones, leaking a child node per character.
    if m.debounceTimer = invalid
        m.debounceTimer = CreateObject("roSGNode", "Timer")
        m.debounceTimer.duration = 0.3
        m.debounceTimer.repeat = false
        m.debounceTimer.observeField("fire", "onDebounceFire")
        m.top.appendChild(m.debounceTimer)
    else
        ' Stop a pending countdown so a fast typist doesn't fire the
        ' search on the previous keystroke's timer.
        m.debounceTimer.control = "stop"
    end if

    q = m.queryField.text
    if q = invalid then q = ""
    if Len(q) < 2
        m.rawResults = []
        renderResults()
        m.status.text = "Type at least 2 characters."
        m.status.visible = true
        m.results.visible = false
        return
    end if
    m.debounceTimer.control = "start"
end sub

sub onDebounceFire()
    m.task = renewTask(m.task, "SearchTask", "onSearchState")
    m.task.query = m.queryField.text
    m.task.libraryId = m.scopeId
    m.task.control = "RUN"
    m.status.text = "Searching…"
end sub

sub onSearchState()
    if m.task.state <> "done" then return
    m.rawResults = m.task.result
    if m.rawResults = invalid then m.rawResults = []
    renderResults()
    restoreResultFocus()
end sub

' ── Result render ──────────────────────────────────────────────────

' Apply the chip mask to the raw result list. Same piggybacking
' rules the web client uses: season → show, artist + album → track,
' unknown types fall through.
function visibleResults() as Object
    out = []
    for each r in m.rawResults
        if matchesFilter(r.type) then out.push(r)
    end for
    return out
end function

function matchesFilter(t as String) as Boolean
    if t = "movie" then return m.filters.movie
    if t = "show" or t = "season" then return m.filters.show
    if t = "episode" then return m.filters.episode
    if t = "artist" or t = "album" or t = "track" then return m.filters.track
    return true
end function

sub renderResults()
    visible = visibleResults()
    if visible.Count() = 0
        m.results.visible = false
        if m.rawResults.Count() > 0
            m.status.text = m.rawResults.Count().ToStr() + " match(es) hidden by the type filters above."
        else
            m.status.text = "No results."
        end if
        m.status.visible = true
        return
    end if
    m.status.visible = false
    m.results.visible = true

    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()

    ' A grid beside the keyboard: rows of five.
    root = createObject("roSGNode", "ContentNode")
    row = invalid
    n = 0
    for each r in visible
        if n mod 5 = 0 then row = root.createChild("ContentNode")
        n = n + 1
        node = row.createChild("ContentNode")
        node.title = r.title
        node.id = r.id
        node.addField("itemType", "string", false)
        node.itemType = r.type
        artPath = invalid
        if r.poster_path <> invalid then artPath = r.poster_path
        if artPath = invalid and r.thumb_path <> invalid then artPath = r.thumb_path
        if artPath <> invalid and serverUrl <> invalid and token <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 400, Prefs_GetAssetTokenStr())
        end if
    end for
    m.results.content = root
end sub

' Moving between the three areas; each control keeps the keys it uses and
' passes on presses off its edges. Keyboard: Up to the chips, Right to the
' results. Chips: Left / Right between them, Down to the keyboard. Results:
' Left off the first column or Up off the top row back to the keyboard.
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    chips = [m.chipMovie, m.chipShow, m.chipEpisode, m.chipTrack]
    if m.queryField.isInFocusChain()
        if key = "up"
            chips[m.chipIndex].setFocus(true)
            return true
        end if
        if key = "right" and m.results.visible
            m.results.setFocus(true)
            return true
        end if
        return false
    end if
    for i = 0 to chips.Count() - 1
        if chips[i].hasFocus()
            if key = "left" and i > 0 then i = i - 1
            if key = "right" and i < chips.Count() - 1 then i = i + 1
            if key = "left" or key = "right"
                m.chipIndex = i
                chips[i].setFocus(true)
                return true
            end if
            if key = "down"
                m.queryField.setFocus(true)
                return true
            end if
            if key = "up"
                m.scopeBtn.setFocus(true)
                return true
            end if
            return false
        end if
    end for
    if m.scopeBtn.hasFocus()
        if key = "down"
            chips[m.chipIndex].setFocus(true)
            return true
        end if
        return key = "up" or key = "left" or key = "right"
    end if
    if m.results.isInFocusChain() and (key = "left" or key = "up")
        m.queryField.setFocus(true)
        return true
    end if
    return false
end function

sub onResultSelected()
    selected = m.results.rowItemSelected
    if selected = invalid then return
    rowIdx = selected[0]
    itemIdx = selected[1]
    rowNode = m.results.content.getChild(rowIdx)
    if rowNode = invalid then return
    item = rowNode.getChild(itemIdx)
    if item = invalid then return
    routeBySearchType(item.itemType, item.id)
end sub

' Type-aware routing — same model HomeScene + the other browse
' scenes use. Photos drop into the full-screen viewer; collections
' drill into their item grid; containers go to DetailScene; leaves
' go straight to playback.
sub routeBySearchType(itemType as String, itemId as String)
    if itemType = "photo"
        saveFocus()
        getMainScene().callFunc("navigateToWithItem", "PhotoScene", itemId)
        return
    end if
    if itemType = "collection" or itemType = "playlist"
        saveFocus()
        getMainScene().callFunc("navigateToWithItem", "CollectionScene", itemId)
        return
    end if
    if itemType = "show" or itemType = "season" or itemType = "artist" or itemType = "album" or itemType = "podcast" or itemType = "audiobook" or itemType = "book_author" or itemType = "book_series" or itemType = "book" or itemType = "movie"
        saveFocus()
        getMainScene().callFunc("navigateToWithItem", "DetailScene", itemId)
    else
        saveFocus()
        getMainScene().callFunc("navigateToWithItem", "PlayerScene", itemId)
    end if
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function

' A fresh Task for every run: on a device a Task that has finished
' ("done") never runs again when set to RUN (a second search hung on
' "Searching..."; the library's second page never came).
function renewTask(old as Dynamic, component as String, callback as String) as Object
    if old <> invalid then old.unobserveField("state")
    t = createObject("roSGNode", component)
    t.observeField("state", callback)
    return t
end function

' ── Back to the query and the result the viewer opened ─────────────

sub saveFocus()
    st = { query: m.queryField.text, scopeId: m.scopeId, scopeName: m.scopeName }
    f = m.results.rowItemSelected
    if f <> invalid and f.Count() >= 2
        st.row = f[0]
        st.item = f[1]
    end if
    m.top.focusState = st
end sub

' The query goes back in the box (which searches again); the result is
' focused when the answer lands.
sub onRestoreState()
    rs = m.top.restoreState
    if rs <> invalid and rs.scopeId <> invalid and rs.scopeId <> ""
        m.scopeId = rs.scopeId
        m.scopeName = rs.scopeName
        updateScopeButton()
    end if
    if rs = invalid or rs.query = invalid or rs.query = "" then return
    m.restore = rs
    m.queryField.text = rs.query
end sub

sub restoreResultFocus()
    rs = m.restore
    m.restore = invalid
    if rs = invalid or rs.row = invalid or not m.results.visible or m.results.content = invalid then return
    rows = m.results.content.getChildCount()
    if rows = 0 then return
    r = rs.row
    if r >= rows then r = rows - 1
    n = m.results.content.getChild(r).getChildCount()
    i = rs.item
    if i >= n then i = n - 1
    if i < 0 then i = 0
    m.results.jumpToRowItem = [r, i]
    m.results.setFocus(true)
end sub

' ── Search in (library scope) ──────────────────────────────────────

sub updateScopeButton()
    if m.scopeId = "" then m.scopeBtn.text = "Search in: All libraries" else m.scopeBtn.text = "Search in: " + m.scopeName
end sub

' The libraries come from the server the first time, then open as a menu.
sub onScopePressed()
    if m.libraries <> invalid
        showScopeMenu()
        return
    end if
    m.libTask = createObject("roSGNode", "ListFetchTask")
    m.libTask.observeField("state", "onLibrariesLoaded")
    m.libTask.path = ApiLibraries()
    m.libTask.control = "RUN"
end sub

sub onLibrariesLoaded()
    if m.libTask = invalid or m.libTask.state <> "done" then return
    rows = m.libTask.result
    m.libTask = invalid
    m.libraries = []
    if rows <> invalid
        for each l in rows
            if l <> invalid and l.id <> invalid and l.name <> invalid then m.libraries.push({ id: l.id, name: l.name })
        end for
    end if
    showScopeMenu()
end sub

sub showScopeMenu()
    labels = ["All libraries"]
    for each l in m.libraries
        labels.push(l.name)
    end for
    Menu_Show("scope", "Search in", "", labels)
end sub

sub onMenuChoice(menuId as String, index as Integer)
    m.scopeBtn.setFocus(true)
    if menuId <> "scope" or index < 0 then return
    id = ""
    name = ""
    if index > 0
        id = m.libraries[index - 1].id
        name = m.libraries[index - 1].name
    end if
    if id = m.scopeId then return
    m.scopeId = id
    m.scopeName = name
    updateScopeButton()
    ' Search again in the new scope; the old scope's results go now.
    q = m.queryField.text
    if q <> invalid and Len(q) >= 2
        m.rawResults = []
        renderResults()
        onDebounceFire()
    end if
end sub
