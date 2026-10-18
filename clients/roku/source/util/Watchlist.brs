' Pure helpers behind WatchlistScene: which status rows to show (from
' GET /api/v1/watchlist/counts), their titles, the card second line and
' where OK on a card goes. No SceneGraph — unit-tested in
' tests/Watchlist_test.brs. Wording and order mirror the web page
' (web/src/lib/watchlist.ts). Depends on source/util/WatchState.brs for
' WatchState_Str / WatchState_Num / WatchState_EpisodeCode.

' Row order: Plan to Watch first, then the tracker progression. key is the
' server's status value (the ?status= the listing takes).
function Watchlist_Statuses() as Object
    return [
        { key: "plan_to_watch", label: "Plan to Watch" },
        { key: "watching", label: "Watching" },
        { key: "on_hold", label: "On Hold" },
        { key: "completed", label: "Completed" },
        { key: "dropped", label: "Dropped" }
    ]
end function

' The rows to fetch, from the counts payload ({ total, statuses: { <key>:
' { total, types } } }, already unwrapped from its data envelope). Returns
' [{ status, label, total }] in Watchlist_Statuses order, leaving out
' statuses with no items — the server omits 0-count statuses, and a
' malformed entry counts as empty. [] when the payload isn't an object.
function Watchlist_RowsFromCounts(counts as Dynamic) as Object
    rows = []
    if counts = invalid or type(counts) <> "roAssociativeArray" then return rows
    statuses = counts.statuses
    if statuses = invalid or type(statuses) <> "roAssociativeArray" then return rows
    for each s in Watchlist_Statuses()
        entry = statuses[s.key]
        total = 0
        if entry <> invalid and type(entry) = "roAssociativeArray" then total = WatchState_Num(entry.total, 0)
        if total > 0 then rows.push({ status: s.key, label: s.label, total: total })
    end for
    return rows
end function

' Row label with its count: "Plan to Watch (12)".
function Watchlist_RowTitle(label as String, total as Integer) as String
    return label + " (" + total.ToStr() + ")"
end function

' The cards of one listing page (the data array of GET /api/v1/watchlist):
' only objects with an id, so a malformed row can't become a card that
' routes nowhere. [] when the payload isn't an array.
function Watchlist_Items(data as Dynamic) as Object
    kept = []
    if data = invalid or type(data) <> "roArray" then return kept
    for each it in data
        if it <> invalid and type(it) = "roAssociativeArray" and WatchState_Str(it.id) <> "" then kept.push(it)
    end for
    return kept
end function

' The line under a card's title, as on the web: "Show · S2 · E5" for an
' episode, the show for a season, "Artist · Album" for a track, "Author ·
' 2019" for an album / audiobook / book, else the year. "" when the row
' has none of it.
function Watchlist_CardSubtitle(item as Dynamic) as String
    if item = invalid or type(item) <> "roAssociativeArray" then return ""
    t = WatchState_Str(item.type)
    if t = "episode"
        return Watchlist_Join(WatchState_Str(item.grandparent_title), WatchState_EpisodeCode(item.parent_index, item.index))
    end if
    if t = "season" then return WatchState_Str(item.parent_title)
    if t = "track"
        return Watchlist_Join(WatchState_Str(item.grandparent_title), WatchState_Str(item.parent_title))
    end if
    year = Watchlist_Year(item)
    if t = "album" or t = "audiobook" or t = "book"
        return Watchlist_Join(WatchState_Str(item.parent_title), year)
    end if
    return year
end function

' The year as text, or "" when missing / not a number.
function Watchlist_Year(item as Object) as String
    y = WatchState_Num(item.year, -1)
    if y <= 0 then return ""
    return y.ToStr()
end function

' "a · b", dropping an empty side.
function Watchlist_Join(a as String, b as String) as String
    if a = "" then return b
    if b = "" then return a
    return a + " · " + b
end function

' The scene OK on a card opens: photos go to their viewer, everything else
' to the item's detail page (which plays leaves from its Play button).
function Watchlist_Destination(itemType as Dynamic) as String
    t = WatchState_Str(itemType)
    if t = "photo" then return "PhotoScene"
    if t = "collection" or t = "playlist" then return "CollectionScene"
    return "DetailScene"
end function
