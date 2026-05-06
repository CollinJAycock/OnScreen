' Unit tests for source/playback/Trickplay.brs.

sub Main()
    testParseTimestamps()
    testParseCueLine()
    testParseFullVtt()
    testParseFullVttCRLF()
    testFindCue()

    print "DONE: Trickplay_test"
end sub

sub testParseTimestamps()
    runIntCase("hh:mm:ss.mmm full form",
        trickplayParseTimestampMs("00:00:10.500"), 10500)
    runIntCase("hh:mm:ss.mmm full form, hour > 0",
        trickplayParseTimestampMs("01:02:03.250"), 3723250)
    runIntCase("mm:ss.mmm short form",
        trickplayParseTimestampMs("00:10.000"), 10000)
    runIntCase("missing millis",
        trickplayParseTimestampMs("00:00:05"), 5000)
    runIntCase("1-digit millis pads",
        trickplayParseTimestampMs("00:00:05.5"), 5500)
    runIntCase("2-digit millis pads",
        trickplayParseTimestampMs("00:00:05.50"), 5500)
    runIntCase("empty string",
        trickplayParseTimestampMs(""), -1)
end sub

sub testParseCueLine()
    cue = trickplayParseCueLine("sprite_0.jpg#xywh=0,0,160,90", 0, 10000)
    if cue = invalid
        print "FAIL: cue line parses — got invalid"
    else
        runIntCase("cue x", cue.x, 0)
        runIntCase("cue y", cue.y, 0)
        runIntCase("cue w", cue.w, 160)
        runIntCase("cue h", cue.h, 90)
        runStringCase("cue sprite path", cue.sprite_path, "sprite_0.jpg")
        runIntCase("cue start_ms", cue.start_ms, 0)
        runIntCase("cue end_ms", cue.end_ms, 10000)
    end if

    ' Server commonly returns a relative path with the item id —
    ' make sure the parser doesn't mangle it.
    cue2 = trickplayParseCueLine(
        "/api/v1/items/abc/trickplay/sprite_3.jpg#xywh=320,180,160,90", 0, 0)
    if cue2 = invalid
        print "FAIL: relative-path cue parses — got invalid"
    else
        runStringCase("nested-path sprite_path",
            cue2.sprite_path, "/api/v1/items/abc/trickplay/sprite_3.jpg")
        runIntCase("nested-path x", cue2.x, 320)
    end if

    ' Missing xywh fragment — must return invalid (cue is
    ' unrenderable without coords).
    if trickplayParseCueLine("sprite_0.jpg", 0, 10000) <> invalid
        print "FAIL: cue without xywh fragment should be invalid"
    else
        print "PASS: cue without xywh is rejected"
    end if

    ' Truncated xywh (only 3 coords) — also invalid.
    if trickplayParseCueLine("sprite_0.jpg#xywh=0,0,160", 0, 10000) <> invalid
        print "FAIL: cue with 3-coord xywh should be invalid"
    else
        print "PASS: 3-coord xywh is rejected"
    end if
end sub

sub testParseFullVtt()
    nl = Chr(10)
    vtt = "WEBVTT" + nl + nl
    vtt = vtt + "00:00:00.000 --> 00:00:10.000" + nl
    vtt = vtt + "sprite_0.jpg#xywh=0,0,160,90" + nl + nl
    vtt = vtt + "00:00:10.000 --> 00:00:20.000" + nl
    vtt = vtt + "sprite_0.jpg#xywh=160,0,160,90" + nl + nl
    vtt = vtt + "00:00:20.000 --> 00:00:30.000" + nl
    vtt = vtt + "sprite_1.jpg#xywh=0,0,160,90" + nl

    cues = Trickplay_ParseVtt(vtt)
    runIntCase("3 cues parsed", cues.Count(), 3)
    if cues.Count() = 3
        runIntCase("cue 0 start_ms", cues[0].start_ms, 0)
        runIntCase("cue 0 end_ms", cues[0].end_ms, 10000)
        runStringCase("cue 0 sprite", cues[0].sprite_path, "sprite_0.jpg")
        runIntCase("cue 1 x offset", cues[1].x, 160)
        runStringCase("cue 2 sprite", cues[2].sprite_path, "sprite_1.jpg")
    end if
end sub

sub testParseFullVttCRLF()
    ' Server might send \r\n line endings — verify the parser
    ' tolerates them. Roku's roUrlTransfer body is whatever the
    ' server emitted.
    crlf = Chr(13) + Chr(10)
    vtt = "WEBVTT" + crlf + crlf
    vtt = vtt + "00:00:00.000 --> 00:00:05.000" + crlf
    vtt = vtt + "sprite_0.jpg#xywh=0,0,80,45" + crlf
    cues = Trickplay_ParseVtt(vtt)
    runIntCase("crlf single cue parsed", cues.Count(), 1)
end sub

sub testFindCue()
    cues = [
        { start_ms: 0,     end_ms: 10000, sprite_path: "a", x: 0, y: 0, w: 80, h: 45 },
        { start_ms: 10000, end_ms: 20000, sprite_path: "b", x: 80, y: 0, w: 80, h: 45 },
        { start_ms: 20000, end_ms: 30000, sprite_path: "c", x: 0, y: 45, w: 80, h: 45 },
    ]
    runStringCase("find cue at start of first",
        Trickplay_FindCue(cues, 0).sprite_path, "a")
    runStringCase("find cue mid-second",
        Trickplay_FindCue(cues, 15000).sprite_path, "b")
    ' end_ms is exclusive — the cue covers [start, end). 20000 is
    ' the start of the next cue, not the end of the previous.
    runStringCase("end_ms boundary belongs to next cue",
        Trickplay_FindCue(cues, 20000).sprite_path, "c")
    if Trickplay_FindCue(cues, 30000) <> invalid
        print "FAIL: position past last cue should return invalid"
    else
        print "PASS: position past last cue returns invalid"
    end if
    if Trickplay_FindCue([], 5000) <> invalid
        print "FAIL: empty cues array should return invalid"
    else
        print "PASS: empty cues array returns invalid"
    end if
end sub

sub runIntCase(name as String, actual as Integer, expected as Integer)
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
