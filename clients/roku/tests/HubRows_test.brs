' Unit tests for source/util/HubRows.brs.

sub Main()
    testOrder()
    testLegacySplit()
    testEmptyAndMalformed()
    testRemoveKeepsRow()
    testRemoveLastInRow()
    testRemoveMissing()

    print "DONE: HubRows_test"
end sub

function hubJson() as String
    j = "{""continue_watching_tv"":[{""id"":""tv1"",""type"":""episode""}],"
    j = j + """continue_watching_movies"":[{""id"":""mv1"",""type"":""movie""},{""id"":""mv2"",""type"":""movie""}],"
    j = j + """continue_watching_other"":[],"
    j = j + """next_up"":[{""id"":""nu1"",""type"":""episode"",""show_title"":""Lost""}],"
    j = j + """plan_to_watch"":[{""id"":""pw1"",""type"":""movie""}],"
    j = j + """trending"":[{""id"":""tr1"",""type"":""movie""}],"
    j = j + """recently_added"":[{""id"":""ra1"",""type"":""movie""}]}"
    return j
end function

sub testOrder()
    rows = HubRows_Build(ParseJson(hubJson()))
    runStr("order: row titles", rowTitles(rows), "Continue Watching TV Shows|Continue Watching Movies|Next Up|Plan to Watch|Trending|Recently Added")
    runStr("order: row kinds", rowKinds(rows), "continue|continue|next_up|plan|trending|recent")
    runStr("order: next up items", rows[2].items[0].id, "nu1")
end sub

sub testLegacySplit()
    ' Pre-split servers: only the combined feed, split here by type.
    j = "{""continue_watching"":[{""id"":""e"",""type"":""episode""},{""id"":""m"",""type"":""movie""},{""id"":""b"",""type"":""audiobook""}],""recently_added"":[]}"
    rows = HubRows_Build(ParseJson(j))
    runStr("legacy: split rows", rowTitles(rows), "Continue Watching TV Shows|Continue Watching Movies|Continue Watching")
    runStr("legacy: other bucket", rows[2].items[0].id, "b")

    ' A server that sends the split arrays wins even when the legacy
    ' feed is present too (both are sent for one release cycle).
    j = "{""continue_watching"":[{""id"":""e"",""type"":""episode""}],""continue_watching_tv"":[],""continue_watching_movies"":[],""continue_watching_other"":[]}"
    runStr("legacy: split arrays win", rowTitles(HubRows_Build(ParseJson(j))), "")
end sub

sub testEmptyAndMalformed()
    runStr("empty: no rows", rowTitles(HubRows_Build(ParseJson("{}"))), "")
    runStr("empty: invalid hub", rowTitles(HubRows_Build(invalid)), "")
    ' A pre-v2.5 server omits next_up / plan_to_watch.
    rows = HubRows_Build(ParseJson("{""trending"":[{""id"":""t"",""type"":""movie""}]}"))
    runStr("empty: rows missing on older servers", rowTitles(rows), "Trending")
    rows = HubRows_Build({ next_up: [invalid, "x", { id: "ok", type: "episode" }] })
    runStr("malformed: bad tiles dropped", rows[0].items.Count().ToStr() + ":" + rows[0].items[0].id, "1:ok")
    rows = HubRows_Build({ next_up: "nope" })
    runStr("malformed: non-array row skipped", rowTitles(rows), "")
end sub

sub testRemoveKeepsRow()
    rows = HubRows_Build(ParseJson(hubJson()))
    out = HubRows_RemoveItem(rows, 1, "mv1")
    runBool("remove: removed", out.removed, true)
    runStr("remove: rows kept", rowTitles(out.rows), "Continue Watching TV Shows|Continue Watching Movies|Next Up|Plan to Watch|Trending|Recently Added")
    runStr("remove: neighbour left", out.rows[1].items[0].id, "mv2")
    runStr("remove: focus on the neighbour", focusStr(out.focus), "1,0")
    ' The input rows are untouched (the scene swaps the list only once the
    ' server confirms).
    runStr("remove: input untouched", rows[1].items.Count().ToStr(), "2")

    out = HubRows_RemoveItem(rows, 1, "mv2")
    runStr("remove: last tile focuses the new last", focusStr(out.focus), "1,0")
end sub

sub testRemoveLastInRow()
    rows = HubRows_Build(ParseJson(hubJson()))
    out = HubRows_RemoveItem(rows, 0, "tv1")
    runStr("remove: emptied row dropped", rowTitles(out.rows), "Continue Watching Movies|Next Up|Plan to Watch|Trending|Recently Added")
    runStr("remove: focus moves to the row that slid up", focusStr(out.focus), "0,0")

    only = HubRows_Build({ continue_watching_tv: [{ id: "a", type: "episode" }] })
    out = HubRows_RemoveItem(only, 0, "a")
    runStr("remove: last row gone", rowTitles(out.rows), "")
    runStr("remove: focus clamps to 0", focusStr(out.focus), "0,0")

    two = HubRows_Build({ trending: [{ id: "t", type: "movie" }], recently_added: [{ id: "r", type: "movie" }] })
    out = HubRows_RemoveItem(two, 1, "r")
    runStr("remove: bottom row emptied focuses the one above", focusStr(out.focus), "0,0")
end sub

sub testRemoveMissing()
    rows = HubRows_Build(ParseJson(hubJson()))
    out = HubRows_RemoveItem(rows, 1, "nope")
    runBool("missing: not removed", out.removed, false)
    runStr("missing: rows unchanged", rowTitles(out.rows), rowTitles(rows))
    out = HubRows_RemoveItem(rows, 9, "mv1")
    runBool("missing: bad row index", out.removed, false)
end sub

function rowTitles(rows as Object) as String
    out = ""
    for i = 0 to rows.Count() - 1
        if i > 0 then out = out + "|"
        out = out + rows[i].title
    end for
    return out
end function

function rowKinds(rows as Object) as String
    out = ""
    for i = 0 to rows.Count() - 1
        if i > 0 then out = out + "|"
        out = out + rows[i].kind
    end for
    return out
end function

function focusStr(f as Object) as String
    return f[0].ToStr() + "," + f[1].ToStr()
end function

sub runStr(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
