' Home rows from the /api/v1/hub payload, as plain data — HomeScene turns
' them into RowList ContentNodes. Pure, so the row order, the legacy
' Continue Watching split and "remove from Continue Watching" are unit-
' tested (tests/HubRows_test.brs) without SceneGraph.
'
' Each row is { title, kind, items } where kind is:
'   "continue"  Continue Watching (TV / Movies / Other) — * offers removal
'   "next_up"   the next episode of shows in progress
'   "plan"      Plan to Watch
'   "trending"  / "recent"

' Row order matches the server's default hub layout: the Continue Watching
' rows, then Next Up and Plan to Watch (hub_layout.go slots them straight
' after continue_*), then Trending and Recently Added. Empty rows are left
' out.
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
    HubRows_Add(rows, "Continue Watching TV Shows", "continue", cwTV)
    HubRows_Add(rows, "Continue Watching Movies", "continue", cwMovies)
    HubRows_Add(rows, "Continue Watching", "continue", cwOther)
    HubRows_Add(rows, "Next Up", "next_up", hub.next_up)
    HubRows_Add(rows, "Plan to Watch", "plan", hub.plan_to_watch)
    HubRows_Add(rows, "Trending", "trending", hub.trending)
    HubRows_Add(rows, "Recently Added", "recent", hub.recently_added)
    return rows
end function

sub HubRows_Add(rows as Object, title as String, kind as String, items as Dynamic)
    if items = invalid or type(items) <> "roArray" then return
    kept = []
    for each it in items
        if it <> invalid and type(it) = "roAssociativeArray" then kept.push(it)
    end for
    if kept.Count() = 0 then return
    rows.push({ title: title, kind: kind, items: kept })
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
            newRows.push({ title: row.title, kind: row.kind, items: items })
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
