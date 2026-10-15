' Unit tests for source/playback/Versions.brs (editions + multi-part movies)
' and Playback_ForStack in source/playback/Decide.brs. Fixtures are parsed
' JSON, so absent fields are absent exactly as the server omits them.

sub Main()
    testGroupPlainFiles()
    testGroupStack()
    testTwoStackedEditions()
    testLoneOrMismatchedParts()
    testPick()
    testLabels()
    testForStack()

    print "DONE: Versions_test"
end sub

' Director's Cut (one file) + the theatrical cut in two parts, listed the
' way the server orders a stack: parts together, part order.
function editionFiles() as Object
    json = "["
    json = json + "{""id"":""dc"",""container"":""mp4"",""video_codec"":""hevc"",""resolution_h"":2160,""edition"":""Director's Cut""},"
    json = json + "{""id"":""p1"",""container"":""avi"",""video_codec"":""h264"",""resolution_h"":480,""part_index"":1},"
    json = json + "{""id"":""p2"",""container"":""avi"",""video_codec"":""h264"",""resolution_h"":480,""part_index"":2}"
    json = json + "]"
    return ParseJson(json)
end function

sub testGroupPlainFiles()
    files = ParseJson("[{""id"":""a"",""resolution_h"":2160},{""id"":""b"",""resolution_h"":1080}]")
    v = Versions_Group(files)
    runInt("plain: one version per file", v.Count(), 2)
    runStr("plain: server order kept", v[0].primary.id, "a")
    runInt("plain: one file each", v[1].files.Count(), 1)
    runInt("plain: no files", Versions_Group(invalid).Count(), 0)
    runInt("plain: malformed rows skipped", Versions_Group([invalid, {}, { id: "x" }]).Count(), 1)
end sub

sub testGroupStack()
    v = Versions_Group(editionFiles())
    runInt("stack: parts are one version", v.Count(), 2)
    runStr("stack: edition first", v[0].primary.id, "dc")
    runStr("stack: part 1 is the primary", v[1].primary.id, "p1")
    runInt("stack: both parts in the version", v[1].files.Count(), 2)

    ' Listed out of order: still part 1 first.
    files = editionFiles()
    shuffled = [files[2], files[0], files[1]]
    v = Versions_Group(shuffled)
    runInt("stack shuffled: two versions", v.Count(), 2)
    runStr("stack shuffled: part 1 leads its version", v[0].primary.id, "p1")
    runStr("stack shuffled: part 2 follows", v[0].files[1].id, "p2")
end sub

sub testTwoStackedEditions()
    json = "["
    json = json + "{""id"":""e1"",""container"":""mkv"",""edition"":""Extended"",""part_index"":1},"
    json = json + "{""id"":""e2"",""container"":""mkv"",""edition"":""Extended"",""part_index"":2},"
    json = json + "{""id"":""t1"",""container"":""mkv"",""part_index"":1},"
    json = json + "{""id"":""t2"",""container"":""mkv"",""part_index"":2}"
    json = json + "]"
    v = Versions_Group(ParseJson(json))
    runInt("editions: two stacked versions", v.Count(), 2)
    runStr("editions: extended stack", v[0].files[1].id, "e2")
    runStr("editions: theatrical stack", v[1].files[1].id, "t2")
end sub

sub testLoneOrMismatchedParts()
    ' A part number with no partner is a plain version (the server wouldn't
    ' send it, but don't hang a one-part stack on it).
    v = Versions_Group(ParseJson("[{""id"":""a"",""part_index"":1},{""id"":""b""}]"))
    runInt("lone part: plain versions", v.Count(), 2)
    runBool("lone part: not a stack", Versions_StackOf(ParseJson("[{""id"":""a"",""part_index"":1}]"), { id: "a", part_index: 1 }) = invalid, true)
    ' Different containers don't stack together.
    v = Versions_Group(ParseJson("[{""id"":""a"",""container"":""avi"",""part_index"":1},{""id"":""b"",""container"":""mkv"",""part_index"":2}]"))
    runInt("mismatched parts: separate", v.Count(), 2)
end sub

sub testPick()
    files = editionFiles()
    runStr("pick: no file id → first version", Versions_Pick(files, "").primary.id, "dc")
    runStr("pick: invalid file id → first version", Versions_Pick(files, invalid).primary.id, "dc")
    runStr("pick: by primary", Versions_Pick(files, "p1").primary.id, "p1")
    ' Naming part 2 still plays the stack from part 1.
    runStr("pick: a part names its whole stack", Versions_Pick(files, "p2").primary.id, "p1")
    runStr("pick: unknown id → first version", Versions_Pick(files, "zz").primary.id, "dc")
    runBool("pick: no files", Versions_Pick([], "") = invalid, true)
end sub

sub testLabels()
    v = Versions_Group(editionFiles())
    runStr("label: edition", Versions_Label(v[0], 1), "Director's Cut")
    runStr("label: tech + parts", Versions_Label(v[1], 2), "480p · H.264 · 2 parts")
    runStr("label: 4K hevc", Versions_TechLabel({ resolution_h: 2160, video_codec: "hevc" }), "4K · HEVC")
    runStr("label: unknown codec upper-cased", Versions_TechLabel({ resolution_h: 1080, video_codec: "vc1" }), "1080p · VC1")
    runStr("label: nothing known", Versions_Label({ primary: { id: "x" }, files: [{ id: "x" }] }, 3), "Version 3")
    runStr("label: blank edition ignored", Versions_Label({ primary: { id: "x", edition: "  ", resolution_h: 720 }, files: [{ id: "x" }] }, 1), "720p")
end sub

sub testForStack()
    d = Playback_ForStack({ mode: "direct", height: 1080, videoCopy: false, supportsHevc: true })
    runStr("stack: never direct", d.mode, "remux")
    runBool("stack: copies the video", d.videoCopy, true)
    runBool("stack: keeps the hevc flag", d.supportsHevc, true)
    runStr("stack: remux stands", Playback_ForStack({ mode: "remux", height: 1080, videoCopy: true, supportsHevc: false }).mode, "remux")
    runStr("stack: transcode stands", Playback_ForStack({ mode: "transcode", height: 2160, videoCopy: false, supportsHevc: false }).mode, "transcode")
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
