' LibraryScene controller — fires ListFetchTask against
' /api/v1/libraries/{id}/items and renders the result as a poster
' grid. Same routing model as FavoritesScene: container types
' (show / season / album / artist / book_series / book_author)
' drill into DetailScene; movies route to detail too so the
' fanart + summary land before the play button; everything else
' goes straight to PlayerScene.

sub init()
    m.rows = m.top.findNode("rows")
    m.empty = m.top.findNode("empty")
    m.task = m.top.findNode("task")

    m.task.observeField("state", "onTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")
    m.top.observeField("itemId", "onItemIdSet")

    ' If MainScene set itemId before mount (the standard
    ' navigateToWithItem path), kick off the fetch immediately.
    ' onItemIdSet handles the (rare) case of a deferred set.
    if m.top.itemId <> invalid and m.top.itemId <> ""
        startFetch(m.top.itemId)
    end if
end sub

sub onItemIdSet()
    if m.top.itemId = invalid or m.top.itemId = "" then return
    startFetch(m.top.itemId)
end sub

sub startFetch(libraryId as String)
    m.task.path = ApiLibraryItems(libraryId)
    m.task.control = "RUN"
end sub

sub onTaskState()
    if m.task.state <> "done" then return
    items = m.task.result
    if items = invalid or items.Count() = 0
        m.empty.text = "This library is empty."
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
    routeForType(item.itemType, item.id)
end sub

' Same type → destination mapping HomeScene + FavoritesScene use.
' Containers drill into DetailScene; leaves go straight to
' PlayerScene; photos and collections route to their own viewers.
sub routeForType(itemType as String, itemId as String)
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
