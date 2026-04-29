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
    if m.debounceTimer <> invalid
        m.debounceTimer.unobserveField("fire")
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
    timer = CreateObject("roSGNode", "Timer")
    timer.duration = 0.3
    timer.repeat = false
    timer.observeField("fire", "onDebounceFire")
    timer.control = "start"
    m.top.appendChild(timer)
    m.debounceTimer = timer
end sub

sub onDebounceFire()
    m.task.query = m.queryField.text
    m.task.control = "RUN"
    m.status.text = "Searching…"
end sub

sub onSearchState()
    if m.task.state <> "done" then return
    m.rawResults = m.task.result
    if m.rawResults = invalid then m.rawResults = []
    renderResults()
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

    root = createObject("roSGNode", "ContentNode")
    row = root.createChild("ContentNode")
    for each r in visible
        node = row.createChild("ContentNode")
        node.title = r.title
        node.id = r.id
        node.addField("itemType", "string", false)
        node.itemType = r.type
        artPath = invalid
        if r.poster_path <> invalid then artPath = r.poster_path
        if artPath = invalid and r.thumb_path <> invalid then artPath = r.thumb_path
        if artPath <> invalid and serverUrl <> invalid and token <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 400, token)
        end if
    end for
    m.results.content = root
end sub

sub onResultSelected()
    selected = m.results.rowItemSelected
    if selected = invalid then return
    rowIdx = selected[0]
    itemIdx = selected[1]
    rowNode = m.results.content.getChild(rowIdx)
    if rowNode = invalid then return
    item = rowNode.getChild(itemIdx)
    if item = invalid then return
    ' Type-aware routing — same model HomeScene uses. Containers
    ' drill into DetailScene; leaves go straight to playback.
    if isDetailType(item.itemType)
        getMainScene().callFunc("navigateToWithItem", "DetailScene", item.id)
    else
        getMainScene().callFunc("navigateToWithItem", "PlayerScene", item.id)
    end if
end sub

function isDetailType(t as String) as Boolean
    return t = "show" or t = "season" or t = "artist" or t = "album" or t = "podcast" or t = "audiobook" or t = "movie"
end function

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
