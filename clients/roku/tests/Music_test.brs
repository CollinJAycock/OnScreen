' Unit tests for source/util/Music.brs, the music discovery paths in
' source/api/Endpoints.brs and nextInQueue in source/playback/Siblings.brs.

sub Main()
    testPaths()
    testRows()
    testQueue()
    testNextInQueue()
    testAppearanceSubtitle()

    print "DONE: Music_test"
end sub

sub testPaths()
    id = "550e8400-e29b-41d4-a716-446655440000"
    runStr("path: similar artists", ApiMusicSimilarArtists(id), "/api/v1/music/artists/" + id + "/similar?limit=20")
    runStr("path: appears on", ApiMusicAppearsOn(id), "/api/v1/music/artists/" + id + "/appears-on?limit=50")
    runStr("path: instant mix", ApiMusicMix(id), "/api/v1/music/mix?seed=" + id)
end sub

sub testRows()
    list = ParseJson("[{""id"":""a1"",""title"":""Air"",""score"":0.8,""shared_genres"":[""Electronic""],""co_listened"":true},{""title"":""no id""},5]")
    rows = Music_Rows(list)
    runInt("rows: only rows with an id", rows.Count(), 1)
    runStr("rows: kept row", rows[0].title, "Air")
    runInt("rows: not a list", Music_Rows({ error: "x" }).Count(), 0)
    runInt("rows: invalid", Music_Rows(invalid).Count(), 0)
end sub

sub testQueue()
    list = ParseJson("[{""id"":""t1"",""title"":""Seed"",""album_id"":""al"",""file_id"":""f1""},{""id"":""t2"",""title"":""Next"",""file_id"":""f2""},{""title"":""broken""}]")
    q = Music_QueueFromMix(list)
    runInt("queue: malformed tracks dropped", q.Count(), 2)
    runStr("queue: seed leads", q[0].id, "t1")
    runStr("queue: titles kept", q[1].title, "Next")
    runStr("queue: entries are tracks", q[1].type, "track")
    runInt("queue: empty mix", Music_QueueFromMix(ParseJson("[]")).Count(), 0)
end sub

sub testNextInQueue()
    q = [{ id: "t1", title: "a" }, { id: "t2", title: "b" }, { id: "t3", title: "c" }]
    runStr("queue next: middle", idOf(nextInQueue(q, "t2")), "t3")
    runStr("queue next: first", idOf(nextInQueue(q, "t1")), "t2")
    runStr("queue next: last has none", idOf(nextInQueue(q, "t3")), "")
    runStr("queue next: not in queue", idOf(nextInQueue(q, "zz")), "")
    runStr("queue next: no queue", idOf(nextInQueue(invalid, "t1")), "")
    runStr("queue next: malformed entry skipped", idOf(nextInQueue([{ id: "t1" }, invalid, { id: "t3" }], "t1")), "t3")
end sub

sub testAppearanceSubtitle()
    row = ParseJson("{""id"":""t"",""title"":""Song"",""role"":""featured"",""album_title"":""Collab"",""album_artist_title"":""Other Artist""}")
    runStr("appears: album + artist", Music_AppearanceSubtitle(row), "Collab · Other Artist")
    runStr("appears: album only", Music_AppearanceSubtitle(ParseJson("{""album_title"":""Hits""}")), "Hits")
    runStr("appears: nothing", Music_AppearanceSubtitle({}), "")
end sub

function idOf(v as Dynamic) as String
    if v = invalid then return ""
    return v.id
end function

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
