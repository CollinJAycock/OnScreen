' PhotoScene controller.
'
' On init we kick the item-detail fetch — needed to learn the
' photo's parent_id + library_id for sibling resolution. As soon
' as the response lands we render the current photo (so the user
' sees something immediately) and fire the parent-children task
' to populate the sibling list. If the parent path yields fewer
' than two photos we fall back to a single page of library items;
' that covers the "loose photo at the library root" case the
' Android client handles via parallel pagination.
'
' Sibling list ordering matches the API response order so left/
' right navigates the same direction the source library does.
' Wrap-around is intentional — TV remote feels less surprising
' when the click never goes dead at the end of the list.

sub init()
    m.photo = m.top.findNode("photo")
    m.position = m.top.findNode("position")
    m.itemTask = m.top.findNode("itemTask")
    m.parentChildrenTask = m.top.findNode("parentChildrenTask")
    m.libraryItemsTask = m.top.findNode("libraryItemsTask")

    m.siblings = []      ' [String] of photo item ids
    m.currentIndex = 0   ' index into siblings

    m.itemTask.observeField("state", "onItemTaskState")
    m.parentChildrenTask.observeField("state", "onParentChildrenState")
    m.libraryItemsTask.observeField("state", "onLibraryItemsState")
    m.top.observeField("itemId", "onItemIdSet")
    ' Listen for D-pad keys via the scene's onKeyEvent — Roku
    ' delivers them through the function defined below.

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
    m.itemTask.itemId = m.top.itemId
    m.itemTask.control = "RUN"
    renderCurrent(m.top.itemId)
end sub

sub onItemTaskState()
    if m.itemTask.state <> "done" then return
    item = m.itemTask.result
    if item = invalid then return
    m.libraryId = item.library_id
    parentId = item.parent_id
    if parentId <> invalid and parentId <> ""
        m.parentChildrenTask.path = ApiItemChildren(parentId)
        m.parentChildrenTask.control = "RUN"
    else
        ' Loose photo (no parent album) — go straight to the
        ' library fallback.
        if m.libraryId <> invalid then fetchLibraryItems()
    end if
end sub

sub onParentChildrenState()
    if m.parentChildrenTask.state <> "done" then return
    list = m.parentChildrenTask.result
    photos = filterPhotoIds(list)
    if photos.Count() >= 2
        adoptSiblings(photos)
        return
    end if
    ' Parent only had this one photo (or none) — fall back to the
    ' library listing. Android's PhotoViewFragment paginates the
    ' library in parallel for big libraries; Roku takes the simpler
    ' single-page approach since `roUrlTransfer` doesn't compose
    ' parallel fetches as cleanly. Up to 200 photos is enough for
    ' most loose-photos directories.
    if m.libraryId <> invalid then fetchLibraryItems()
end sub

sub fetchLibraryItems()
    m.libraryItemsTask.path = "/api/v1/libraries/" + m.libraryId + "/items?limit=200&offset=0"
    m.libraryItemsTask.control = "RUN"
end sub

sub onLibraryItemsState()
    if m.libraryItemsTask.state <> "done" then return
    list = m.libraryItemsTask.result
    photos = filterPhotoIds(list)
    if photos.Count() >= 2 then adoptSiblings(photos)
end sub

' Pull the photo ids out of an item-list response, preserving
' the response order (Android does the same — siblings render
' left-to-right in library / album order).
function filterPhotoIds(list as Object) as Object
    out = []
    if list = invalid then return out
    for each it in list
        if it.type = "photo" and it.id <> invalid then out.push(it.id)
    end for
    return out
end function

' Wire the resolved sibling list into the scene state. Picks the
' current index by locating the initial photo in the list; if
' it's somehow missing (race against deletion, rare scan timing)
' we land on index 0.
sub adoptSiblings(photos as Object)
    m.siblings = photos
    m.currentIndex = 0
    target = m.top.itemId
    for i = 0 to photos.Count() - 1
        if photos[i] = target
            m.currentIndex = i
            exit for
        end if
    end for
    refreshPositionLabel()
end sub

sub advance(delta as Integer)
    if m.siblings = invalid or m.siblings.Count() < 2 then return
    n = m.siblings.Count()
    ' `next` is a BrightScript reserved keyword (loop terminator);
    ' use a different name for the wrap-around index variable.
    nextIdx = ((m.currentIndex + delta) mod n + n) mod n
    m.currentIndex = nextIdx
    renderCurrent(m.siblings[nextIdx])
    refreshPositionLabel()
end sub

sub renderCurrent(itemId as String)
    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()
    if serverUrl = invalid or token = invalid then return
    ' Photos route through /items/{id}/image (server-side resize +
    ' cache); 1920×1080 with fit=contain mirrors the Android
    ' viewer's request size. The asset middleware accepts the
    ' access token via ?token= same as /artwork.
    m.photo.uri = serverUrl + "/api/v1/items/" + itemId + "/image?w=1920&h=1080&fit=contain&token=" + token
end sub

sub refreshPositionLabel()
    if m.siblings.Count() < 2
        m.position.visible = false
        return
    end if
    m.position.text = (m.currentIndex + 1).ToStr() + " / " + m.siblings.Count().ToStr()
    m.position.visible = true
end sub

' Roku delivers D-pad + back keys via onKeyEvent on the scene
' that has focus. PhotoScene captures left/right + back here so
' the user can navigate without any focus-grabbing widget on the
' page (the Poster node is non-focusable by default).
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if key = "left" or key = "rewind"
        advance(-1)
        return true
    end if
    if key = "right" or key = "fastforward"
        advance(1)
        return true
    end if
    if key = "back"
        getMainScene().callFunc("navigateTo", "HomeScene")
        return true
    end if
    return false
end function

function getMainScene() as Object
    node = m.top
    while node.getParent() <> invalid
        node = node.getParent()
    end while
    return node
end function
