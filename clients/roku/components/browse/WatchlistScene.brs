' WatchlistScene controller. On mount: GET /api/v1/watchlist/counts, then
' GET /api/v1/watchlist?status=… for each status that has items, one after
' another, then one RowList render. Row building + labels live in
' source/util/Watchlist.brs (unit-tested).
'
' Every request goes through ApiCallTask (Client_RequestSync), the shared
' path with the 401 refresh + single replay. The calls are chained, never
' parallel, so this screen has at most one request — and so at most one
' token refresh — in flight at a time.

sub init()
    m.rows = m.top.findNode("rows")
    m.empty = m.top.findNode("empty")

    ' Statuses to fetch ([{ status, label, total }]), the next one's index,
    ' and the rows loaded so far ([{ title, items }]).
    m.rowDefs = []
    m.nextRow = 0
    m.loaded = []
    m.failedRows = 0
    ' The in-flight ApiCallTask, held so it isn't collected mid-request.
    m.call = invalid

    m.rows.observeField("rowItemSelected", "onCardSelected")

    startCall({ action: "counts" }, ApiWatchlistCounts())
end sub

sub startCall(ctx as Object, path as String)
    t = createObject("roSGNode", "ApiCallTask")
    t.method = "GET"
    t.path = path
    t.context = ctx
    t.observeField("state", "onCallDone")
    m.call = t
    t.control = "RUN"
end sub

sub onCallDone(evt as Object)
    t = evt.getRoSGNode()
    if t.state <> "done" then return
    m.call = invalid
    ctx = t.context
    res = t.result
    if ctx = invalid then return
    if ctx.action = "counts"
        onCountsDone(res)
    else if ctx.action = "row"
        onRowDone(res, ctx)
    end if
end sub

sub onCountsDone(res as Dynamic)
    if res = invalid or res.ok <> true
        showMessage("Couldn't load your watchlist. Check your connection and try again.")
        return
    end if
    m.rowDefs = Watchlist_RowsFromCounts(res.data)
    if m.rowDefs.Count() = 0
        showEmpty()
        return
    end if
    m.nextRow = 0
    fetchNextRow()
end sub

sub fetchNextRow()
    if m.nextRow >= m.rowDefs.Count()
        finishLoading()
        return
    end if
    def = m.rowDefs[m.nextRow]
    startCall({ action: "row", index: m.nextRow }, ApiWatchlist(def.status))
end sub

sub onRowDone(res as Dynamic, ctx as Object)
    idx = ctx.index
    if idx <> invalid and idx >= 0 and idx < m.rowDefs.Count()
        def = m.rowDefs[idx]
        if res <> invalid and res.ok = true
            items = Watchlist_Items(res.data)
            ' The counts can be a moment stale; a status that turned out
            ' empty is left out like any other.
            if items.Count() > 0
                m.loaded.push({ title: Watchlist_RowTitle(def.label, def.total), items: items })
            end if
        else
            m.failedRows = m.failedRows + 1
        end if
    end if
    m.nextRow = m.nextRow + 1
    fetchNextRow()
end sub

sub finishLoading()
    if m.loaded.Count() = 0
        if m.failedRows > 0
            showMessage("Couldn't load your watchlist. Check your connection and try again.")
        else
            showEmpty()
        end if
        return
    end if
    m.empty.visible = false
    renderRows()
    m.rows.setFocus(true)
end sub

sub showEmpty()
    showMessage("Nothing on your watchlist yet — use Status on a title's page to add it.")
end sub

sub showMessage(text as String)
    m.empty.text = text
    m.empty.visible = true
end sub

' Build the RowList tree: root → one row per status → one card per item.
' Rows whose cards carry a second line get the extra height for it (same
' as HomeScene).
sub renderRows()
    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()
    root = createObject("roSGNode", "ContentNode")
    heights = []
    for each r in m.loaded
        row = root.createChild("ContentNode")
        row.title = r.title
        hasSubtitle = false
        for each it in r.items
            node = row.createChild("ContentNode")
            node.title = WatchState_Str(it.title)
            node.id = it.id
            node.addField("itemType", "string", false)
            node.itemType = WatchState_Str(it.type)
            subtitle = Watchlist_CardSubtitle(it)
            if subtitle <> "" then hasSubtitle = true
            ' Watched check from watch_state (the listing carries no
            ' resume offset, so no progress bar).
            CardFields_Apply(node, it, subtitle)
            artPath = invalid
            if it.poster_path <> invalid then artPath = it.poster_path
            if artPath = invalid and it.thumb_path <> invalid then artPath = it.thumb_path
            if artPath <> invalid and serverUrl <> invalid and token <> invalid
                node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 500, Prefs_GetAssetTokenStr())
            end if
        end for
        if hasSubtitle
            heights.push(420)
        else
            heights.push(380)
        end if
    end for
    m.rows.rowHeights = heights
    m.rows.content = root
end sub

sub onCardSelected()
    selected = m.rows.rowItemSelected
    if selected = invalid then return
    rowNode = m.rows.content.getChild(selected[0])
    if rowNode = invalid then return
    item = rowNode.getChild(selected[1])
    if item = invalid then return
    getMainScene().callFunc("navigateToWithItem", Watchlist_Destination(item.itemType), item.id)
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
