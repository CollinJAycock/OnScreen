' HomeScene controller. Mounts → fires HubFetchTask → on
' completion, builds a ContentNode tree from the response and
' binds it to the RowList. Pressing OK on a card routes to the
' player (or detail screen when we ship one).
'
' Rows come from HubRows_Build (source/util/HubRows.brs): Continue
' Watching, Next Up, Plan to Watch, Trending, Recently Added. The *
' (options) key on a Continue Watching card offers "Remove from Continue
' Watching" (POST /items/{id}/dismiss-continue-watching); the tile leaves
' the row once the server confirms.

sub init()
    m.rows = m.top.findNode("rows")
    m.loading = m.top.findNode("loading")
    m.optionsHint = m.top.findNode("optionsHint")
    m.hubTask = m.top.findNode("hubTask")
    m.searchBtn = m.top.findNode("searchBtn")
    m.favoritesBtn = m.top.findNode("favoritesBtn")
    m.historyBtn = m.top.findNode("historyBtn")
    m.librariesBtn = m.top.findNode("librariesBtn")

    ' Plain-data rows (HubRows_Build) — rebuilt into ContentNodes on
    ' every change so the RowList always gets a fresh tree.
    m.hubRows = []
    ' The Continue Watching card the options menu is about:
    ' { rowIdx, id }.
    m.menuTarget = invalid
    m.menuDialog = invalid
    m.menuId = ""
    ' In-flight ApiCallTasks, held so they aren't collected mid-request.
    m.pendingCalls = []
    ' Header buttons, left to right. Up from the top row reaches them;
    ' down comes back to the rows.
    m.headerButtons = [m.librariesBtn, m.favoritesBtn, m.historyBtn, m.searchBtn]
    m.headerIdx = 0
    m.inHeader = false

    m.hubTask.observeField("state", "onHubTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")
    m.rows.observeField("rowItemFocused", "onRowItemFocused")
    m.searchBtn.observeField("buttonSelected", "onSearchPressed")
    m.favoritesBtn.observeField("buttonSelected", "onFavoritesPressed")
    m.historyBtn.observeField("buttonSelected", "onHistoryPressed")
    m.librariesBtn.observeField("buttonSelected", "onLibrariesPressed")

    ' Kick off the fetch. Task nodes start when control=RUN.
    m.hubTask.control = "RUN"
end sub

sub onSearchPressed()
    getMainScene().callFunc("navigateTo", "SearchScene")
end sub

sub onFavoritesPressed()
    getMainScene().callFunc("navigateTo", "FavoritesScene")
end sub

sub onHistoryPressed()
    getMainScene().callFunc("navigateTo", "HistoryScene")
end sub

sub onLibrariesPressed()
    getMainScene().callFunc("navigateTo", "LibraryListScene")
end sub

sub onHubTaskState()
    if m.hubTask.state <> "done" then return

    hub = m.hubTask.result
    if hub = invalid or hub.Count() = 0
        m.loading.text = "Couldn't reach OnScreen — check your server URL"
        focusHeader()
        return
    end if

    m.loading.visible = false
    m.hubRows = HubRows_Build(hub)
    renderRows()
    focusRowsOrHeader()
end sub

sub focusHeader()
    m.inHeader = true
    m.optionsHint.visible = false
    m.headerButtons[m.headerIdx].setFocus(true)
end sub

' The rows when there are any (a new server with nothing watched or
' added yet has none), else the header so the page isn't a dead end.
sub focusRowsOrHeader()
    if m.hubRows.Count() = 0
        focusHeader()
        return
    end if
    m.inHeader = false
    m.rows.setFocus(true)
    updateOptionsHint()
end sub

' Build a SceneGraph ContentNode tree from m.hubRows. The RowList
' expects: root ContentNode → row ContentNodes (one per section) → item
' ContentNodes (one per card). Rows whose tiles carry a second line
' (episode tiles: "S2 · E5 — Title") get the extra height for it.
sub renderRows()
    root = createObject("roSGNode", "ContentNode")
    heights = []
    for each r in m.hubRows
        ' Row kinds stay in m.hubRows (same index as the row node) —
        ' focusedContinueTarget reads them there.
        row = root.createChild("ContentNode")
        row.title = r.title
        hasSubtitle = false
        for each item in r.items
            if addItemToRow(row, item) then hasSubtitle = true
        end for
        if hasSubtitle
            heights.push(420)
        else
            heights.push(380)
        end if
    end for
    if heights.Count() = 0 then heights.push(380)
    m.rows.rowHeights = heights
    m.rows.content = root
    updateOptionsHint()
end sub

' Adds the card; returns true when it has a second line.
function addItemToRow(row as Object, item as Object) as Boolean
    if item = invalid then return false
    node = row.createChild("ContentNode")
    text = WatchState_HubTileText(item)
    node.title = text.title
    node.id = item.id
    ' Stash the original item dict on the node so the click handler
    ' can read fields not in the standard ContentNode schema (type,
    ' fileId for stream URL, etc.). SceneGraph nodes accept
    ' arbitrary dynamic fields via setField().
    node.addField("itemType", "string", false)
    node.itemType = item.type
    ' Watch state for the card: Continue Watching tiles carry the resume
    ' point, which PosterCard draws as a progress bar.
    CardFields_Apply(node, item, text.subtitle)
    artPath = item.poster_path
    if artPath = invalid then artPath = item.thumb_path
    if artPath <> invalid
        token = Prefs_GetAccessToken()
        serverUrl = Prefs_GetServerUrl()
        if token <> invalid and serverUrl <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 500, Prefs_GetAssetTokenStr())
        end if
    end if
    return text.subtitle <> ""
end function

' Card pressed. RowList sets `rowItemSelected = [rowIdx, itemIdx]`
' before firing the observer; pull the corresponding ContentNode
' off m.rows.content and route based on item type.
sub onCardSelected()
    selected = m.rows.rowItemSelected
    if selected = invalid then return
    rowIdx = selected[0]
    itemIdx = selected[1]
    rowNode = m.rows.content.getChild(rowIdx)
    if rowNode = invalid then return
    itemNode = rowNode.getChild(itemIdx)
    if itemNode = invalid then return

    ' Type-aware routing — same model the Android Navigator uses.
    ' Containers (show / season / artist / album / podcast / multi-
    ' file audiobook parent) drill into DetailScene so the user can
    ' pick a child to play. Leaf items (movie / episode / track /
    ' single-file audiobook / podcast_episode) go straight to
    ' playback — by the time the user clicked them they've already
    ' chosen what they want. Movies route to detail too so fanart +
    ' summary appear before the Play button (matches the Android
    ' modern UX). Photos + collections deferred until their own
    ' scenes ship.
    routeForType(itemNode.itemType, itemNode.id)
end sub

' ── Continue Watching options ──────────────────────────────────────

sub onRowItemFocused()
    updateOptionsHint()
end sub

' The "* Options" hint shows only while a Continue Watching card has
' focus — the one row where * does something.
sub updateOptionsHint()
    m.optionsHint.visible = (not m.inHeader and focusedContinueTarget() <> invalid)
end sub

' { rowIdx, id, title } for the focused card when it's on a Continue
' Watching row, else invalid.
function focusedContinueTarget() as Dynamic
    if m.rows.content = invalid then return invalid
    focused = m.rows.rowItemFocused
    if focused = invalid or focused.Count() < 2 then return invalid
    rowIdx = focused[0]
    if rowIdx < 0 or rowIdx >= m.hubRows.Count() then return invalid
    if m.hubRows[rowIdx].kind <> "continue" then return invalid
    rowNode = m.rows.content.getChild(rowIdx)
    if rowNode = invalid then return invalid
    itemNode = rowNode.getChild(focused[1])
    if itemNode = invalid then return invalid
    return { rowIdx: rowIdx, id: itemNode.id, title: itemNode.title }
end function

' The RowList handles its own rows; an "up" it doesn't use (top row)
' bubbles here and moves to the header buttons, where left / right step
' along and "down" returns to the rows.
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if m.inHeader
        if key = "left" and m.headerIdx > 0
            m.headerIdx = m.headerIdx - 1
            m.headerButtons[m.headerIdx].setFocus(true)
            return true
        end if
        if key = "right" and m.headerIdx < m.headerButtons.Count() - 1
            m.headerIdx = m.headerIdx + 1
            m.headerButtons[m.headerIdx].setFocus(true)
            return true
        end if
        if key = "down" and m.hubRows.Count() > 0
            focusRowsOrHeader()
            return true
        end if
        return key = "left" or key = "right"
    end if
    if key = "up" and m.rows.isInFocusChain()
        m.inHeader = true
        m.optionsHint.visible = false
        m.headerButtons[m.headerIdx].setFocus(true)
        return true
    end if
    if key = "options" or key = "*"
        if not m.rows.isInFocusChain() then return false
        target = focusedContinueTarget()
        if target = invalid then return false
        m.menuTarget = target
        Menu_Show("cw", target.title, "", ["Remove from Continue Watching", "Cancel"])
        return true
    end if
    return false
end function

sub onMenuChoice(menuId as String, index as Integer)
    if menuId = "cw" and index = 0 and m.menuTarget <> invalid
        dismissContinueWatching(m.menuTarget)
    end if
    focusRowsOrHeader()
end sub

sub dismissContinueWatching(target as Object)
    t = createObject("roSGNode", "ApiCallTask")
    t.method = "POST"
    t.path = ApiItemDismissContinueWatching(target.id)
    t.context = { rowIdx: target.rowIdx, id: target.id }
    t.observeField("state", "onDismissDone")
    m.pendingCalls.push(t)
    t.control = "RUN"
end sub

sub onDismissDone(evt as Object)
    t = evt.getRoSGNode()
    if t.state <> "done" then return
    dropPendingCall(t)
    res = t.result
    ctx = t.context
    if res <> invalid and res.ok = true and ctx <> invalid
        out = HubRows_RemoveItem(m.hubRows, ctx.rowIdx, ctx.id)
        if out.removed
            m.hubRows = out.rows
            renderRows()
            if m.hubRows.Count() > 0 then m.rows.jumpToRowItem = out.focus
            ' Don't pull focus back if the user has moved on (header, a
            ' new menu) while the request was out.
            if not m.inHeader and not Menu_IsOpen() then focusRowsOrHeader()
        end if
        return
    end if
    Menu_ShowMessage("cwFailed", "Continue Watching", WatchState_WriteErrorText(res, "Couldn't remove from Continue Watching."))
end sub

sub dropPendingCall(t as Object)
    kept = []
    for each p in m.pendingCalls
        if not p.isSameNode(t) then kept.push(p)
    end for
    m.pendingCalls = kept
end sub

' Centralised type → destination mapping. DetailScene + SearchScene
' + FavoritesScene + HistoryScene + CollectionScene all share
' equivalents of this routine — adding a new type later means
' updating each, which is why the Android client kept this in a
' single Navigator object.
sub routeForType(itemType as String, itemId as String)
    if itemType = "collection" or itemType = "playlist"
        getMainScene().callFunc("navigateToWithItem", "CollectionScene", itemId)
        return
    end if
    if itemType = "photo"
        getMainScene().callFunc("navigateToWithItem", "PhotoScene", itemId)
        return
    end if
    detail = false
    if itemType = "show" or itemType = "season" or itemType = "artist" or itemType = "album" or itemType = "podcast" or itemType = "audiobook" or itemType = "book_author" or itemType = "book_series" or itemType = "movie"
        detail = true
    end if
    if detail
        getMainScene().callFunc("navigateToWithItem", "DetailScene", itemId)
    else
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
