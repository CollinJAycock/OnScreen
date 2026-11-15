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
    m.top.findNode("title").text = I18n_T("library.fallbackName")
    m.empty.text = I18n_T("common.loadingDots")

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
    ' Back into the library: the grid being filled towards the saved row
    ' before it goes on screen (startRestore).
    m.restoreRoot = invalid
    m.restoreDone = false
    ' The view (LibraryView): sort option, ?watch= and genre. Header
    ' buttons change it; each change reloads the grid from the top.
    m.sortIdx = 0
    m.watch = ""
    m.genre = ""
    m.genres = invalid
    m.inHeader = false
    m.headerIdx = 0
    m.sortBtn = m.top.findNode("sortBtn")
    m.watchBtn = m.top.findNode("watchBtn")
    m.genreBtn = m.top.findNode("genreBtn")
    m.sortBtn.observeField("buttonSelected", "onSortPressed")
    m.watchBtn.observeField("buttonSelected", "onWatchPressed")
    m.genreBtn.observeField("buttonSelected", "onGenrePressed")

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
    ' The view: what Back hands back, else this library's saved sort.
    m.sortIdx = LibraryView_ParseSort(Prefs_Get("libsort:" + libraryId))
    if m.sortIdx < 0 then m.sortIdx = LibraryView_DefaultSort(m.top.libraryType)
    rs = m.top.restoreState
    if rs <> invalid
        if rs.sortIdx <> invalid then m.sortIdx = rs.sortIdx
        if rs.watch <> invalid then m.watch = rs.watch
        if rs.genre <> invalid then m.genre = rs.genre
    end if
    m.hasFilters = LibraryView_HasFilters(m.top.libraryType)
    m.watchBtn.visible = m.hasFilters
    m.genreBtn.visible = m.hasFilters
    updateViewButtons()
    startRestore()
    fetchPage()
end sub

sub fetchPage()
    m.loadingPage = true
    m.task = renewTask(m.task, "ListFetchTask", "onTaskState")
    m.task.path = LibraryView_ItemsPath(m.libraryId, m.offset, LibraryPageSize(), m.sortIdx, m.watch, m.genre)
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
    if m.task.ok <> true
        ' A failed page isn't the end of the library: keep hasMore, so
        ' coming near the end again retries it.
        if m.restoreRoot <> invalid then attachRestored()
        if first and (m.rows.content = invalid or m.rows.content.getChildCount() = 0)
            m.empty.text = I18n_T("library.loadFailed")
            m.empty.visible = true
            focusHeader()
        end if
        return
    end if
    m.offset = m.offset + items.Count()
    ' A full page may have more behind it; a short one is the last.
    m.hasMore = (items.Count() >= LibraryPageSize())
    if first and items.Count() = 0
        if m.watch <> "" or m.genre <> ""
            m.empty.text = I18n_T("library.noMatches")
        else
            m.empty.text = I18n_T("library.empty")
        end if
        m.empty.visible = true
        focusHeader()
        return
    end if
    appendCards(items)
    if m.restoreRoot <> invalid
        ' Still loading towards the card the viewer opened.
        rs = m.top.restoreState
        if rs.row >= m.restoreRoot.getChildCount() and m.hasMore
            fetchPage()
            return
        end if
        attachRestored()
        return
    end if
    m.empty.visible = false
    if first and not m.inHeader then m.rows.setFocus(true)
end sub

' Add cards to the grid in rows of six (onCardSelected reads a card
' through its row, so the split needs nothing else), continuing the last
' row of the previous page. New rows are filled before they go into the
' grid, with one appendChildren per page: a createChild on the shown grid
' re-lays it out each time.
sub appendCards(items as Object)
    serverUrl = Prefs_GetServerUrl()
    token = Prefs_GetAccessToken()
    assetToken = Prefs_GetAssetTokenStr()
    root = m.rows.content
    if m.restoreRoot <> invalid then root = m.restoreRoot
    if root = invalid then root = createObject("roSGNode", "ContentNode")
    newRows = []
    for each it in items
        if m.cardCount mod 6 = 0
            m.lastRow = createObject("roSGNode", "ContentNode")
            newRows.push(m.lastRow)
        end if
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
            node.HDPosterUrl = AssetArtwork(serverUrl, artPath, ArtworkCardWidth(), assetToken)
        end if
    end for
    if newRows.Count() > 0 then root.appendChildren(newRows)
    if m.restoreRoot = invalid and m.rows.content = invalid then m.rows.content = root
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
        saveFocus()
        getMainScene().callFunc("navigateToWithItem", "PhotoScene", itemId)
        return
    end if
    if itemType = "collection" or itemType = "playlist"
        saveFocus()
        getMainScene().callFunc("navigateToWithItem", "CollectionScene", itemId)
        return
    end if
    if itemType = "show" or itemType = "season" or itemType = "artist" or itemType = "album" or itemType = "podcast" or itemType = "audiobook" or itemType = "book_author" or itemType = "book_series" or itemType = "book" or itemType = "movie"
        saveFocus()
        getMainScene().callFunc("navigateToWithItem", "DetailScene", itemId)
    else
        saveFocus()
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

' ── Back to the card the viewer opened (MainScene's restoreState) ──

sub saveFocus()
    st = { sortIdx: m.sortIdx, watch: m.watch, genre: m.genre }
    f = m.rows.rowItemSelected
    if f <> invalid and f.Count() >= 2
        st.row = f[0]
        st.item = f[1]
    end if
    m.top.focusState = st
end sub

' Back into the library (restoreState has the row): pages load into a
' grid that isn't shown yet (m.restoreRoot) until the saved row is in,
' then it goes on screen once and jumps there — not page after page laid
' out on screen only to be scrolled past.
sub startRestore()
    rs = m.top.restoreState
    if m.restoreDone = true or rs = invalid or rs.row = invalid then return
    m.restoreRoot = createObject("roSGNode", "ContentNode")
end sub

' The restored grid goes on screen, focused on the saved card (or the
' nearest one that loaded).
sub attachRestored()
    root = m.restoreRoot
    m.restoreRoot = invalid
    m.restoreDone = true
    rows = root.getChildCount()
    if rows = 0 then return
    m.rows.content = root
    m.empty.visible = false
    rs = m.top.restoreState
    r = rs.row
    if r >= rows then r = rows - 1
    n = root.getChild(r).getChildCount()
    i = rs.item
    if i >= n then i = n - 1
    if i < 0 then i = 0
    m.rows.jumpToRowItem = [r, i]
    if not m.inHeader then m.rows.setFocus(true)
end sub

' ── Sort and filters ───────────────────────────────────────────────

sub updateViewButtons()
    m.sortBtn.text = I18n_TP("library.sortChip", { sort: LibraryView_SortOptions()[m.sortIdx].label })
    watchLabel = I18n_T("library.watchAll")
    for each o in LibraryView_WatchOptions()
        if o.value = m.watch then watchLabel = o.label
    end for
    m.watchBtn.text = I18n_TP("library.showChip", { filter: watchLabel })
    genre = m.genre
    if genre = "" then genre = I18n_T("library.genreAll")
    m.genreBtn.text = I18n_TP("library.genreChip", { genre: genre })
end sub

function headerButtons() as Object
    if m.hasFilters = true then return [m.sortBtn, m.watchBtn, m.genreBtn]
    return [m.sortBtn]
end function

sub focusHeader()
    m.inHeader = true
    btns = headerButtons()
    if m.headerIdx >= btns.Count() then m.headerIdx = 0
    btns[m.headerIdx].setFocus(true)
end sub

sub focusGrid()
    if m.rows.content = invalid or m.rows.content.getChildCount() = 0 then return
    m.inHeader = false
    m.rows.setFocus(true)
end sub

sub onSortPressed()
    labels = []
    for each o in LibraryView_SortOptions()
        labels.push(o.label)
    end for
    Menu_Show("sort", I18n_T("library.sortBy"), "", labels)
end sub

sub onWatchPressed()
    labels = []
    for each o in LibraryView_WatchOptions()
        labels.push(o.label)
    end for
    Menu_Show("watch", I18n_T("library.show"), "", labels)
end sub

' The genres come from the server the first time (with how many titles
' each has), then open as a menu.
sub onGenrePressed()
    if m.genres <> invalid
        showGenreMenu()
        return
    end if
    m.genreTask = createObject("roSGNode", "ListFetchTask")
    m.genreTask.observeField("state", "onGenresLoaded")
    m.genreTask.path = "/api/v1/libraries/" + m.libraryId + "/genres"
    m.genreTask.control = "RUN"
end sub

sub onGenresLoaded()
    if m.genreTask = invalid or m.genreTask.state <> "done" then return
    rows = m.genreTask.result
    m.genreTask = invalid
    m.genres = []
    if rows <> invalid
        for each g in rows
            if g <> invalid and g.name <> invalid and g.name <> "" then m.genres.push(g.name)
        end for
    end if
    showGenreMenu()
end sub

sub showGenreMenu()
    if m.genres.Count() = 0
        Menu_ShowMessage("noGenres", I18n_T("library.genre"), I18n_T("library.noGenres"))
        return
    end if
    labels = [I18n_T("library.allGenres")]
    labels.Append(m.genres)
    Menu_Show("genre", I18n_T("library.genre"), "", labels)
end sub

sub onMenuChoice(menuId as String, index as Integer)
    changed = false
    if index >= 0
        if menuId = "sort" and index <> m.sortIdx
            m.sortIdx = index
            Prefs_Set("libsort:" + m.libraryId, LibraryView_SortKey(index))
            changed = true
        else if menuId = "watch"
            v = LibraryView_WatchOptions()[index].value
            changed = (v <> m.watch)
            m.watch = v
        else if menuId = "genre"
            g = ""
            if index > 0 then g = m.genres[index - 1]
            changed = (g <> m.genre)
            m.genre = g
        end if
    end if
    focusHeader()
    if changed then reload()
end sub

' Start the grid over in the new view.
sub reload()
    updateViewButtons()
    ' A new view starts at the top, not at the card Back came to.
    m.restoreRoot = invalid
    m.restoreDone = true
    m.offset = 0
    m.hasMore = false
    m.cardCount = 0
    m.lastRow = invalid
    m.rows.content = createObject("roSGNode", "ContentNode")
    m.empty.text = I18n_T("common.loadingDots")
    m.empty.visible = true
    fetchPage()
end sub

' Up from the first row to the buttons, Down back; Left / Right along them.
function onKeyEvent(key as String, press as Boolean) as Boolean
    if not press then return false
    if m.inHeader
        btns = headerButtons()
        if key = "left" and m.headerIdx > 0
            m.headerIdx = m.headerIdx - 1
            btns[m.headerIdx].setFocus(true)
            return true
        end if
        if key = "right" and m.headerIdx < btns.Count() - 1
            m.headerIdx = m.headerIdx + 1
            btns[m.headerIdx].setFocus(true)
            return true
        end if
        if key = "down"
            focusGrid()
            return true
        end if
        return key = "left" or key = "right" or key = "up"
    end if
    if key = "up"
        f = m.rows.rowItemFocused
        if f = invalid or f.Count() < 1 or f[0] = 0
            focusHeader()
            return true
        end if
    end if
    return false
end function
