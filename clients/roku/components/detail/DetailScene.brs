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

sub init()
    m.fanart = m.top.findNode("fanart")
    m.title = m.top.findNode("title")
    m.meta = m.top.findNode("meta")
    m.summary = m.top.findNode("summary")
    m.playBtn = m.top.findNode("playBtn")
    m.markBtn = m.top.findNode("markBtn")
    m.reportBtn = m.top.findNode("reportBtn")
    m.childrenHeader = m.top.findNode("childrenHeader")
    m.childrenList = m.top.findNode("children")
    m.optionsHint = m.top.findNode("optionsHint")
    m.loading = m.top.findNode("loading")

    m.itemTask = m.top.findNode("itemTask")
    m.childrenTask = m.top.findNode("childrenTask")

    m.item = invalid
    m.children = []

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
    ' In-flight ApiCallTasks, held so they aren't collected mid-request.
    m.pendingCalls = []

    m.itemTask.observeField("state", "onItemTaskState")
    m.childrenTask.observeField("state", "onChildrenTaskState")
    m.playBtn.observeField("buttonSelected", "onPlayPressed")
    m.markBtn.observeField("buttonSelected", "onMarkPressed")
    m.reportBtn.observeField("buttonSelected", "onReportPressed")
    m.childrenList.observeField("rowItemSelected", "onChildSelected")
    m.childrenList.observeField("rowItemFocused", "onChildFocused")
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
    m.meta.text = buildMetaLine(item)
    if item.summary <> invalid then m.summary.text = item.summary

    renderButtons()
end sub

' ── Action buttons ─────────────────────────────────────────────────

' Label, show / hide and lay out Play / Mark / Report from the current
' item, children and up-next state. Keeps focus on the same button when
' it survives the re-render.
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

' Place the visible buttons left to right (fixed widths from the XML) and
' keep the focus index valid.
sub layoutButtons()
    previous = invalid
    if m.btnIndex >= 0 and m.btnIndex < m.visibleButtons.Count() then previous = m.visibleButtons[m.btnIndex]

    m.visibleButtons = []
    x = 0
    for each b in [m.playBtn, m.markBtn, m.reportBtn]
        if b.visible
            b.translation = [x, 0]
            x = x + b.minWidth + 24
            m.visibleButtons.push(b)
        end if
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
    if m.children.Count() = 0
        m.childrenHeader.visible = false
        m.childrenList.visible = false
        m.optionsHint.visible = false
        if m.focusArea = "children" then focusButtons()
        return
    end if

    ' Header label depends on the parent type — same wording the
    ' Android detail page uses so users see the same mental model
    ' across surfaces.
    parentType = ""
    if m.item <> invalid then parentType = m.item.type
    if parentType = "album"
        m.childrenHeader.text = "Tracks"
    else if parentType = "audiobook"
        m.childrenHeader.text = "Chapters"
    else if parentType = "artist"
        m.childrenHeader.text = "Albums"
    else if parentType = "book_author" or parentType = "book_series"
        m.childrenHeader.text = "Books"
    else if parentType = "show"
        m.childrenHeader.text = "Seasons"
    else
        m.childrenHeader.text = "Episodes"
    end if
    m.childrenHeader.visible = true

    ' Episode stills are landscape; everything else is a poster.
    if parentType = "season"
        m.childrenList.rowItemSize = [[320, 180]]
        m.childrenList.rowHeights = [260]
    else
        m.childrenList.rowItemSize = [[200, 260]]
        m.childrenList.rowHeights = [300]
    end if

    ' A refresh (after a watched mark) keeps the cursor where it was.
    keepIdx = -1
    if m.childrenList.content <> invalid and m.childrenList.rowItemFocused <> invalid and m.childrenList.rowItemFocused.Count() > 1
        keepIdx = m.childrenList.rowItemFocused[1]
    end if

    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()

    root = createObject("roSGNode", "ContentNode")
    row = root.createChild("ContentNode")
    for each child in m.children
        node = row.createChild("ContentNode")
        ' Episodes + chapters: prefix the index for at-a-glance
        ' ordering ("1. Pilot"). Tracks and other types use the
        ' plain title — index numbers on a Pink Floyd album are
        ' just visual noise next to the track name.
        if child.index <> invalid and (child.type = "episode" or child.type = "audiobook_chapter")
            node.title = child.index.ToStr() + ". " + child.title
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
    m.childrenList.content = root
    m.childrenList.visible = true
    if keepIdx > 0
        if keepIdx > m.children.Count() - 1 then keepIdx = m.children.Count() - 1
        m.childrenList.jumpToRowItem = [0, keepIdx]
    end if

    ' A page with no buttons (book_author / book_series) starts on the
    ' row. Wait for the item, though: until it lands no buttons exist
    ' yet, and the Play button should win on a show that has them.
    if m.item <> invalid and m.visibleButtons.Count() = 0 and not Menu_IsOpen() then focusChildren()
    jumpToUpNext()
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
    if childType = "season" or childType = "album" or childType = "book_series" or childType = "audiobook"
        getMainScene().callFunc("navigateToWithItem", "DetailScene", childNode.id)
    else
        getMainScene().callFunc("navigateToWithItem", "PlayerScene", childNode.id)
    end if
end sub

sub onChildFocused()
    updateOptionsHint()
end sub

sub updateOptionsHint()
    m.optionsHint.visible = (m.focusArea = "children" and childMenuActions(focusedChild()).Count() > 0)
end sub

function focusedChild() as Dynamic
    focused = m.childrenList.rowItemFocused
    if focused = invalid or focused.Count() < 2 then return invalid
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
    getMainScene().callFunc("navigateToWithItem", "PlayerScene", target)
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
    if menuId = "markAll"
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
    else if ctx.action = "mark"
        onMarkDone(res, ctx)
    else if ctx.action = "report"
        onReportDone(res)
    end if
end sub

function isLeafType(t as String) as Boolean
    return t = "movie" or t = "episode" or t = "track" or t = "audiobook_chapter" or t = "podcast_episode"
end function

function buildMetaLine(item as Object) as String
    parts = []
    if item.year <> invalid then parts.push(item.year.ToStr())
    if item.content_rating <> invalid and item.content_rating <> "" then parts.push(item.content_rating)
    if item.duration_ms <> invalid and item.duration_ms > 0
        mins = Int(item.duration_ms / 60000)
        if mins >= 60
            h = Int(mins / 60)
            mm = mins mod 60
            parts.push(h.ToStr() + "h " + mm.ToStr() + "m")
        else
            parts.push(mins.ToStr() + "m")
        end if
    end if
    if item.rating <> invalid and item.rating > 0
        parts.push("★ " + Str(item.rating))
    end if
    if item.genres <> invalid and item.genres.Count() > 0
        parts.push(item.genres[0])
    end if
    out = ""
    for i = 0 to parts.Count() - 1
        if i > 0 then out = out + "  ·  "
        out = out + parts[i]
    end for
    return out
end function

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
