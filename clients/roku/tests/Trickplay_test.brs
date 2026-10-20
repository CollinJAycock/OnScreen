' Unit tests for source/playback/Trickplay.brs.

sub Main()
    testBifUrl()
    testAppliesToPrimaryVersion()
    testAppliesToStackContainingFirstFile()
    testNotForOtherVersion()
    testNotForOffsetSession()
    testNotForBadInput()
    testNotForAudio()
    testAttachSetsAllThreeFields()
    testAttachSkipsWithoutToken()
    testAttachSkipsWhenNotApplicable()

    print "DONE: Trickplay_test"
end sub

sub testBifUrl()
    ' Pinned: the server's RequiredAllowQueryToken route + ?token= carrier.
    runStringCase("bif url",
        Trickplay_BifUrl("http://x:7070", "550e8400-e29b-41d4-a716-446655440000", "tok"),
        "http://x:7070/trickplay/550e8400-e29b-41d4-a716-446655440000/index.bif?token=tok")
end sub

sub testAppliesToPrimaryVersion()
    files = [{ id: "a" }, { id: "b" }]
    runBoolCase("first file's version applies", Trickplay_Applies("movie", files, [files[0]], 0), true)
end sub

sub testAppliesToStackContainingFirstFile()
    ' A stack's parts in part order — the server's first file may be any part.
    files = [{ id: "p2", part_index: 2 }, { id: "p1", part_index: 1 }]
    runBoolCase("stack holding the first file applies", Trickplay_Applies("movie", files, [files[1], files[0]], 0), true)
end sub

sub testNotForOtherVersion()
    files = [{ id: "a" }, { id: "b" }]
    runBoolCase("another version gets none", Trickplay_Applies("movie", files, [files[1]], 0), false)
end sub

sub testNotForOffsetSession()
    files = [{ id: "a" }]
    runBoolCase("session starting mid-item gets none", Trickplay_Applies("movie", files, files, 125000), false)
end sub

sub testNotForBadInput()
    runBoolCase("no item files", Trickplay_Applies("movie", [], [{ id: "a" }], 0), false)
    runBoolCase("invalid item files", Trickplay_Applies("movie", invalid, [{ id: "a" }], 0), false)
    runBoolCase("invalid version files", Trickplay_Applies("movie", [{ id: "a" }], invalid, 0), false)
    runBoolCase("first file without id", Trickplay_Applies("movie", [{}], [{ id: "a" }], 0), false)
    runBoolCase("version file without id", Trickplay_Applies("movie", [{ id: "a" }], [{}, invalid], 0), false)
end sub

sub testNotForAudio()
    files = [{ id: "a" }]
    for each t in ["track", "audiobook", "audiobook_chapter", "podcast_episode"]
        runBoolCase(t + " gets none", Trickplay_Applies(t, files, files, 0), false)
    end for
    runBoolCase("episode applies", Trickplay_Applies("episode", files, files, 0), true)
    runBoolCase("unknown type applies", Trickplay_Applies(invalid, files, files, 0), true)
end sub

sub testAttachSetsAllThreeFields()
    content = {}
    Trickplay_AttachBif(content, "http://x", "item1", "tok", true)
    want = "http://x/trickplay/item1/index.bif?token=tok"
    runStringCase("HDBifUrl set", valueOr(content.HDBifUrl), want)
    runStringCase("SDBifUrl set", valueOr(content.SDBifUrl), want)
    runStringCase("FHDBifUrl set", valueOr(content.FHDBifUrl), want)
end sub

sub testAttachSkipsWithoutToken()
    content = {}
    Trickplay_AttachBif(content, "http://x", "item1", "", true)
    runBoolCase("empty token leaves no bif", content.HDBifUrl = invalid, true)
    content = {}
    Trickplay_AttachBif(content, "http://x", "item1", invalid, true)
    runBoolCase("invalid token leaves no bif", content.HDBifUrl = invalid, true)
end sub

sub testAttachSkipsWhenNotApplicable()
    content = {}
    Trickplay_AttachBif(content, "http://x", "item1", "tok", false)
    runBoolCase("not applicable leaves no bif", content.HDBifUrl = invalid, true)
end sub

function valueOr(v as Dynamic) as String
    if v = invalid then return "<invalid>"
    return v
end function

sub runBoolCase(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub

sub runStringCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
