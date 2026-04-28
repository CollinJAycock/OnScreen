' HomeScene controller. Mounts → fires HubFetchTask → on
' completion, builds a ContentNode tree from the response and
' binds it to the RowList. Pressing OK on a card routes to the
' player (or detail screen when we ship one).

sub init()
    m.rows = m.top.findNode("rows")
    m.loading = m.top.findNode("loading")
    m.hubTask = m.top.findNode("hubTask")

    m.hubTask.observeField("state", "onHubTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")

    ' Kick off the fetch. Task nodes start when control=RUN.
    m.hubTask.control = "RUN"
end sub

sub onHubTaskState()
    if m.hubTask.state <> "done" then return

    hub = m.hubTask.result
    if hub = invalid or hub.Count() = 0
        m.loading.text = "Couldn't reach OnScreen — check your server URL"
        return
    end if

    m.loading.visible = false
    bindHubToRows(hub)
end sub

' Build a SceneGraph ContentNode tree mirroring the JSON hub. The
' RowList expects: root ContentNode → row ContentNodes (one per
' section) → item ContentNodes (one per card).
sub bindHubToRows(hub as Object)
    root = createObject("roSGNode", "ContentNode")

    addRowIfNonEmpty(root, "Continue Watching", hub.continue_watching)
    addBecauseYouWatched(root, hub.because_you_watched)
    addRowIfNonEmpty(root, "Trending", hub.trending)
    addRowIfNonEmpty(root, "Recently Added", hub.recently_added)

    m.rows.content = root
    m.rows.setFocus(true)
end sub

sub addRowIfNonEmpty(root as Object, title as String, items as Object)
    if items = invalid or items.Count() = 0 then return
    row = root.createChild("ContentNode")
    row.title = title
    for each item in items
        addItemToRow(row, item)
    end for
end sub

sub addBecauseYouWatched(root as Object, rows as Object)
    if rows = invalid or rows.Count() = 0 then return
    for each entry in rows
        seed = entry.seed
        items = entry["items"]
        if seed = invalid or items = invalid or items.Count() = 0 then continue for
        row = root.createChild("ContentNode")
        row.title = "Because you watched " + seed.title
        for each item in items
            addItemToRow(row, item)
        end for
    end for
end sub

sub addItemToRow(row as Object, item as Object)
    if item = invalid then return
    node = row.createChild("ContentNode")
    node.title = item.title
    node.id = item.id
    ' Stash the original item dict on the node so the click handler
    ' can read fields not in the standard ContentNode schema (type,
    ' fileId for stream URL, etc.). SceneGraph nodes accept
    ' arbitrary dynamic fields via setField().
    node.addField("itemType", "string", false)
    node.itemType = item.type
    if item.poster_path <> invalid
        token = Prefs_GetAccessToken()
        serverUrl = Prefs_GetServerUrl()
        if token <> invalid and serverUrl <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, item.poster_path, 500, token)
        end if
    end if
end sub

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

    ' Future: shows + seasons route to a DetailScene; movies and
    ' episodes go straight to playback. For the scaffold we route
    ' everything to the player and let it 404 gracefully on
    ' container types — replace with the real router as DetailScene
    ' lands.
    player = createObject("roSGNode", "PlayerScene")
    player.itemId = itemNode.id
    getMainScene().callFunc("navigateTo", "PlayerScene")
end sub

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
