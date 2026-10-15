' Unit tests for source/playback/Siblings.brs.

sub Main()
    testMultiDiscAlbum()
    testTrackWithoutDisc()
    testEpisodesKeepExactNext()
    testMultiEpisodeFiles()
    testGuards()

    print "DONE: Siblings_test"
end sub

' Listed in the order the server returns an album: disc, then track.
function multiDiscAlbum() as Object
    return [
        { id: "d1t1", type: "track", index: 1, disc_number: 1 },
        { id: "d1t2", type: "track", index: 2, disc_number: 1 },
        { id: "d1t3", type: "track", index: 3, disc_number: 1 },
        { id: "d2t1", type: "track", index: 1, disc_number: 2 },
        { id: "d2t2", type: "track", index: 2, disc_number: 2 },
    ]
end function

sub testMultiDiscAlbum()
    kids = multiDiscAlbum()
    runIdCase("next track on the same disc",
        nextSiblingOf({ id: "d1t1", type: "track", index: 1 }, kids), "d1t2")
    runIdCase("last track of disc 1 continues on disc 2",
        nextSiblingOf({ id: "d1t3", type: "track", index: 3 }, kids), "d2t1")
    runIdCase("disc 2 track advances on disc 2, not back to disc 1",
        nextSiblingOf({ id: "d2t1", type: "track", index: 1 }, kids), "d2t2")
    runIdCase("last track of the album has no next",
        nextSiblingOf({ id: "d2t2", type: "track", index: 2 }, kids), "")

    ' Order-independent: the same answers with disc 2 listed first.
    shuffled = [kids[4], kids[3], kids[2], kids[0], kids[1]]
    runIdCase("disc boundary with the listing shuffled",
        nextSiblingOf({ id: "d1t3", type: "track", index: 3 }, shuffled), "d2t1")
end sub

sub testTrackWithoutDisc()
    ' Parsed JSON, so the disc-less rows lack the key just as the server
    ' omits it.
    json = "[{""id"":""d2t1"",""type"":""track"",""index"":1,""disc_number"":2},"
    json = json + "{""id"":""t2"",""type"":""track"",""index"":2},{""id"":""t1"",""type"":""track"",""index"":1}]"
    kids = ParseJson(json)
    runIdCase("no disc_number reads as disc 1",
        nextSiblingOf({ id: "t1", type: "track", index: 1 }, kids), "t2")
    runIdCase("the last disc-1 track moves on to disc 2",
        nextSiblingOf({ id: "t2", type: "track", index: 2 }, kids), "d2t1")
end sub

sub testEpisodesKeepExactNext()
    kids = [
        { id: "e1", type: "episode", index: 1 },
        { id: "e3", type: "episode", index: 3 },
        { id: "e2", type: "episode", index: 2 },
    ]
    runIdCase("episode takes index + 1",
        nextSiblingOf({ id: "e1", type: "episode", index: 1 }, kids), "e2")
    runIdCase("episode after a gap has no next",
        nextSiblingOf({ id: "e3", type: "episode", index: 3 }, [kids[0], kids[1]]), "")
end sub

sub testMultiEpisodeFiles()
    ' Parsed JSON: index_end is present only on the multi-episode row.
    json = "[{""id"":""e1-2"",""type"":""episode"",""index"":1,""index_end"":2},"
    json = json + "{""id"":""e3"",""type"":""episode"",""index"":3},"
    json = json + "{""id"":""e4-5"",""type"":""episode"",""index"":4,""index_end"":5},"
    json = json + "{""id"":""e6"",""type"":""episode"",""index"":6}]"
    kids = ParseJson(json)
    runIdCase("multi-episode file is followed by the episode after its range",
        nextSiblingOf(kids[0], kids), "e3")
    runIdCase("single episode is followed by a multi-episode file",
        nextSiblingOf(kids[1], kids), "e4-5")
    runIdCase("second multi-episode file skips the episode it covers",
        nextSiblingOf(kids[2], kids), "e6")

    ' A stray single-episode row for a covered number is never picked.
    withCovered = [kids[0], { id: "e2", type: "episode", index: 2 }, kids[1]]
    runIdCase("covered episode is not looked for",
        nextSiblingOf(kids[0], withCovered), "e3")
    runIdCase("multi-episode file at the end has no next",
        nextSiblingOf(kids[0], [kids[0]]), "")
    ' An index_end that isn't past index (malformed) reads as a single episode.
    runIdCase("index_end not past index is ignored",
        nextSiblingOf({ id: "x", type: "episode", index: 3, index_end: 3 }, kids), "e4-5")
end sub

sub testGuards()
    kids = [invalid, { id: "x", type: "track" }, { id: "d1t2", type: "track", index: 2, disc_number: 1 }]
    runIdCase("malformed rows are skipped",
        nextSiblingOf({ id: "d1t1", type: "track", index: 1 }, kids), "d1t2")
    runIdCase("item without an index has no next",
        nextSiblingOf({ id: "d1t1", type: "track" }, kids), "")
    runIdCase("no listing has no next",
        nextSiblingOf({ id: "d1t1", type: "track", index: 1 }, invalid), "")
end sub

' expected "" = no next sibling (invalid).
sub runIdCase(name as String, actual as Dynamic, expected as String)
    got = ""
    if actual <> invalid then got = actual.id
    if got = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + got + "]"
    end if
end sub
