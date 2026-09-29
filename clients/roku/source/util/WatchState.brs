' Pure helpers behind the channel's watch-state UI: Next Up tiles, the
' show / season Play button (GET /items/{id}/up-next), "Mark all" options,
' poster-card badges and the season-page Play fallback. No SceneGraph —
' unit-tested in tests/WatchState_test.brs. Wording mirrors
' web/src/lib/watchState.ts and the Android TV app's WatchStateUi so the
' clients label things the same.

' True only for a real boolean true. JSON-decoded fields can be missing
' (invalid) or, on a malformed row, some other type; comparing those to
' `true` is a Type Mismatch on some firmware, so check the type first.
function WatchState_IsTrue(v as Dynamic) as Boolean
    if v = invalid then return false
    t = type(v)
    if t <> "Boolean" and t <> "roBoolean" then return false
    return v
end function

' A JSON number as an Integer, or `fallback` when the field is missing or
' not numeric.
function WatchState_Num(v as Dynamic, fallback as Integer) as Integer
    if v = invalid then return fallback
    t = type(v)
    if t = "Integer" or t = "roInt" or t = "roInteger" or t = "LongInteger" or t = "roLongInteger" or t = "Float" or t = "roFloat" or t = "Double" or t = "roDouble"
        return Int(v)
    end if
    return fallback
end function

function WatchState_Str(v as Dynamic) as String
    if v = invalid then return ""
    t = type(v)
    if t <> "String" and t <> "roString" then return ""
    return v
end function

' "S2 · E5". A missing season drops the S half; a missing or 0 episode
' number (the server's "unknown") drops the E half; both missing → "".
function WatchState_EpisodeCode(season as Dynamic, episode as Dynamic) as String
    s = WatchState_Num(season, -1)
    e = WatchState_Num(episode, -1)
    out = ""
    if s >= 0 then out = "S" + s.ToStr()
    if e > 0
        if out <> "" then out = out + " · "
        out = out + "E" + e.ToStr()
    end if
    return out
end function

' Second line of an episode tile: "S2 · E5 — Episode title", or just the
' title when the numbers are missing.
function WatchState_NextUpSubtitle(item as Object) as String
    title = WatchState_Str(item.title)
    code = WatchState_EpisodeCode(item.season_number, item.episode_number)
    if code = "" then return title
    if title = "" then return code
    return code + " — " + title
end function

' Title + subtitle for a hub tile. Episode tiles that carry their show
' (Next Up, recently-added episodes) read "Show" over "S2 · E5 — Title";
' every other tile keeps its own title and no subtitle.
function WatchState_HubTileText(item as Object) as Object
    title = WatchState_Str(item.title)
    show = WatchState_Str(item.show_title)
    if WatchState_Str(item.type) = "episode" and show <> ""
        return { title: show, subtitle: WatchState_NextUpSubtitle(item) }
    end if
    return { title: title, subtitle: "" }
end function

' Map an up-next response to the show / season Play button, or invalid
' when there's nothing to play (mode "none", no episode, unknown mode):
'   resume  → "Resume S3 · E4"
'   next    → "Play S3 · E5"
'   start   → "Play S1 · E1"
'   rewatch → "Watch again"
' Returns { kind, label, episodeId, seasonId }.
function WatchState_UpNextAction(u as Dynamic) as Dynamic
    if u = invalid or type(u) <> "roAssociativeArray" then return invalid
    ep = u.episode
    if ep = invalid or type(ep) <> "roAssociativeArray" then return invalid
    epId = WatchState_Str(ep.id)
    if epId = "" then return invalid
    mode = WatchState_Str(u.mode)
    code = WatchState_EpisodeCode(ep.season_number, ep.episode_number)
    action = { kind: "", label: "", episodeId: epId, seasonId: WatchState_Str(ep.season_id) }
    if mode = "resume"
        action.kind = "resume"
        action.label = "Resume"
        if code <> "" then action.label = "Resume " + code
    else if mode = "next" or mode = "start"
        action.kind = "play"
        action.label = "Play"
        if code <> "" then action.label = "Play " + code
    else if mode = "rewatch"
        action.kind = "rewatch"
        action.label = "Watch again"
    else
        return invalid
    end if
    return action
end function

' Which "Mark all …" actions a show / season offers, from its up-next state:
' nothing watched yet → only watched; everything watched → only unwatched;
' no episodes → neither; unknown (the call failed / an older server) or part
' watched → both.
function WatchState_MarkAllOptions(u as Dynamic) as Object
    mode = ""
    if u <> invalid and type(u) = "roAssociativeArray" then mode = WatchState_Str(u.mode)
    if mode = "none" then return { watched: false, unwatched: false }
    if mode = "start" then return { watched: true, unwatched: false }
    if mode = "rewatch" then return { watched: false, unwatched: true }
    return { watched: true, unwatched: true }
end function

' Label for the show / season mark button, or "" to hide it. Both options
' open a small menu ("Mark all…"); one option is the button itself.
function WatchState_MarkAllLabel(opts as Object) as String
    if opts.watched and opts.unwatched then return "Mark all…"
    if opts.watched then return "Mark all watched"
    if opts.unwatched then return "Mark all unwatched"
    return ""
end function

' Types a manual mark applies to: the server's playable leaves, plus the
' show / season containers (which mark every episode underneath).
function WatchState_IsMarkableLeaf(itemType as Dynamic) as Boolean
    t = WatchState_Str(itemType)
    return t = "movie" or t = "episode" or t = "music_video" or t = "home_video"
end function

function WatchState_IsContainer(itemType as Dynamic) as Boolean
    t = WatchState_Str(itemType)
    return t = "show" or t = "season"
end function

' Is this item (GET /items/{id} detail, a /children row or a library row)
' watched? Detail and library rows carry watch_state; /children rows carry
' a watched boolean.
function WatchState_IsWatched(item as Dynamic) as Boolean
    if item = invalid or type(item) <> "roAssociativeArray" then return false
    if WatchState_Str(item.watch_state) = "watched" then return true
    return WatchState_IsTrue(item.watched)
end function

' 0–100, or 0 when either side is missing / non-positive.
function WatchState_ProgressPct(offsetMs as Dynamic, durationMs as Dynamic) as Integer
    o = WatchState_Num(offsetMs, 0)
    d = WatchState_Num(durationMs, 0)
    if o <= 0 or d <= 0 then return 0
    ' Float division first: offset * 100 overflows a 32-bit Integer on a
    ' long title.
    pct = Int((o / d) * 100)
    if pct < 0 then return 0
    if pct > 100 then return 100
    return pct
end function

' What watch indicator a poster card shows. Returns { kind, count, pct }:
'   kind "watched"   → the check mark
'   kind "unwatched" → the unwatched-episode count badge (count > 0)
'   kind "progress"  → the bar under the poster (pct 1–100)
'   kind ""          → nothing
' Only derives from fields the server actually sent, so listings without
' watch state (older servers, music, photos) render exactly as before.
' Show-like counts (library rows) win over watch_state.
function WatchState_CardBadge(item as Dynamic) as Object
    none = { kind: "", count: 0, pct: 0 }
    if item = invalid or type(item) <> "roAssociativeArray" then return none
    if item.unwatched_count <> invalid
        unwatched = WatchState_Num(item.unwatched_count, 0)
        if item.leaf_count <> invalid and WatchState_Num(item.leaf_count, 0) = 0 then return none
        if unwatched > 0 then return { kind: "unwatched", count: unwatched, pct: 0 }
        return { kind: "watched", count: 0, pct: 0 }
    end if
    if WatchState_IsWatched(item) then return { kind: "watched", count: 0, pct: 0 }
    ' An explicit "unwatched" never shows a stale bar.
    if WatchState_Str(item.watch_state) = "unwatched" then return none
    pct = WatchState_ProgressPct(item.view_offset_ms, item.duration_ms)
    if pct > 0 then return { kind: "progress", count: 0, pct: pct }
    return none
end function

' Season-page Play when up-next isn't available (older server, failed
' call): the part-watched episode, else the first unwatched one, else the
' first. /children rows carry watched + view_offset_ms. invalid when empty.
function WatchState_PickEpisode(children as Dynamic) as Dynamic
    if children = invalid or type(children) <> "roArray" then return invalid
    firstUnwatched = invalid
    first = invalid
    for each c in children
        if c <> invalid and type(c) = "roAssociativeArray" and WatchState_Str(c.id) <> ""
            if first = invalid then first = c
            watched = WatchState_IsWatched(c)
            if not watched and WatchState_Num(c.view_offset_ms, 0) > 0 then return c
            if not watched and firstUnwatched = invalid then firstUnwatched = c
        end if
    end for
    if firstUnwatched <> invalid then return firstUnwatched
    return first
end function

' Index of the row whose id is `id`, or -1.
function WatchState_IndexOfId(rows as Dynamic, id as String) as Integer
    if rows = invalid or type(rows) <> "roArray" or id = "" then return -1
    for i = 0 to rows.Count() - 1
        r = rows[i]
        if r <> invalid and type(r) = "roAssociativeArray" and WatchState_Str(r.id) = id then return i
    end for
    return -1
end function

' Failure copy for the mark / Continue Watching writes. They share a tight
' per-user rate limit on the server (429), which gets its own sentence.
function WatchState_WriteErrorText(result as Dynamic, fallback as String) as String
    if result <> invalid and type(result) = "roAssociativeArray" and result.code <> invalid and result.code = 429
        return "Too many changes at once. Try again in a minute."
    end if
    return fallback
end function

' Confirmation copy before unwatching a whole show.
function WatchState_ConfirmUnwatchShowText(title as String) as String
    return "Mark every episode of """ + title + """ as unwatched? This clears your watched marks and resume points for the whole show."
end function
