' DetailScene controller. Caller sets `itemId` on m.top before
' mount; init fires the item + children fetch tasks in parallel,
' then renders a hero + action buttons + children carousel.
'
' Watch state (v2.5): shows and seasons also fetch GET
' /items/{id}/up-next for the Play button and the "Mark all" options;
' marks go through POST / DELETE /items/{id}/watched and reports through
' POST /items/{id}/issues, each on a one-shot ApiCallTask (never HTTP on
' the render thread). Labels and rules live in source/util/WatchState.brs
' and ReportProblem.brs.
'
' Versions (v2.6): an item with two or more versions (files[].edition
' names one; the parts of a multi-part movie are one version — see
' source/playback/Versions.brs) gets a Version button; the pick rides to
' PlayerScene as its fileId.
'
' Music (v2.6): an artist page adds "Similar artists" and "Appears on"
' rows (GET /music/artists/{id}/similar, /appears-on) under its albums;
' artist and album pages get an Instant mix button (GET /music/mix?seed=)
' that plays the mix as PlayerScene's queue. Rows are tracked by kind in
' m.rowKinds ("children", "similar", "appears").
'
' Watchlist status (v2.6): movies, shows, seasons, episodes, albums,
' artists, audiobooks and books get a Status button once GET
' /items/{id}/watch-status lands ("Add to Watchlist" / "Status: Watching");
' OK lists the five statuses, and a pick PUTs or DELETEs the same path. The
' button keeps its old value until the write succeeds. Labels and parsing
' live in source/util/Watchlist.brs.

sub init()
    m.fanart = m.top.findNode("fanart")
    m.title = m.top.findNode("title")
    m.logo = m.top.findNode("logo")
    m.meta = m.top.findNode("meta")
    m.summary = m.top.findNode("summary")
    m.playBtn = m.top.findNode("playBtn")
    m.versionBtn = m.top.findNode("versionBtn")
    m.mixBtn = m.top.findNode("mixBtn")
    m.markBtn = m.top.findNode("markBtn")
    m.statusBtn = m.top.findNode("statusBtn")
    m.reportBtn = m.top.findNode("reportBtn")
    m.childrenHeader = m.top.findNode("childrenHeader")
    m.childrenList = m.top.findNode("children")
    m.optionsHint = m.top.findNode("optionsHint")
    m.loading = m.top.findNode("loading")

    m.itemTask = m.top.findNode("itemTask")
    m.childrenTask = m.top.findNode("childrenTask")

    m.item = invalid
    m.children = []
    ' An artist's Similar artists / Appears on rows, and the kind of each
    ' row in the list ("children", "similar", "appears").
    m.similar = []
    m.appearsOn = []
    m.rowKinds = []
    m.mixLoading = false

    ' Up-next (shows / seasons): the response, whether it has landed
    ' (a failed call leaves m.upNext invalid but m.upNextLoaded true),
    ' and whether the children row has been moved to its episode yet.
    m.upNext = invalid
    m.upNextLoaded = false
    m.jumpedToUpNext = false

    ' Focus: "buttons" (m.btnIndex into m.visibleButtons) or "children".
    m.focusArea = "buttons"
    m.visibleButtons = []
    m.btnIndex = 0

    ' Menus (components/common/MenuDialog.brs) and what they act on.
    m.menuDialog = invalid
    m.menuId = ""
    m.childMenu = invalid   ' { child, actions[] } for the * menu
    m.reportTarget = invalid ' { id, fileId, title }
    ' Versions of the item (Versions_Group) and the picked one's primary
    ' file id ("" = the first, which is what Play starts by default).
    m.versions = []
    m.selectedFileId = ""
    ' Watchlist status: the current key ("" = none), whether the GET has
    ' landed (the button stays hidden until then, and when it failed), and
    ' whether a PUT / DELETE is in flight.
    m.watchStatus = ""
    m.statusLoaded = false
    m.statusSaving = false
    m.statusMenu = []
    ' In-flight ApiCallTasks, held so they aren't collected mid-request.
    m.pendingCalls = []
    ' Each button's XML width; layoutButtons scales from these.
    m.baseWidths = {}
    for each b in allButtons()
        m.baseWidths[b.id] = b.minWidth
    end for

    m.itemTask.observeField("state", "onItemTaskState")
    m.childrenTask.observeField("state", "onChildrenTaskState")
    m.playBtn.observeField("buttonSelected", "onPlayPressed")
    m.versionBtn.observeField("buttonSelected", "onVersionPressed")
    m.mixBtn.observeField("buttonSelected", "onMixPressed")
    m.markBtn.observeField("buttonSelected", "onMarkPressed")
    m.statusBtn.observeField("buttonSelected", "onStatusPressed")
    m.reportBtn.observeField("buttonSelected", "onReportPressed")
    m.childrenList.observeField("rowItemSelected", "onChildSelected")
    m.childrenList.observeField("rowItemFocused", "onChildFocused")
    m.logo.observeField("loadStatus", "onLogoLoadStatus")
    m.top.observeField("itemId", "onItemIdSet")

    if m.top.itemId <> invalid and m.top.itemId <> ""
        kickoff()
    end if
end sub

sub onItemIdSet()
    if m.top.itemId <> invalid and m.top.itemId <> "" and m.item = invalid
        kickoff()
    end if
end sub

sub kickoff()
    m.itemTask.itemId = m.top.itemId
    m.itemTask.control = "RUN"
    m.childrenTask.itemId = m.top.itemId
    m.childrenTask.control = "RUN"
end sub

sub onItemTaskState()
    if m.itemTask.state <> "done" then return
    item = m.itemTask.result
    if item = invalid or item.id = invalid
        m.loading.text = "Couldn't load item details"
        return
    end if
    firstLoad = (m.item = invalid)
    m.item = item
    renderItem()
    if firstLoad and WatchState_IsContainer(item.type) then fetchUpNext()
    if firstLoad and WatchState_Str(item.type) = "artist" then fetchArtistRows()
    if firstLoad and Watchlist_SupportsStatus(item.type) then fetchWatchStatus()
end sub

sub onChildrenTaskState()
    if m.childrenTask.state <> "done" then return
    list = m.childrenTask.result
    if list = invalid then list = []
    ' book_author children mix book_series + standalone audiobooks.
    ' Surface series first (alphabetical) then standalone books
    ' (year-desc) so the user sees the catalog structure rather
    ' than an arbitrary mixed grid. Other parent types render
    ' children in server-supplied order.
    parentType = ""
    if m.item <> invalid and m.item.type <> invalid then parentType = m.item.type
    if parentType = "book_author"
        m.children = sortAuthorChildren(list)
    else
        m.children = list
    end if
    renderChildren()
    ' Children changed under a Play button whose fallback reads them.
    renderButtons()
end sub

' Sort an author's children: book_series rows by title (server
' returns them roughly in create order; sorting client-side gives a
' deterministic alphabetical view), then standalone audiobook rows by
' year ascending and reversed (newest first). Foreign types drop out
' — the author detail surface is the wrong place for them.
'
' Built-in roArray.sort/sortBy is what's available — no closure-based
' comparator on Roku, so we sort once per bucket then reverse the
' books for year-desc.
function sortAuthorChildren(list as Object) as Object
    series = []
    books = []
    for each c in list
        if c.type = "book_series"
            series.push(c)
        else if c.type = "audiobook"
            books.push(c)
        end if
    end for
    ' "i" flag = case-insensitive on string fields. Year is numeric
    ' so "i" is a no-op on books — added uniformly for parity.
    series.sortBy("title", "i")
    books.sortBy("year", "i")
    ' Reverse the books to get newest-first. Roku's roArray has no
    ' descending-sort flag, so reverse-after-sort is the idiom.
    out = []
    for each s in series
        out.push(s)
    end for
    i = books.Count() - 1
    while i >= 0
        out.push(books[i])
        i = i - 1
    end while
    return out
end function

sub renderItem()
    item = m.item
    if item = invalid then return

    m.loading.visible = false

    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()

    if item.fanart_path <> invalid and serverUrl <> invalid and token <> invalid
        m.fanart.uri = AssetArtwork(serverUrl, item.fanart_path, 1920, Prefs_GetAssetTokenStr())
    else if item.poster_path <> invalid and serverUrl <> invalid and token <> invalid
        ' Fall back to the poster scaled to 1920 — better than a
        ' bare dark rectangle for items without dedicated fanart.
        m.fanart.uri = AssetArtwork(serverUrl, item.poster_path, 1920, Prefs_GetAssetTokenStr())
    end if

    m.title.text = item.title
    renderLogo(item, serverUrl)
    m.meta.text = DetailMeta_Line(item)
    if item.summary <> invalid then m.summary.text = item.summary

    renderButtons()
end sub

' Movies and shows with a clear logo (logo_path, v2.6) show it in place of
' the title text. The PNG is fetched at its original size (no ?w=, which
' would re-encode it as a JPEG without transparency); the title stays up
' until it has loaded and comes back if it fails.
sub renderLogo(item as Object, serverUrl as Dynamic)
    t = WatchState_Str(item.type)
    path = WatchState_Str(item.logo_path)
    if (t <> "movie" and t <> "show") or path = "" or serverUrl = invalid
        m.logo.visible = false
        m.title.visible = true
        return
    end if
    uri = AssetArtworkOriginal(serverUrl, path, Prefs_GetAssetTokenStr())
    if m.logo.uri <> uri then m.logo.uri = uri
end sub

sub onLogoLoadStatus()
    status = m.logo.loadStatus
    if status = "ready"
        ' Shrink the box to the scaled bitmap so the logo sits flush left
        ' (scaleToFit centres it in its box) with its bottom just above the
        ' meta line, whatever its aspect ratio.
        bw = m.logo.bitmapWidth
        bh = m.logo.bitmapHeight
        if bw > 0 and bh > 0
            scale = 640 / bw
            if 170 / bh < scale then scale = 170 / bh
            w = Int(bw * scale)
            h = Int(bh * scale)
            m.logo.width = w
            m.logo.height = h
            m.logo.translation = [0, 70 - h]
        end if
        m.logo.visible = true
        m.title.visible = false
    else if status = "failed"
        m.logo.visible = false
        m.title.visible = true
    end if
end sub

' ── Action buttons ─────────────────────────────────────────────────

' Label, show / hide and lay out Play / Mark / Status / Report from the
' current item, children, up-next and watchlist state. Keeps focus on the
' same button when it survives the re-render.
sub renderButtons()
    item = m.item
    if item = invalid then return
    t = ""
    if item.type <> invalid then t = item.type

    ' Play. Leaves get "Resume Nm" when part-watched; shows / seasons ask
    ' up-next; other containers (album / podcast / multi-file audiobook)
    ' play their first child. book_author + book_series are pure browse
    ' parents — the books grid is the only affordance there.
    playLabel = ""
    if t = "book_author" or t = "book_series"
        playLabel = ""
    else if WatchState_IsContainer(t)
        playLabel = containerPlayLabel()
    else
        playLabel = "Play"
        if item.view_offset_ms <> invalid and item.view_offset_ms > 0 and isLeafType(t)
            mins = Int(item.view_offset_ms / 60000)
            playLabel = "Resume " + mins.ToStr() + "m"
        end if
    end if
    m.playBtn.text = playLabel
    m.playBtn.visible = (playLabel <> "")

    ' Version picker, for a playable item with a choice of versions.
    m.versions = []
    if m.playBtn.visible and isLeafType(t) then m.versions = Versions_Group(item.files)
    if m.versions.Count() > 1
        m.versionBtn.text = "Version: " + Versions_Label(selectedVersion(), selectedVersionIndex() + 1)
        m.versionBtn.visible = true
    else
        m.versionBtn.visible = false
    end if

    ' Mark watched / unwatched.
    markLabel = ""
    if WatchState_IsMarkableLeaf(t)
        if WatchState_IsWatched(item)
            markLabel = "Mark unwatched"
        else
            markLabel = "Mark watched"
        end if
    else if WatchState_IsContainer(t)
        markLabel = WatchState_MarkAllLabel(WatchState_MarkAllOptions(m.upNext))
    end if
    m.markBtn.text = markLabel
    m.markBtn.visible = (markLabel <> "")

    m.mixBtn.visible = (t = "artist" or t = "album")

    ' Watchlist status, once its GET has landed.
    m.statusBtn.text = Watchlist_StatusButtonLabel(m.watchStatus)
    m.statusBtn.visible = (m.statusLoaded and Watchlist_SupportsStatus(t))

    m.reportBtn.visible = ReportProblem_IsReportable(t)

    layoutButtons()
end sub

' Show / season Play label: the up-next action once it lands; "Play"
' while it's loading, and when it failed on a season (the fallback picks
' from the episode list); hidden for "none" and for a show whose up-next
' failed (its children are seasons — nothing to pick from).
function containerPlayLabel() as String
    if not m.upNextLoaded then return "Play"
    action = WatchState_UpNextAction(m.upNext)
    if action <> invalid then return action.label
    if m.upNext <> invalid then return "" ' mode "none": no episodes
    if m.item.type = "season" and WatchState_PickEpisode(m.children) <> invalid then return "Play"
    return ""
end function

' Place the visible buttons left to right (fixed widths from the XML,
' scaled down together when they'd overflow the content width) and keep
' the focus index valid.
sub layoutButtons()
    previous = invalid
    if m.btnIndex >= 0 and m.btnIndex < m.visibleButtons.Count() then previous = m.visibleButtons[m.btnIndex]

    m.visibleButtons = []
    widths = []
    for each b in allButtons()
        if b.visible
            m.visibleButtons.push(b)
            widths.push(m.baseWidths[b.id])
        end if
    end for
    widths = DetailMeta_FitWidths(widths, 24, 1760)
    x = 0
    for i = 0 to m.visibleButtons.Count() - 1
        b = m.visibleButtons[i]
        if b.minWidth <> widths[i]
            b.minWidth = widths[i]
            b.maxWidth = widths[i]
        end if
        b.translation = [x, 0]
        x = x + widths[i] + 24
    end for

    m.btnIndex = 0
    if previous <> invalid
        for i = 0 to m.visibleButtons.Count() - 1
            if m.visibleButtons[i].isSameNode(previous) then m.btnIndex = i
        end for
    end if

    if m.visibleButtons.Count() = 0
        ' Nothing to press (book_author / book_series) — the children row
        ' is the only thing on the page.
        if m.childrenList.visible and not Menu_IsOpen() then focusChildren()
    else if m.focusArea = "buttons" and not Menu_IsOpen()
        m.visibleButtons[m.btnIndex].setFocus(true)
    end if
end sub

' Every action button in row order.
function allButtons() as Object
    return [m.playBtn, m.versionBtn, m.mixBtn, m.markBtn, m.statusBtn, m.reportBtn]
end function

sub focusButtons()
    if m.visibleButtons.Count() = 0 then return
    if m.btnIndex >= m.visibleButtons.Count() then m.btnIndex = m.visibleButtons.Count() - 1
    m.focusArea = "buttons"
    m.visibleButtons[m.btnIndex].setFocus(true)
    m.optionsHint.visible = false
end sub

sub focusChildren()
    m.focusArea = "children"
    m.childrenList.setFocus(true)
    updateOptionsHint()
end sub

sub restoreFocus()
    if m.focusArea = "children" and m.childrenList.visible
        focusChildren()
    else if m.visibleButtons.Count() > 0
        focusButtons()
    else if m.childrenList.visible
        focusChildren()
    end if
end sub

' ── Versions ───────────────────────────────────────────────────────

' Index into m.versions of the picked version (0 when none is picked or
' the pick is gone after a reload).
function selectedVersionIndex() as Integer
    if m.selectedFileId = "" then return 0
    for i = 0 to m.versions.Count() - 1
        if m.versions[i].primary.id = m.selectedFileId then return i
    end for
    return 0
end function

function selectedVersion() as Object
    return m.versions[selectedVersionIndex()]
end function

' The picked version's file id for Play / Report, or "" for the default.
function selectedFileId() as String
    if m.versions.Count() < 2 then return ""
    return selectedVersion().primary.id
end function

sub onVersionPressed()
    if m.versions.Count() < 2 then return
    labels = []
    for i = 0 to m.versions.Count() - 1
        label = Versions_Label(m.versions[i], i + 1)
        if i = selectedVersionIndex() then label = "● " + label
        labels.push(label)
    end for
    labels.push("Cancel")
    Menu_Show("version", "Version", "", labels)
end sub

' ── Music: artist rows + instant mix ───────────────────────────────

sub fetchArtistRows()
    if m.item = invalid then return
    startCall("GET", ApiMusicSimilarArtists(m.item.id), invalid, { action: "similar" })
    startCall("GET", ApiMusicAppearsOn(m.item.id), invalid, { action: "appears" })
end sub

' A failed call (an older server without the endpoints) leaves its row out.
sub onArtistRowDone(res as Dynamic, kind as String)
    rows = []
    if res <> invalid and res.ok = true then rows = Music_Rows(res.data)
    if kind = "similar" then m.similar = rows else m.appearsOn = rows
    renderChildren()
end sub

sub onMixPressed()
    if m.item = invalid or m.mixLoading then return
    m.mixLoading = true
    m.mixBtn.text = "Loading mix…"
    startCall("GET", ApiMusicMix(m.item.id), invalid, { action: "mix" })
end sub

' Play the mix as a queue: its first track now, the rest by auto-advance.
sub onMixDone(res as Dynamic)
    m.mixLoading = false
    m.mixBtn.text = "Instant mix"
    queue = []
    if res <> invalid and res.ok = true then queue = Music_QueueFromMix(res.data)
    if queue.Count() = 0
        msg = "Couldn't make an instant mix. Try again later."
        if res <> invalid and res.ok = true then msg = "There isn't enough music around this yet to make a mix."
        Menu_ShowMessage("mixFailed", "Instant mix", msg)
        return
    end if
    getMainScene().callFunc("navigateToWithParams", "PlayerScene", queue[0].id, { queue: queue })
end sub

' ── Watchlist status ───────────────────────────────────────────────

sub fetchWatchStatus()
    if m.item = invalid then return
    startCall("GET", ApiItemWatchStatus(m.item.id), invalid, { action: "status" })
end sub

' No status ("" or a 404) shows "Add to Watchlist"; a failed read leaves
' the button out rather than offer "Add" over a status that may exist.
sub onWatchStatusDone(res as Dynamic)
    status = Watchlist_StatusFromResult(res)
    if status = invalid then return
    m.watchStatus = status
    m.statusLoaded = true
    renderButtons()
end sub

sub onStatusPressed()
    if m.item = invalid or not m.statusLoaded or m.statusSaving then return
    m.statusMenu = Watchlist_StatusMenu(m.watchStatus)
    labels = []
    for each e in m.statusMenu
        labels.push(e.label)
    end for
    labels.push("Cancel")
    Menu_Show("status", "Watchlist status", WatchState_Str(m.item.title), labels)
end sub

' A pick from the Status menu. Picking the current status changes nothing.
sub applyStatusChoice(index as Integer)
    if index < 0 or index >= m.statusMenu.Count() or m.item = invalid then return
    e = m.statusMenu[index]
    if e.action = "set"
        if e.status = m.watchStatus then return
        m.statusSaving = true
        startCall("PUT", ApiItemWatchStatus(m.item.id), Watchlist_StatusBody(e.status), { action: "statusSet", status: e.status })
    else if e.action = "clear"
        m.statusSaving = true
        startCall("DELETE", ApiItemWatchStatus(m.item.id), invalid, { action: "statusSet", status: "" })
    end if
end sub

' The button moves only once the server has the change; a failure keeps
' the old value and says so.
sub onStatusSetDone(res as Dynamic, ctx as Object)
    m.statusSaving = false
    if res = invalid or res.ok <> true
        Menu_ShowMessage("statusFailed", "Watchlist status", WatchState_WriteErrorText(res, "Couldn't update your watchlist status. Try again later."))
        return
    end if
    status = ctx.status
    ' A PUT echoes the saved row; trust it over the request when present.
    if ctx.status <> ""
        saved = Watchlist_StatusFromResult(res)
        if saved <> invalid and saved <> "" then status = saved
    end if
    m.watchStatus = status
    renderButtons()
end sub

' ── Up next (shows / seasons) ──────────────────────────────────────

sub fetchUpNext()
    if m.item = invalid then return
    startCall("GET", ApiItemUpNext(m.item.id), invalid, { action: "upnext" })
end sub

sub onUpNextDone(res as Dynamic)
    m.upNextLoaded = true
    m.upNext = invalid
    if res <> invalid and res.ok = true and res.data <> invalid and type(res.data) = "roAssociativeArray"
        m.upNext = res.data
    end if
    renderButtons()
    jumpToUpNext()
end sub

' Open the children row on the up-next episode (season page) or its
' season (show page), once, and only while the user hasn't started
' browsing the row themselves.
sub jumpToUpNext()
    if m.jumpedToUpNext or m.focusArea = "children" then return
    if not m.childrenList.visible then return
    action = WatchState_UpNextAction(m.upNext)
    if action = invalid then return
    target = action.episodeId
    if m.item.type = "show" then target = action.seasonId
    idx = WatchState_IndexOfId(m.children, target)
    m.jumpedToUpNext = true
    if idx > 0 then m.childrenList.jumpToRowItem = [0, idx]
end sub

' ── Children row ───────────────────────────────────────────────────

sub renderChildren()
    prevKinds = m.rowKinds
    m.rowKinds = []
    if m.children.Count() > 0 then m.rowKinds.push("children")
    if m.similar.Count() > 0 then m.rowKinds.push("similar")
    if m.appearsOn.Count() > 0 then m.rowKinds.push("appears")
    if m.rowKinds.Count() = 0
        m.childrenHeader.visible = false
        m.childrenList.visible = false
        m.optionsHint.visible = false
        if m.focusArea = "children" then focusButtons()
        return
    end if

    parentType = ""
    if m.item <> invalid then parentType = m.item.type

    ' Episode stills are landscape; everything else is a poster.
    if parentType = "season"
        m.childrenList.rowItemSize = [[320, 180]]
        m.childrenList.rowHeights = [260]
    else
        m.childrenList.rowItemSize = [[200, 260]]
        m.childrenList.rowHeights = [300]
    end if

    ' A refresh (after a watched mark, or a row arriving) keeps the cursor
    ' on the same row and card.
    keepKind = ""
    keepIdx = -1
    focused = m.childrenList.rowItemFocused
    if m.childrenList.content <> invalid and focused <> invalid and focused.Count() > 1
        if focused[0] >= 0 and focused[0] < prevKinds.Count()
            keepKind = prevKinds[focused[0]]
            keepIdx = focused[1]
        end if
    end if

    serverUrl = Prefs_GetServerUrl()

    root = createObject("roSGNode", "ContentNode")
    for each kind in m.rowKinds
        row = root.createChild("ContentNode")
        row.title = rowTitle(kind)
        if kind = "children"
            addChildCards(row, serverUrl)
        else if kind = "similar"
            for each a in m.similar
                addCard(row, a.id, Music_Str(a.title), "artist", a, "", a.poster_path, serverUrl)
            end for
        else if kind = "appears"
            for each t in m.appearsOn
                addCard(row, t.id, Music_Str(t.title), "track", t, Music_AppearanceSubtitle(t), t.album_poster_path, serverUrl)
            end for
        end if
    end for
    m.childrenList.content = root
    m.childrenList.visible = true
    m.childrenHeader.visible = true

    headerRow = 0
    newRow = -1
    for i = 0 to m.rowKinds.Count() - 1
        if m.rowKinds[i] = keepKind then newRow = i
    end for
    if newRow >= 0 and (keepIdx > 0 or newRow > 0)
        rowSize = root.getChild(newRow).getChildCount()
        if keepIdx > rowSize - 1 then keepIdx = rowSize - 1
        if keepIdx < 0 then keepIdx = 0
        m.childrenList.jumpToRowItem = [newRow, keepIdx]
        headerRow = newRow
    end if
    m.childrenHeader.text = rowTitle(m.rowKinds[headerRow])

    ' A page with no buttons (book_author / book_series) starts on the
    ' row. Wait for the item, though: until it lands no buttons exist
    ' yet, and the Play button should win on a show that has them.
    if m.item <> invalid and m.visibleButtons.Count() = 0 and not Menu_IsOpen() then focusChildren()
    jumpToUpNext()
end sub

' Header over a row. The children row's depends on the parent type — same
' wording the Android detail page uses so users see the same mental model
' across surfaces.
function rowTitle(kind as String) as String
    if kind = "similar" then return "Similar artists"
    if kind = "appears" then return "Appears on"
    parentType = ""
    if m.item <> invalid then parentType = WatchState_Str(m.item.type)
    if parentType = "album" then return "Tracks"
    if parentType = "audiobook" then return "Chapters"
    if parentType = "artist" then return "Albums"
    if parentType = "book_author" or parentType = "book_series" then return "Books"
    if parentType = "show" then return "Seasons"
    return "Episodes"
end function

' One card: title, id, itemType (routes the OK press), watch badge /
' subtitle (CardFields) and art.
sub addCard(row as Object, id as String, title as String, itemType as String, data as Object, subtitle as String, artPath as Dynamic, serverUrl as Dynamic)
    node = row.createChild("ContentNode")
    node.title = title
    node.id = id
    node.addField("itemType", "string", false)
    node.itemType = itemType
    CardFields_Apply(node, data, subtitle)
    if artPath <> invalid and serverUrl <> invalid
        node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 400, Prefs_GetAssetTokenStr())
    end if
end sub

sub addChildCards(row as Object, serverUrl as Dynamic)
    token = Prefs_GetAccessToken()
    for each child in m.children
        node = row.createChild("ContentNode")
        ' Episodes + chapters: prefix the index for at-a-glance
        ' ordering ("1. Pilot"). Tracks and other types use the
        ' plain title — index numbers on a Pink Floyd album are
        ' just visual noise next to the track name. A multi-episode
        ' file reads as its range ("1–2. Pilot").
        nums = ""
        if child.type = "episode"
            nums = WatchState_EpisodeNumbers(child.index, child.index_end, "")
        else if child.index <> invalid and child.type = "audiobook_chapter"
            nums = child.index.ToStr()
        end if
        if nums <> ""
            node.title = nums + ". " + child.title
        else
            node.title = child.title
        end if
        node.id = child.id
        node.addField("itemType", "string", false)
        node.itemType = child.type
        ' Watched check / resume bar from the row's watched +
        ' view_offset_ms.
        CardFields_Apply(node, child, "")
        artPath = invalid
        if child.thumb_path <> invalid then artPath = child.thumb_path
        if artPath = invalid and child.poster_path <> invalid then artPath = child.poster_path
        if artPath <> invalid and serverUrl <> invalid and token <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 400, Prefs_GetAssetTokenStr())
        end if
    end for
end sub

' Children of the carousel were OK-pressed — fire the player for
' leaf children (episode / track / audiobook_chapter / podcast_episode),
' or drill into DetailScene for container children that themselves
' have children (a season under a show, an album under an artist, a
' series under an author, an audiobook under a series). Without the type
' branch, a season-card press would hit PlayerScene and bail home on a
' parent with no file.
sub onChildSelected()
    selected = m.childrenList.rowItemSelected
    if selected = invalid then return
    rowIdx = selected[0]
    itemIdx = selected[1]
    rowNode = m.childrenList.content.getChild(rowIdx)
    if rowNode = invalid then return
    childNode = rowNode.getChild(itemIdx)
    if childNode = invalid then return
    childType = ""
    if childNode.itemType <> invalid then childType = childNode.itemType
    if childType = "season" or childType = "album" or childType = "artist" or childType = "book_series" or childType = "audiobook"
        getMainScene().callFunc("navigateToWithItem", "DetailScene", childNode.id)
    else
        getMainScene().callFunc("navigateToWithItem", "PlayerScene", childNode.id)
    end if
end sub

sub onChildFocused()
    ' The header names the row in view (one row shows at a time).
    focused = m.childrenList.rowItemFocused
    if focused <> invalid and focused.Count() > 1 and focused[0] >= 0 and focused[0] < m.rowKinds.Count()
        m.childrenHeader.text = rowTitle(m.rowKinds[focused[0]])
    end if
    updateOptionsHint()
end sub

sub updateOptionsHint()
    m.optionsHint.visible = (m.focusArea = "children" and childMenuActions(focusedChild()).Count() > 0)
end sub

function focusedChild() as Dynamic
    focused = m.childrenList.rowItemFocused
    if focused = invalid or focused.Count() < 2 then return invalid
    ' Only the item's own children have options.
    if focused[0] < 0 or focused[0] >= m.rowKinds.Count() or m.rowKinds[focused[0]] <> "children" then return invalid
    idx = focused[1]
    if idx < 0 or idx >= m.children.Count() then return invalid
    return m.children[idx]
end function

' What the * menu offers on a child card: [{ label, action }]. Episodes
' toggle their mark and can be reported; seasons mark all their
' episodes (the listing carries no season counts, so both ways).
function childMenuActions(child as Dynamic) as Object
    actions = []
    if child = invalid then return actions
    ct = WatchState_Str(child.type)
    if ct = "episode"
        if WatchState_IsWatched(child)
            actions.push({ label: "Mark unwatched", action: "unwatch" })
        else
            actions.push({ label: "Mark watched", action: "watch" })
        end if
    else if ct = "season"
        actions.push({ label: "Mark season watched", action: "watch" })
        actions.push({ label: "Mark season unwatched", action: "unwatch" })
    end if
    if ReportProblem_IsReportable(ct) then actions.push({ label: "Report a problem", action: "report" })
    return actions
end function

sub openChildMenu()
    child = focusedChild()
    actions = childMenuActions(child)
    if actions.Count() = 0 then return
    labels = []
    for each a in actions
        labels.push(a.label)
    end for
    labels.push("Cancel")
    m.childMenu = { child: child, actions: actions }
    Menu_Show("child", WatchState_Str(child.title), "", labels)
end sub

' ── Keys ───────────────────────────────────────────────────────────

' Left / right move between the action buttons; down goes to the
' children row, up comes back. The RowList consumes left / right inside
' the row and lets an unhandled up bubble here. * on a child card opens
' its options. Back is left to MainScene's back-stack.
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if m.focusArea = "buttons"
        n = m.visibleButtons.Count()
        if key = "left" and n > 0
            if m.btnIndex > 0
                m.btnIndex = m.btnIndex - 1
                focusButtons()
            end if
            return true
        end if
        if key = "right" and n > 0
            if m.btnIndex < n - 1
                m.btnIndex = m.btnIndex + 1
                focusButtons()
            end if
            return true
        end if
        if key = "down" and m.childrenList.visible
            focusChildren()
            return true
        end if
        return false
    end if
    if m.focusArea = "children"
        if key = "up" and m.visibleButtons.Count() > 0
            focusButtons()
            return true
        end if
        if key = "options" or key = "*"
            openChildMenu()
            return true
        end if
    end if
    return false
end function

' ── Button handlers ────────────────────────────────────────────────

' Play pressed on the parent. Leaf items play themselves; shows and
' seasons play the up-next episode (a season whose up-next failed picks
' the part-watched / first unwatched / first episode itself); other
' containers pick the first child.
sub onPlayPressed()
    if m.item = invalid then return
    t = m.item.type
    ' book_author + book_series are pure browse parents; the button is
    ' hidden for them in renderButtons.
    if t = "book_author" or t = "book_series" then return
    target = m.item.id
    if WatchState_IsContainer(t)
        action = WatchState_UpNextAction(m.upNext)
        if action <> invalid
            target = action.episodeId
        else
            ep = invalid
            if t = "season" then ep = WatchState_PickEpisode(m.children)
            if ep = invalid then return
            target = ep.id
        end if
    else if not isLeafType(t)
        if m.children.Count() = 0
            return
        end if
        target = m.children[0].id
    end if
    ' A picked version applies to this item's own Play only.
    fileId = ""
    if target = m.item.id then fileId = selectedFileId()
    if fileId <> ""
        getMainScene().callFunc("navigateToWithParams", "PlayerScene", target, { fileId: fileId })
    else
        getMainScene().callFunc("navigateToWithItem", "PlayerScene", target)
    end if
end sub

sub onMarkPressed()
    if m.item = invalid then return
    t = m.item.type
    if WatchState_IsMarkableLeaf(t)
        sendMark(m.item.id, not WatchState_IsWatched(m.item), "item")
        return
    end if
    if not WatchState_IsContainer(t) then return
    opts = WatchState_MarkAllOptions(m.upNext)
    if opts.watched and opts.unwatched
        Menu_Show("markAll", m.item.title, "", ["Mark all watched", "Mark all unwatched", "Cancel"])
    else if opts.watched
        markContainer(true)
    else if opts.unwatched
        markContainer(false)
    end if
end sub

' Whole show / season. Unwatching a whole show wipes every mark and
' resume point in it, so it asks first.
sub markContainer(watched as Boolean)
    if not watched and m.item.type = "show"
        Menu_Show("confirmUnwatchShow", "Mark all unwatched?", WatchState_ConfirmUnwatchShowText(m.item.title), ["Mark all unwatched", "Cancel"])
        return
    end if
    sendMark(m.item.id, watched, "container")
end sub

sub onReportPressed()
    if m.item = invalid then return
    fileId = invalid
    if m.item.files <> invalid and m.item.files.Count() > 0 and m.item.files[0] <> invalid then fileId = m.item.files[0].id
    ' The picked version is the one being reported.
    picked = selectedFileId()
    if picked <> "" then fileId = picked
    openReportMenu({ id: m.item.id, fileId: fileId, title: m.item.title })
end sub

' Kinds as buttons (plus Cancel); the note is skipped on Roku.
sub openReportMenu(target as Object)
    m.reportTarget = target
    labels = []
    for each k in ReportProblem_Kinds()
        labels.push(k.label)
    end for
    labels.push("Cancel")
    Menu_Show("report", "Report a problem", target.title, labels)
end sub

sub onMenuChoice(menuId as String, index as Integer)
    if menuId = "version"
        if index >= 0 and index < m.versions.Count()
            m.selectedFileId = m.versions[index].primary.id
            renderButtons()
        end if
    else if menuId = "markAll"
        if index = 0 then markContainer(true)
        if index = 1 then markContainer(false)
    else if menuId = "confirmUnwatchShow"
        if index = 0 then sendMark(m.item.id, false, "container")
    else if menuId = "child"
        cm = m.childMenu
        m.childMenu = invalid
        if cm <> invalid and index >= 0 and index < cm.actions.Count()
            a = cm.actions[index].action
            if a = "watch" then sendMark(cm.child.id, true, "child")
            if a = "unwatch" then sendMark(cm.child.id, false, "child")
            if a = "report" then openReportMenu({ id: cm.child.id, fileId: invalid, title: WatchState_Str(cm.child.title) })
        end if
    else if menuId = "status"
        applyStatusChoice(index)
    else if menuId = "report"
        kinds = ReportProblem_Kinds()
        if m.reportTarget <> invalid and index >= 0 and index < kinds.Count()
            sendReport(m.reportTarget, kinds[index].value)
        end if
    end if
    ' A follow-up dialog (confirm, report kinds) keeps the key focus;
    ' otherwise hand it back to the page.
    if not Menu_IsOpen() then restoreFocus()
end sub

' ── Writes ─────────────────────────────────────────────────────────

' scope: "item" (this movie / episode page), "container" (this show /
' season), "child" (a card in the children row).
sub sendMark(id as String, watched as Boolean, scope as String)
    verb = "DELETE"
    if watched then verb = "POST"
    startCall(verb, ApiItemWatched(id), invalid, { action: "mark", watched: watched, scope: scope })
end sub

sub onMarkDone(res as Dynamic, ctx as Object)
    if res = invalid or res.ok <> true
        Menu_ShowMessage("markFailed", "Watched", WatchState_WriteErrorText(res, "Couldn't update watched state."))
        return
    end if
    if ctx.scope = "item"
        ' Movie / episode page: the mark also clears the resume point.
        if ctx.watched
            m.item.watch_state = "watched"
        else
            m.item.watch_state = "unwatched"
        end if
        m.item.view_offset_ms = 0
        renderButtons()
        return
    end if
    ' Show / season / child card: badges, the Play label and the Mark
    ' options all move — re-read them.
    m.childrenTask.control = "RUN"
    if WatchState_IsContainer(m.item.type) then fetchUpNext()
end sub

sub sendReport(target as Object, kind as String)
    startCall("POST", ApiItemIssues(target.id), ReportProblem_Body(kind, target.fileId), { action: "report" })
end sub

sub onReportDone(res as Dynamic)
    Menu_ShowMessage("reportResult", "Report a problem", ReportProblem_ResultText(res))
end sub

' One-shot ApiCallTask; the result lands in onApiCallDone.
sub startCall(verb as String, path as String, body as Dynamic, ctx as Object)
    t = createObject("roSGNode", "ApiCallTask")
    t.method = verb
    t.path = path
    if body <> invalid then t.body = body
    t.context = ctx
    t.observeField("state", "onApiCallDone")
    m.pendingCalls.push(t)
    t.control = "RUN"
end sub

sub onApiCallDone(evt as Object)
    t = evt.getRoSGNode()
    if t.state <> "done" then return
    kept = []
    for each p in m.pendingCalls
        if not p.isSameNode(t) then kept.push(p)
    end for
    m.pendingCalls = kept

    ctx = t.context
    res = t.result
    if ctx = invalid then return
    if ctx.action = "upnext"
        onUpNextDone(res)
    else if ctx.action = "similar" or ctx.action = "appears"
        onArtistRowDone(res, ctx.action)
    else if ctx.action = "mix"
        onMixDone(res)
    else if ctx.action = "mark"
        onMarkDone(res, ctx)
    else if ctx.action = "report"
        onReportDone(res)
    else if ctx.action = "status"
        onWatchStatusDone(res)
    else if ctx.action = "statusSet"
        onStatusSetDone(res, ctx)
    end if
end sub

function isLeafType(t as String) as Boolean
    return t = "movie" or t = "episode" or t = "track" or t = "audiobook_chapter" or t = "podcast_episode"
end function

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
