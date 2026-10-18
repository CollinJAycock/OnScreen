' Unit tests for source/util/Watchlist.brs.

sub Main()
    testRowsFromCounts()
    testRowsMalformed()
    testRowTitle()
    testItems()
    testCardSubtitle()
    testDestination()

    print "DONE: Watchlist_test"
end sub

sub testRowsFromCounts()
    ' The /watchlist/counts payload after the data envelope comes off;
    ' on_hold is missing (0-count statuses may be left out) and dropped
    ' is an explicit 0.
    j = "{""total"":9,""statuses"":{"
    j = j + """dropped"":{""total"":0,""types"":{}},"
    j = j + """completed"":{""total"":2,""types"":{""movie"":2}},"
    j = j + """watching"":{""total"":3,""types"":{""show"":1,""episode"":2}},"
    j = j + """plan_to_watch"":{""total"":4,""types"":{""movie"":4}}}}"
    rows = Watchlist_RowsFromCounts(ParseJson(j))
    runStr("counts: status order, empties skipped", rowStatuses(rows), "plan_to_watch|watching|completed")
    runStr("counts: labels", rowLabels(rows), "Plan to Watch|Watching|Completed")
    runStr("counts: totals", rows[0].total.ToStr() + "," + rows[1].total.ToStr() + "," + rows[2].total.ToStr(), "4,3,2")

    rows = Watchlist_RowsFromCounts(ParseJson("{""total"":0,""statuses"":{}}"))
    runStr("counts: nothing listed", rowStatuses(rows), "")

    j = "{""statuses"":{""plan_to_watch"":{""total"":1},""watching"":{""total"":1},""on_hold"":{""total"":1},""completed"":{""total"":1},""dropped"":{""total"":1}}}"
    runStr("counts: all five", rowLabels(Watchlist_RowsFromCounts(ParseJson(j))), "Plan to Watch|Watching|On Hold|Completed|Dropped")
end sub

sub testRowsMalformed()
    runStr("malformed: invalid", rowStatuses(Watchlist_RowsFromCounts(invalid)), "")
    runStr("malformed: array", rowStatuses(Watchlist_RowsFromCounts([])), "")
    runStr("malformed: no statuses", rowStatuses(Watchlist_RowsFromCounts({ total: 3 })), "")
    runStr("malformed: statuses not an object", rowStatuses(Watchlist_RowsFromCounts({ statuses: "x" })), "")
    rows = Watchlist_RowsFromCounts({ statuses: { plan_to_watch: "x", watching: { total: "3" }, on_hold: { types: {} }, dropped: { total: 1 } } })
    runStr("malformed: bad entries count as empty", rowStatuses(rows), "dropped")
    ' Unknown statuses from a newer server are ignored.
    rows = Watchlist_RowsFromCounts({ statuses: { rewatching: { total: 5 } } })
    runStr("malformed: unknown status ignored", rowStatuses(rows), "")
end sub

sub testRowTitle()
    runStr("title: with count", Watchlist_RowTitle("Plan to Watch", 12), "Plan to Watch (12)")
    runStr("title: one", Watchlist_RowTitle("On Hold", 1), "On Hold (1)")
end sub

sub testItems()
    j = "[{""id"":""a"",""type"":""movie"",""title"":""Alien""},{""title"":""no id""},{""id"":"""",""type"":""movie""},{""id"":""b"",""type"":""show""}]"
    items = Watchlist_Items(ParseJson(j))
    runStr("items: only rows with an id", ids(items), "a|b")
    runStr("items: invalid", ids(Watchlist_Items(invalid)), "")
    runStr("items: not an array", ids(Watchlist_Items({ id: "x" })), "")
    runStr("items: junk entries dropped", ids(Watchlist_Items([invalid, "x", 3, { id: 7 }, { id: "ok" }])), "ok")
end sub

sub testCardSubtitle()
    ep = ParseJson("{""id"":""e"",""type"":""episode"",""title"":""Hello, Ms. Cobel"",""index"":5,""parent_title"":""Season 2"",""parent_index"":2,""grandparent_title"":""Severance""}")
    runStr("subtitle: episode", Watchlist_CardSubtitle(ep), "Severance · S2 · E5")
    runStr("subtitle: episode without a show", Watchlist_CardSubtitle({ type: "episode", index: 5, parent_index: 2 }), "S2 · E5")
    runStr("subtitle: episode without numbers", Watchlist_CardSubtitle({ type: "episode", grandparent_title: "Lost" }), "Lost")
    runStr("subtitle: specials season 0", Watchlist_CardSubtitle({ type: "episode", grandparent_title: "Lost", parent_index: 0, index: 1 }), "Lost · S0 · E1")
    runStr("subtitle: season", Watchlist_CardSubtitle({ type: "season", title: "Season 1", parent_title: "Lost" }), "Lost")
    runStr("subtitle: track", Watchlist_CardSubtitle({ type: "track", parent_title: "Revolver", grandparent_title: "The Beatles" }), "The Beatles · Revolver")
    runStr("subtitle: album", Watchlist_CardSubtitle({ type: "album", parent_title: "The Beatles", year: 1966 }), "The Beatles · 1966")
    runStr("subtitle: audiobook without author", Watchlist_CardSubtitle({ type: "audiobook", year: 2019 }), "2019")
    runStr("subtitle: movie year", Watchlist_CardSubtitle(ParseJson("{""type"":""movie"",""year"":1979}")), "1979")
    runStr("subtitle: movie without year", Watchlist_CardSubtitle({ type: "movie" }), "")
    runStr("subtitle: show year", Watchlist_CardSubtitle({ type: "show", year: 2004 }), "2004")
    runStr("subtitle: invalid", Watchlist_CardSubtitle(invalid), "")
end sub

sub testDestination()
    runStr("dest: movie", Watchlist_Destination("movie"), "DetailScene")
    runStr("dest: show", Watchlist_Destination("show"), "DetailScene")
    runStr("dest: episode opens its page", Watchlist_Destination("episode"), "DetailScene")
    runStr("dest: track opens its page", Watchlist_Destination("track"), "DetailScene")
    runStr("dest: photo", Watchlist_Destination("photo"), "PhotoScene")
    runStr("dest: playlist", Watchlist_Destination("playlist"), "CollectionScene")
    runStr("dest: missing type", Watchlist_Destination(invalid), "DetailScene")
end sub

function rowStatuses(rows as Object) as String
    out = ""
    for i = 0 to rows.Count() - 1
        if i > 0 then out = out + "|"
        out = out + rows[i].status
    end for
    return out
end function

function rowLabels(rows as Object) as String
    out = ""
    for i = 0 to rows.Count() - 1
        if i > 0 then out = out + "|"
        out = out + rows[i].label
    end for
    return out
end function

function ids(items as Object) as String
    out = ""
    for i = 0 to items.Count() - 1
        if i > 0 then out = out + "|"
        out = out + items[i].id
    end for
    return out
end function

sub runStr(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
