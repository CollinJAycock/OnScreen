' Unit tests for source/util/WatchState.brs. Fixtures are parsed JSON
' where the shape matters, so missing keys are missing exactly as the
' server omits them.

sub Main()
    testEpisodeCode()
    testHubTileText()
    testUpNextAction()
    testMarkAllOptions()
    testMarkTypes()
    testIsWatched()
    testProgressPct()
    testCardBadge()
    testPickEpisode()
    testIndexOfId()
    testWriteErrorText()
    testConfirmText()

    print "DONE: WatchState_test"
end sub

sub testEpisodeCode()
    runStr("code: season + episode", WatchState_EpisodeCode(2, 5), "S2 · E5")
    runStr("code: episode only", WatchState_EpisodeCode(invalid, 5), "E5")
    runStr("code: season only", WatchState_EpisodeCode(3, invalid), "S3")
    runStr("code: episode 0 is unknown", WatchState_EpisodeCode(1, 0), "S1")
    runStr("code: specials season 0 kept", WatchState_EpisodeCode(0, 2), "S0 · E2")
    runStr("code: both missing", WatchState_EpisodeCode(invalid, invalid), "")
    runStr("code: non-numeric ignored", WatchState_EpisodeCode("2", 5), "E5")
end sub

sub testHubTileText()
    nextUp = ParseJson("{""id"":""e5"",""type"":""episode"",""title"":""The Fire"",""show_title"":""Lost"",""season_number"":2,""episode_number"":5}")
    t = WatchState_HubTileText(nextUp)
    runStr("tile: episode shows the show on top", t.title, "Lost")
    runStr("tile: episode subtitle", t.subtitle, "S2 · E5 — The Fire")

    noNums = ParseJson("{""id"":""e"",""type"":""episode"",""title"":""Pilot"",""show_title"":""Lost""}")
    runStr("tile: subtitle without numbers is the title", WatchState_HubTileText(noNums).subtitle, "Pilot")

    ' Continue Watching episode tiles from older servers carry no show.
    bare = ParseJson("{""id"":""e"",""type"":""episode"",""title"":""Pilot""}")
    t = WatchState_HubTileText(bare)
    runStr("tile: episode without show keeps its title", t.title, "Pilot")
    runStr("tile: episode without show has no subtitle", t.subtitle, "")

    movie = ParseJson("{""id"":""m"",""type"":""movie"",""title"":""Heat"",""show_title"":""x""}")
    t = WatchState_HubTileText(movie)
    runStr("tile: movie keeps its title", t.title, "Heat")
    runStr("tile: movie has no subtitle", t.subtitle, "")
end sub

function upNext(json as String) as Object
    return ParseJson(json)
end function

sub testUpNextAction()
    a = WatchState_UpNextAction(upNext("{""mode"":""resume"",""episode"":{""id"":""e4"",""season_id"":""s3"",""season_number"":3,""episode_number"":4,""view_offset_ms"":120000}}"))
    runStr("upnext: resume label", a.label, "Resume S3 · E4")
    runStr("upnext: resume kind", a.kind, "resume")
    runStr("upnext: resume episode", a.episodeId, "e4")
    runStr("upnext: resume season", a.seasonId, "s3")

    a = WatchState_UpNextAction(upNext("{""mode"":""next"",""episode"":{""id"":""e5"",""season_id"":""s3"",""season_number"":3,""episode_number"":5}}"))
    runStr("upnext: next label", a.label, "Play S3 · E5")
    runStr("upnext: next kind", a.kind, "play")

    a = WatchState_UpNextAction(upNext("{""mode"":""start"",""episode"":{""id"":""e1"",""season_id"":""s1"",""season_number"":1,""episode_number"":1}}"))
    runStr("upnext: start label", a.label, "Play S1 · E1")

    a = WatchState_UpNextAction(upNext("{""mode"":""rewatch"",""episode"":{""id"":""e1"",""season_id"":""s1"",""season_number"":1,""episode_number"":1}}"))
    runStr("upnext: rewatch label", a.label, "Watch again")
    runStr("upnext: rewatch kind", a.kind, "rewatch")

    a = WatchState_UpNextAction(upNext("{""mode"":""resume"",""episode"":{""id"":""e"",""season_id"":""s""}}"))
    runStr("upnext: resume without numbers", a.label, "Resume")
    a = WatchState_UpNextAction(upNext("{""mode"":""next"",""episode"":{""id"":""e"",""season_id"":""s""}}"))
    runStr("upnext: play without numbers", a.label, "Play")

    runBool("upnext: none has no action", WatchState_UpNextAction(upNext("{""mode"":""none""}")) = invalid, true)
    runBool("upnext: unknown mode has no action", WatchState_UpNextAction(upNext("{""mode"":""later"",""episode"":{""id"":""e""}}")) = invalid, true)
    runBool("upnext: episode without id has no action", WatchState_UpNextAction(upNext("{""mode"":""next"",""episode"":{}}")) = invalid, true)
    runBool("upnext: invalid has no action", WatchState_UpNextAction(invalid) = invalid, true)
end sub

sub testMarkAllOptions()
    o = WatchState_MarkAllOptions(upNext("{""mode"":""none""}"))
    runBool("markall: none offers neither", o.watched or o.unwatched, false)
    o = WatchState_MarkAllOptions(upNext("{""mode"":""start"",""episode"":{""id"":""e""}}"))
    runBool("markall: start offers watched", o.watched and not o.unwatched, true)
    o = WatchState_MarkAllOptions(upNext("{""mode"":""rewatch"",""episode"":{""id"":""e""}}"))
    runBool("markall: rewatch offers unwatched", o.unwatched and not o.watched, true)
    o = WatchState_MarkAllOptions(upNext("{""mode"":""resume"",""episode"":{""id"":""e""}}"))
    runBool("markall: part watched offers both", o.watched and o.unwatched, true)
    o = WatchState_MarkAllOptions(invalid)
    runBool("markall: unknown offers both", o.watched and o.unwatched, true)

    runStr("markall label: both", WatchState_MarkAllLabel({ watched: true, unwatched: true }), "Mark all…")
    runStr("markall label: watched", WatchState_MarkAllLabel({ watched: true, unwatched: false }), "Mark all watched")
    runStr("markall label: unwatched", WatchState_MarkAllLabel({ watched: false, unwatched: true }), "Mark all unwatched")
    runStr("markall label: neither hides", WatchState_MarkAllLabel({ watched: false, unwatched: false }), "")
end sub

sub testMarkTypes()
    runBool("markable: movie", WatchState_IsMarkableLeaf("movie"), true)
    runBool("markable: episode", WatchState_IsMarkableLeaf("episode"), true)
    runBool("markable: home_video", WatchState_IsMarkableLeaf("home_video"), true)
    runBool("markable: track is not", WatchState_IsMarkableLeaf("track"), false)
    runBool("markable: show is a container, not a leaf", WatchState_IsMarkableLeaf("show"), false)
    runBool("markable: invalid", WatchState_IsMarkableLeaf(invalid), false)
    runBool("container: show", WatchState_IsContainer("show"), true)
    runBool("container: season", WatchState_IsContainer("season"), true)
    runBool("container: album is not", WatchState_IsContainer("album"), false)
end sub

sub testIsWatched()
    runBool("watched: detail watch_state", WatchState_IsWatched(ParseJson("{""watch_state"":""watched""}")), true)
    runBool("watched: in progress is not", WatchState_IsWatched(ParseJson("{""watch_state"":""in_progress""}")), false)
    runBool("watched: children boolean", WatchState_IsWatched(ParseJson("{""watched"":true}")), true)
    runBool("watched: children false", WatchState_IsWatched(ParseJson("{""watched"":false}")), false)
    runBool("watched: malformed flag", WatchState_IsWatched({ watched: "yes" }), false)
    runBool("watched: nothing", WatchState_IsWatched({}), false)
    runBool("watched: invalid", WatchState_IsWatched(invalid), false)
end sub

sub testProgressPct()
    runInt("pct: quarter", WatchState_ProgressPct(1500000, 6000000), 25)
    runInt("pct: missing offset", WatchState_ProgressPct(invalid, 6000000), 0)
    runInt("pct: zero duration", WatchState_ProgressPct(1000, 0), 0)
    runInt("pct: capped at 100", WatchState_ProgressPct(7000, 5000), 100)
    ' 22,000,000 ms (~6 h) × 100 overflows a 32-bit Integer; the float path doesn't.
    runInt("pct: long title doesn't overflow", WatchState_ProgressPct(22000000, 24000000), 91)
end sub

sub testCardBadge()
    b = WatchState_CardBadge(ParseJson("{""type"":""show"",""leaf_count"":10,""unwatched_count"":3}"))
    runStr("badge: show with episodes left", b.kind, "unwatched")
    runInt("badge: show count", b.count, 3)
    b = WatchState_CardBadge(ParseJson("{""type"":""show"",""leaf_count"":10,""unwatched_count"":0}"))
    runStr("badge: show fully watched", b.kind, "watched")
    b = WatchState_CardBadge(ParseJson("{""type"":""show"",""leaf_count"":0,""unwatched_count"":0}"))
    runStr("badge: empty show shows nothing", b.kind, "")
    ' Counts win over watch_state.
    b = WatchState_CardBadge(ParseJson("{""type"":""season"",""leaf_count"":5,""unwatched_count"":2,""watch_state"":""watched""}"))
    runStr("badge: counts win", b.kind, "unwatched")

    b = WatchState_CardBadge(ParseJson("{""type"":""movie"",""watch_state"":""watched""}"))
    runStr("badge: watched movie", b.kind, "watched")
    b = WatchState_CardBadge(ParseJson("{""type"":""movie"",""watch_state"":""in_progress"",""view_offset_ms"":610000,""duration_ms"":6000000}"))
    runStr("badge: in-progress movie", b.kind, "progress")
    runInt("badge: in-progress pct", b.pct, 10)
    b = WatchState_CardBadge(ParseJson("{""type"":""movie"",""watch_state"":""unwatched"",""view_offset_ms"":600000,""duration_ms"":6000000}"))
    runStr("badge: explicit unwatched never shows a bar", b.kind, "")
    b = WatchState_CardBadge(ParseJson("{""type"":""movie"",""watch_state"":""in_progress""}"))
    runStr("badge: in progress without numbers", b.kind, "")

    ' /children rows: watched boolean + view_offset_ms + duration_ms.
    b = WatchState_CardBadge(ParseJson("{""type"":""episode"",""watched"":true,""view_offset_ms"":0}"))
    runStr("badge: watched episode row", b.kind, "watched")
    b = WatchState_CardBadge(ParseJson("{""type"":""episode"",""watched"":false,""view_offset_ms"":900000,""duration_ms"":1800000}"))
    runStr("badge: part-watched episode row", b.kind, "progress")
    runInt("badge: part-watched episode pct", b.pct, 50)

    ' Hub Continue Watching tiles carry only the resume point.
    b = WatchState_CardBadge(ParseJson("{""type"":""episode"",""view_offset_ms"":300000,""duration_ms"":1200000}"))
    runStr("badge: continue watching tile", b.kind, "progress")

    ' Listings without watch state render as before.
    b = WatchState_CardBadge(ParseJson("{""type"":""album"",""title"":""x""}"))
    runStr("badge: no watch fields", b.kind, "")
    runStr("badge: invalid", WatchState_CardBadge(invalid).kind, "")
end sub

sub testPickEpisode()
    kids = ParseJson("[{""id"":""e1"",""watched"":true,""view_offset_ms"":0},{""id"":""e2"",""watched"":false,""view_offset_ms"":0},{""id"":""e3"",""watched"":false,""view_offset_ms"":5000}]")
    runStr("pick: part-watched wins", pickedId(WatchState_PickEpisode(kids)), "e3")
    kids = ParseJson("[{""id"":""e1"",""watched"":true,""view_offset_ms"":0},{""id"":""e2"",""watched"":false,""view_offset_ms"":0}]")
    runStr("pick: first unwatched", pickedId(WatchState_PickEpisode(kids)), "e2")
    kids = ParseJson("[{""id"":""e1"",""watched"":true,""view_offset_ms"":0},{""id"":""e2"",""watched"":true,""view_offset_ms"":0}]")
    runStr("pick: all watched starts over", pickedId(WatchState_PickEpisode(kids)), "e1")
    runStr("pick: empty", pickedId(WatchState_PickEpisode([])), "")
    runStr("pick: invalid", pickedId(WatchState_PickEpisode(invalid)), "")
    runStr("pick: malformed rows skipped", pickedId(WatchState_PickEpisode([invalid, {}, { id: "ok", watched: false }])), "ok")
end sub

function pickedId(c as Dynamic) as String
    if c = invalid then return ""
    return c.id
end function

sub testIndexOfId()
    rows = [{ id: "a" }, { id: "b" }, invalid, { id: "c" }]
    runInt("index: found", WatchState_IndexOfId(rows, "c"), 3)
    runInt("index: missing", WatchState_IndexOfId(rows, "z"), -1)
    runInt("index: empty id", WatchState_IndexOfId(rows, ""), -1)
    runInt("index: invalid rows", WatchState_IndexOfId(invalid, "a"), -1)
end sub

sub testWriteErrorText()
    runStr("write error: rate limited", WatchState_WriteErrorText({ code: 429 }, "fallback"), "Too many changes at once. Try again in a minute.")
    runStr("write error: other", WatchState_WriteErrorText({ code: 500 }, "fallback"), "fallback")
    runStr("write error: invalid", WatchState_WriteErrorText(invalid, "fallback"), "fallback")
end sub

sub testConfirmText()
    runStr("confirm: names the show", WatchState_ConfirmUnwatchShowText("Lost"), "Mark every episode of ""Lost"" as unwatched? This clears your watched marks and resume points for the whole show.")
end sub

sub runStr(name as String, actual as Dynamic, expected as String)
    got = "<invalid>"
    if actual <> invalid then got = actual
    if got = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + got + "]"
    end if
end sub

sub runInt(name as String, actual as Integer, expected as Integer)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
