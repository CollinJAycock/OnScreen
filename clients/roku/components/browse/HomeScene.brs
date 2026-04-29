' HomeScene controller. Mounts → fires HubFetchTask → on
' completion, builds a ContentNode tree from the response and
' binds it to the RowList. Pressing OK on a card routes to the
' player (or detail screen when we ship one).

sub init()
    m.rows = m.top.findNode("rows")
    m.loading = m.top.findNode("loading")
    m.hubTask = m.top.findNode("hubTask")
    m.searchBtn = m.top.findNode("searchBtn")
    m.favoritesBtn = m.top.findNode("favoritesBtn")
    m.historyBtn = m.top.findNode("historyBtn")

    m.hubTask.observeField("state", "onHubTaskState")
    m.rows.observeField("rowItemSelected", "onCardSelected")
    m.searchBtn.observeField("buttonSelected", "onSearchPressed")
    m.favoritesBtn.observeField("buttonSelected", "onFavoritesPressed")
    m.historyBtn.observeField("buttonSelected", "onHistoryPressed")

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

    ' Continue Watching: server pre-splits TV / Movies / Other on
    ' newer builds so each row is one tile per show (TV) or one
    ' tile per file (movies). Older servers only return the
    ' combined continue_watching feed; we filter client-side as a
    ' fallback so a server upgrade is the only thing required to
    ' unlock the split UI.
    cwTV = hub.continue_watching_tv
    cwMovies = hub.continue_watching_movies
    cwOther = hub.continue_watching_other
    if cwTV = invalid and cwMovies = invalid and cwOther = invalid
        cwTV = []
        cwMovies = []
        cwOther = []
        if hub.continue_watching <> invalid
            for each it in hub.continue_watching
                if it.type = "episode"
                    cwTV.push(it)
                else if it.type = "movie"
                    cwMovies.push(it)
                else
                    cwOther.push(it)
                end if
            end for
        end if
    end if
    addRowIfNonEmpty(root, "Continue Watching TV Shows", cwTV)
    addRowIfNonEmpty(root, "Continue Watching Movies", cwMovies)
    addRowIfNonEmpty(root, "Continue Watching", cwOther)
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
