' Home rows from the /api/v1/hub payload, as plain data — HomeScene turns
' them into RowList ContentNodes. Pure, so the row order, the legacy
' Continue Watching split and "remove from Continue Watching" are unit-
' tested (tests/HubRows_test.brs) without SceneGraph. Row titles come from
' the catalog (source/util/I18n.brs); collection names are the server's.
'
' Each row is { key, title, kind, items } where kind is:
'   "continue"  Continue Watching (TV / Movies / Other) — * offers removal
'   "next_up"   the next episode of shows in progress
'   "plan"      Plan to Watch
'   "collection" a collection an admin put on the home screen
'   "trending" / "recent" / "library" (a library's recently added)
' and key is the row's name in the saved home layout (HubRows_Order).

' Default order, as the web home and Android TV (HomeFragment.buildRows):
' Continue Watching TV, Next Up, Continue Watching Movies / Other, Plan to
' Watch, the promoted collections, Trending, Recently Added, then each
' library's recently added. Empty rows are left out.
function HubRows_Build(hub as Dynamic) as Object
    rows = []
    if hub = invalid or type(hub) <> "roAssociativeArray" then return rows

    ' Continue Watching: newer servers pre-split TV / Movies / Other (one
    ' tile per show on the TV row). Older servers only send the combined
    ' continue_watching feed, which is split here by type.
    cwTV = hub.continue_watching_tv
    cwMovies = hub.continue_watching_movies
    cwOther = hub.continue_watching_other
    if cwTV = invalid and cwMovies = invalid and cwOther = invalid
        cwTV = []
        cwMovies = []
        cwOther = []
        legacy = hub.continue_watching
        if legacy <> invalid and type(legacy) = "roArray"
            for each it in legacy
                if it <> invalid and type(it) = "roAssociativeArray"
                    if it.type = "episode"
                        cwTV.push(it)
                    else if it.type = "movie"
                        cwMovies.push(it)
                    else
                        cwOther.push(it)
                    end if
                end if
            end for
        end if
    end if
    HubRows_Add(rows, "continue_tv", I18n_T("hub.continueTv"), "continue", cwTV)
    HubRows_Add(rows, "next_up", I18n_T("hub.nextUp"), "next_up", hub.next_up)
    HubRows_Add(rows, "continue_movies", I18n_T("hub.continueMovies"), "continue", cwMovies)
    HubRows_Add(rows, "continue_other", I18n_T("hub.continueWatching"), "continue", cwOther)
    HubRows_Add(rows, "plan_to_watch", I18n_T("hub.planToWatch"), "plan", hub.plan_to_watch)
    if hub.collection_rows <> invalid and type(hub.collection_rows) = "roArray"
        for each c in hub.collection_rows
            if c <> invalid and type(c) = "roAssociativeArray" and HubRows_IsStr(c.collection_id) and HubRows_IsStr(c.name)
                HubRows_Add(rows, "collection:" + c.collection_id, c.name, "collection", c.items)
            end if
        end for
    end if
    HubRows_Add(rows, "trending", I18n_T("hub.trending"), "trending", hub.trending)
    HubRows_Add(rows, "recently_added", I18n_T("hub.recentlyAdded"), "recent", hub.recently_added)
    if hub.recently_added_by_library <> invalid and type(hub.recently_added_by_library) = "roArray"
        for each l in hub.recently_added_by_library
            if l <> invalid and type(l) = "roAssociativeArray" and HubRows_IsStr(l.library_id) and HubRows_IsStr(l.library_name)
                HubRows_Add(rows, "library:" + l.library_id, I18n_TP("hub.recentlyAddedIn", { library: l.library_name }), "library", l.items)
            end if
        end for
    end if
    return rows
end function

' rows under the saved home layout (GET /users/me/preferences ->
' hub_layout, [{ key, enabled }], set on the web home and shared by every
' client): rows it names first, in its order, a disabled one hidden; a key
' with no row skipped; rows it doesn't name after, in default order. A
' key listed twice counts once. No layout = default order.
function HubRows_Order(rows as Object, layout as Dynamic) as Object
    if layout = invalid or type(layout) <> "roArray" or layout.Count() = 0 then return rows
    byKey = {}
    for each r in rows
        if not byKey.DoesExist(r.key) then byKey[r.key] = r
    end for
    used = {}
    out = []
    for each pref in layout
        if pref <> invalid and type(pref) = "roAssociativeArray" and HubRows_IsStr(pref.key)
            r = byKey[pref.key]
            if r <> invalid and not used.DoesExist(pref.key)
                used[pref.key] = true
                if pref.enabled <> false then out.push(r)
            end if
        end if
    end for
    for each r in rows
        if not used.DoesExist(r.key)
            used[r.key] = true
            out.push(r)
        end if
    end for
    return out
end function

function HubRows_IsStr(v as Dynamic) as Boolean
    if v = invalid then return false
    if type(v) <> "String" and type(v) <> "roString" then return false
    return v <> ""
end function

sub HubRows_Add(rows as Object, key as String, title as String, kind as String, items as Dynamic)
    if items = invalid or type(items) <> "roArray" then return
    kept = []
    for each it in items
        if it <> invalid and type(it) = "roAssociativeArray" then kept.push(it)
    end for
    if kept.Count() = 0 then return
    rows.push({ key: key, title: title, kind: kind, items: kept })
end sub

' Drop the tile with id `itemId` from row `rowIdx` (the row the user acted
' on), and the row itself if that empties it. Returns { rows, focus,
' removed }: focus is the [row, item] to put the cursor on afterwards — the
' neighbour that slid into the removed tile's place, the new last tile, or
' the row that moved up into an emptied row's slot. removed is false (rows
' unchanged) when the tile isn't there.
function HubRows_RemoveItem(rows as Object, rowIdx as Integer, itemId as String) as Object
    out = { rows: rows, focus: [0, 0], removed: false }
    if rowIdx < 0 or rowIdx >= rows.Count() then return out
    row = rows[rowIdx]
    idx = -1
    for i = 0 to row.items.Count() - 1
        rid = row.items[i].id
        if rid <> invalid and rid = itemId
            idx = i
            exit for
        end if
    end for
    if idx < 0
        out.focus = [rowIdx, 0]
        return out
    end if

    items = []
    for i = 0 to row.items.Count() - 1
        if i <> idx then items.push(row.items[i])
    end for
    newRows = []
    for i = 0 to rows.Count() - 1
        if i <> rowIdx
            newRows.push(rows[i])
        else if items.Count() > 0
            newRows.push({ key: row.key, title: row.title, kind: row.kind, items: items })
        end if
    end for
    out.rows = newRows
    out.removed = true
    if items.Count() > 0
        focusItem = idx
        if focusItem > items.Count() - 1 then focusItem = items.Count() - 1
        out.focus = [rowIdx, focusItem]
    else
        focusRow = rowIdx
        if focusRow > newRows.Count() - 1 then focusRow = newRows.Count() - 1
        if focusRow < 0 then focusRow = 0
        out.focus = [focusRow, 0]
    end if
    return out
end function

' The Collections row (GET /collections; Android TV's TV-only
' "collections" row, last by default): each collection as a card that
' opens CollectionScene. A franchise's cover is TMDB art (poster_url).
sub HubRows_AddCollections(rows as Object, cols as Dynamic)
    if cols = invalid or type(cols) <> "roArray" then return
    items = []
    for each c in cols
        if c <> invalid and type(c) = "roAssociativeArray" and HubRows_IsStr(c.id) and HubRows_IsStr(c.name)
            it = { id: c.id, type: "collection", title: c.name }
            if HubRows_IsStr(c.poster_path) then it.poster_path = c.poster_path
            if HubRows_IsStr(c.poster_url) and Left(c.poster_url, 4) = "http" then it.poster_url = c.poster_url
            items.push(it)
        end if
    end for
    HubRows_Add(rows, "collections", I18n_T("hub.collections"), "collections", items)
end sub
