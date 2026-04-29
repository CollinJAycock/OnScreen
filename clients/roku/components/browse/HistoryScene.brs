' HistoryScene controller — fires ListFetchTask against
' /api/v1/history and renders the result as a single-row grid.
' The watch-history shape uses media_id for the playback target
' (not id, which is the watch_event PK), so routing reads that
' field instead of node.id.

sub init()
    m.rows = m.top.findNode("rows")
    m.empty = m.top.findNode("empty")
    m.task = m.top.findNode("task")

    m.task.observeField("state", "onTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")

    m.task.path = ApiHistory()
    m.task.control = "RUN"
end sub

sub onTaskState()
    if m.task.state <> "done" then return
    items = m.task.result
    if items = invalid or items.Count() = 0
        m.empty.visible = true
        return
    end if
    m.empty.visible = false

    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()

    root = createObject("roSGNode", "ContentNode")
    row = root.createChild("ContentNode")
    for each it in items
        node = row.createChild("ContentNode")
        node.title = it.title
        ' History rows expose media_id for the playback target;
        ' .id on the server response is the watch_event PK and
        ' isn't what we want to route to.
        node.id = it.media_id
        node.addField("itemType", "string", false)
        node.itemType = it.type
        artPath = invalid
        if it.thumb_path <> invalid then artPath = it.thumb_path
        if artPath <> invalid and serverUrl <> invalid and token <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 500, token)
        end if
    end for
    m.rows.content = root
    m.rows.setFocus(true)
end sub

sub onCardSelected()
    selected = m.rows.rowItemSelected
    if selected = invalid then return
    rowNode = m.rows.content.getChild(selected[0])
    if rowNode = invalid then return
    item = rowNode.getChild(selected[1])
    if item = invalid then return
    routeHistorySelection(item.itemType, item.id)
end sub

sub routeHistorySelection(itemType as String, itemId as String)
    if itemType = "photo"
        getMainScene().callFunc("navigateToWithItem", "PhotoScene", itemId)
        return
    end if
    if itemType = "collection" or itemType = "playlist"
        getMainScene().callFunc("navigateToWithItem", "CollectionScene", itemId)
        return
    end if
    if itemType = "show" or itemType = "season" or itemType = "artist" or itemType = "album" or itemType = "podcast" or itemType = "audiobook" or itemType = "book_author" or itemType = "book_series" or itemType = "movie"
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
