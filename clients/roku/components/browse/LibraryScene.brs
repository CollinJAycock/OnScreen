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

    ' Re-entrancy guard (mirrors DetailScene / PhotoScene): MainScene
    ' sets itemId before mount AND init() reads it, so both init and
    ' the itemId observer can fire for the same id — without this the
    ' fetch would kick off twice.
    m.kickedOff = false
    ' Paging: the server caps a page at 200. The next page is fetched when
    ' the focus comes within three rows of the end (onRowFocused), and its
    ' cards are appended to the grid, which keeps the focus where it is.
    m.libraryId = ""
    m.offset = 0
    m.hasMore = false
    m.loadingPage = false
    m.cardCount = 0
    m.lastRow = invalid

    m.task.observeField("state", "onTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")
    m.rows.observeField("rowItemFocused", "onRowFocused")
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
    if m.kickedOff then return
    startFetch(m.top.itemId)
end sub

sub startFetch(libraryId as String)
    m.kickedOff = true
    m.libraryId = libraryId
    fetchPage()
end sub

sub fetchPage()
    m.loadingPage = true
    m.task = renewTask(m.task, "ListFetchTask", "onTaskState")
    m.task.path = ApiLibraryItemsPage(m.libraryId, m.offset, LibraryPageSize())
    m.task.control = "RUN"
end sub

function LibraryPageSize() as Integer
    return 200
end function

sub onTaskState()
    if m.task.state <> "done" then return
    m.loadingPage = false
    items = m.task.result
    if items = invalid then items = []
    first = (m.offset = 0)
    m.offset = m.offset + items.Count()
    ' A full page may have more behind it; a short one is the last.
    m.hasMore = (items.Count() >= LibraryPageSize())
    if first and items.Count() = 0
        m.empty.text = "This library is empty."
        return
    end if
    m.empty.visible = false
    appendCards(items)
    if first then m.rows.setFocus(true)
end sub

' Add cards to the grid in rows of six (onCardSelected reads a card
' through its row, so the split needs nothing else), continuing the last
' row of the previous page.
sub appendCards(items as Object)
    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()
    root = m.rows.content
    if root = invalid then root = createObject("roSGNode", "ContentNode")
    for each it in items
        if m.cardCount mod 6 = 0 then m.lastRow = root.createChild("ContentNode")
        m.cardCount = m.cardCount + 1
        node = m.lastRow.createChild("ContentNode")
        node.title = it.title
        node.id = it.id
        node.addField("itemType", "string", false)
        node.itemType = it.type
        ' Watched check, unwatched-episode count (shows) or resume bar,
        ' from the listing's watch_state / leaf_count / unwatched_count.
        CardFields_Apply(node, it, "")
        artPath = invalid
        if it.poster_path <> invalid then artPath = it.poster_path
        if artPath = invalid and it.thumb_path <> invalid then artPath = it.thumb_path
        if artPath <> invalid and serverUrl <> invalid and token <> invalid
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, 500, Prefs_GetAssetTokenStr())
        end if
    end for
    if m.rows.content = invalid then m.rows.content = root
end sub

sub onRowFocused()
    f = m.rows.rowItemFocused
    if f = invalid or f.Count() < 1 or not m.hasMore or m.loadingPage then return
    if m.rows.content = invalid then return
    if f[0] >= m.rows.content.getChildCount() - 3 then fetchPage()
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
    if itemType = "show" or itemType = "season" or itemType = "artist" or itemType = "album" or itemType = "podcast" or itemType = "audiobook" or itemType = "book_author" or itemType = "book_series" or itemType = "book" or itemType = "movie"
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

sub onHeading()
    h = m.top.heading
    if h <> invalid and h <> "" then m.top.findNode("title").text = h
end sub

' A fresh Task for every run: on a device a Task that has finished
' ("done") never runs again when set to RUN (a second search hung on
' "Searching..."; the library's second page never came).
function renewTask(old as Dynamic, component as String, callback as String) as Object
    if old <> invalid then old.unobserveField("state")
    t = createObject("roSGNode", component)
    t.observeField("state", callback)
    return t
end function
