' Unit tests for source/playback/Siblings.brs.

sub Main()
    testMultiDiscAlbum()
    testTrackWithoutDisc()
    testEpisodesKeepExactNext()
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
