' Unit tests for source/playback/Subtitles.brs: what a subtitle pick does
' (firmware, burn-in re-issue, nothing, unsupported) and the row label.
' Fixtures are parsed JSON, so absent fields are absent exactly as an
' older server omits them.

sub Main()
    testIsImage()
    testDirectPlay()
    testBurnIn()
    testWhileBurned()
    testStacked()
    testOlderServer()
    testLabels()

    print "DONE: Subtitles_test"
end sub

' Stream 3: English text (SRT). Stream 4: English PGS, OCR'd. Stream 6:
' French VobSub, no text version. Stream 7: image track with no index.
function streams() as Object
    json = "["
    json = json + "{""index"":3,""codec"":""subrip"",""language"":""eng"",""image_based"":false},"
    json = json + "{""index"":4,""codec"":""hdmv_pgs_subtitle"",""language"":""eng"",""image_based"":true,""text_version_id"":""ext-1""},"
    json = json + "{""index"":6,""codec"":""dvd_subtitle"",""language"":""fre"",""image_based"":true},"
    json = json + "{""codec"":""hdmv_pgs_subtitle"",""image_based"":true}"
    json = json + "]"
    return ParseJson(json)
end function

function sessionCtx(burnIndex as Integer) as Object
    return { serverSession: true, stacked: false, burnIndex: burnIndex }
end function

sub testIsImage()
    s = streams()
    runBool("image: text track", Subtitles_IsImage(s[0]), false)
    runBool("image: pgs", Subtitles_IsImage(s[1]), true)
    runBool("image: invalid", Subtitles_IsImage(invalid), false)
    runBool("image: older server, no field", Subtitles_IsImage(ParseJson("{""codec"":""hdmv_pgs_subtitle""}")), false)
    runBool("will burn: pgs in a session", Subtitles_WillBurn(s[1], true), true)
    runBool("will burn: pgs on direct play", Subtitles_WillBurn(s[1], false), false)
    runBool("will burn: text in a session", Subtitles_WillBurn(s[0], true), false)
    runBool("will burn: no index", Subtitles_WillBurn(s[3], true), false)
end sub

' Direct play leaves every track to the firmware, as before.
sub testDirectPlay()
    s = streams()
    ctx = { serverSession: false, stacked: false, burnIndex: -1 }
    runStr("direct: image track", Subtitles_PickPlan(s, 1, ctx).kind, "firmware")
    runStr("direct: text track", Subtitles_PickPlan(s, 0, ctx).kind, "firmware")
    runStr("direct: off", Subtitles_PickPlan(s, -1, ctx).kind, "firmware")
end sub

sub testBurnIn()
    s = streams()
    ' The channel can't side-load the OCR text version, so even a track
    ' with text_version_id burns in.
    p = Subtitles_PickPlan(s, 1, sessionCtx(-1))
    runStr("burn: image track re-issues", p.kind, "reissue")
    runInt("burn: absolute index, not position", p.burnIndex, 4)
    p = Subtitles_PickPlan(s, 2, sessionCtx(-1))
    runInt("burn: no text version burns too", p.burnIndex, 6)
    runStr("burn: text track stays with the firmware", Subtitles_PickPlan(s, 0, sessionCtx(-1)).kind, "firmware")
    runStr("burn: off with nothing burned", Subtitles_PickPlan(s, -1, sessionCtx(-1)).kind, "firmware")
    runStr("burn: image track without an index", Subtitles_PickPlan(s, 3, sessionCtx(-1)).kind, "firmware")
    runStr("burn: row past the end", Subtitles_PickPlan(s, 9, sessionCtx(-1)).kind, "none")
    runStr("burn: no streams", Subtitles_PickPlan(invalid, 0, sessionCtx(-1)).kind, "none")
end sub

sub testWhileBurned()
    s = streams()
    runStr("burned: same track again", Subtitles_PickPlan(s, 1, sessionCtx(4)).kind, "none")
    p = Subtitles_PickPlan(s, -1, sessionCtx(4))
    runStr("burned: off re-issues", p.kind, "reissue")
    runInt("burned: off drops the burn", p.burnIndex, -1)
    p = Subtitles_PickPlan(s, 0, sessionCtx(4))
    runStr("burned: text track re-issues", p.kind, "reissue")
    runInt("burned: text track drops the burn", p.burnIndex, -1)
    p = Subtitles_PickPlan(s, 2, sessionCtx(4))
    runStr("burned: other image track re-issues", p.kind, "reissue")
    runInt("burned: other image track burns it", p.burnIndex, 6)
    ' Stream 0 is a real index: burned, then off.
    runStr("burned: index 0 off", Subtitles_PickPlan(s, -1, sessionCtx(0)).kind, "reissue")
end sub

sub testStacked()
    s = streams()
    ctx = { serverSession: true, stacked: true, burnIndex: -1 }
    runStr("stacked: image track", Subtitles_PickPlan(s, 1, ctx).kind, "unsupported")
    runStr("stacked: text track", Subtitles_PickPlan(s, 0, ctx).kind, "firmware")
end sub

' A server that predates image_based marks nothing: today's behaviour.
sub testOlderServer()
    old = ParseJson("[{""index"":4,""codec"":""hdmv_pgs_subtitle"",""language"":""eng""}]")
    runStr("older server: pgs stays with the firmware", Subtitles_PickPlan(old, 0, sessionCtx(-1)).kind, "firmware")
    runStr("older server: label", Subtitles_LangLabel(old[0], Subtitles_WillBurn(old[0], true)), "eng")
end sub

sub testLabels()
    s = streams()
    runStr("label: burned in", Subtitles_LangLabel(s[1], true), "eng (burned in)")
    runStr("label: plain", Subtitles_LangLabel(s[1], false), "eng")
    runStr("label: no language", Subtitles_LangLabel(s[3], false), "und")
    runStr("label: no language, burned in", Subtitles_LangLabel(s[3], true), "und (burned in)")
    runStr("label: invalid stream", Subtitles_LangLabel(invalid, false), "und")
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

sub runInt(name as String, actual as Dynamic, expected as Integer)
    if actual <> invalid and actual = expected
        print "PASS: " + name
    else
        got = "<invalid>"
        if actual <> invalid then got = actual.ToStr()
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + got
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
