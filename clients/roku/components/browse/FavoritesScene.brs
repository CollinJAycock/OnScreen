' FavoritesScene controller — fires ListFetchTask against
' /api/v1/favorites and renders the result as a single-row grid.

sub init()
    m.rows = m.top.findNode("rows")
    m.empty = m.top.findNode("empty")
    m.task = m.top.findNode("task")

    m.task.observeField("state", "onTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")

    m.task.path = ApiFavorites()
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
        node.id = it.id
        node.addField("itemType", "string", false)
        node.itemType = it.type
        artPath = invalid
        if it.poster_path <> invalid then artPath = it.poster_path
        if artPath = invalid and it.thumb_path <> invalid then artPath = it.thumb_path
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
    routeFavoriteSelection(item.itemType, item.id)
end sub

' Type-aware routing — same model HomeScene + SearchScene use.
sub routeFavoriteSelection(itemType as String, itemId as String)
    if itemType = "show" or itemType = "season" or itemType = "artist" or itemType = "album" or itemType = "podcast" or itemType = "audiobook" or itemType = "movie"
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
