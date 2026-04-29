' CollectionScene controller — fetches /collections/{id}/items via
' ListFetchTask and renders the result as a single-row grid.
' Type-aware routing on click matches HomeScene / SearchScene /
' FavoritesScene.

sub init()
    m.rows = m.top.findNode("rows")
    m.empty = m.top.findNode("empty")
    m.task = m.top.findNode("itemsTask")

    m.task.observeField("state", "onTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")
    m.top.observeField("itemId", "onItemIdSet")

    if m.top.itemId <> invalid and m.top.itemId <> ""
        kickoff()
    end if
end sub

sub onItemIdSet()
    if m.top.itemId <> invalid and m.top.itemId <> ""
        kickoff()
    end if
end sub

sub kickoff()
    m.task.path = ApiCollectionItems(m.top.itemId)
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
        if it.poster_path <> invalid and serverUrl <> invalid and token <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, it.poster_path, 500, token)
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
    routeCollectionSelection(item.itemType, item.id)
end sub

sub routeCollectionSelection(itemType as String, itemId as String)
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
