' Unit tests for source/playback/Subtitles.brs: what a subtitle pick does
' (firmware, burn-in re-issue, nothing, unsupported) and the row label.
' Fixtures are parsed JSON, so absent fields are absent exactly as an
' older server omits them.

sub testSideload()
    file = {
        id: "F1",
        subtitle_streams: [
            { index: 3, language: "eng", codec: "subrip", title: "Full" },
            { index: 4, language: "eng", codec: "hdmv_pgs_subtitle", image_based: true },
            { index: 5, language: "fre", codec: "ass" }
        ],
        external_subtitles: [{ id: "X", language: "spa", url: "/media/subtitles/F1/external/X", forced: true }]
    }
    sess = Subtitles_Sideload(file, "https://s", "T", true)
    runStr("sideload: session tracks", sess.tracks.Count().ToStr(), "3")
    runStr("sideload: embedded text url", sess.urlByRow["0"], "https://s/media/subtitles/F1/3?token=T")
    runBool("sideload: an image track is never side-loaded", sess.urlByRow.DoesExist("1"), false)
    runStr("sideload: second text track", sess.urlByRow["2"], "https://s/media/subtitles/F1/5?token=T")
    runStr("sideload: external after the streams", sess.urlByRow["3"], "https://s/media/subtitles/F1/external/X?token=T")
    direct = Subtitles_Sideload(file, "https://s", "T", false)
    runStr("sideload: direct play loads externals only", direct.tracks.Count().ToStr(), "1")
    runStr("sideload: direct external row", direct.urlByRow["3"], "https://s/media/subtitles/F1/external/X?token=T")
    runStr("sideload: external label", Subtitles_ExternalLabel(file.external_subtitles[0]), "spa · external · forced")
    runStr("sideload: no file", Subtitles_Sideload(invalid, "https://s", "T", true).tracks.Count().ToStr(), "0")
end sub

sub Main()
    testSideload()
    testIsImage()
    testDirectPlay()
    testBurnIn()
    testWhileBurned()
    testStacked()
    testOlderServer()
    testLabels()
    testModeOf()
    testPreferredRow()
    testPreferredRowEdges()

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

' A file from its subtitle_streams JSON (and external_subtitles JSON).
function subFile(streamsJson as String, extJson as String) as Object
    return ParseJson("{""id"":""F1"",""subtitle_streams"":" + streamsJson + ",""external_subtitles"":" + extJson + "}")
end function

sub testModeOf()
    runStr("mode: subtitle_mode", Subtitles_ModeOf(ParseJson("{""subtitle_mode"":""foreign"",""forced_subtitles_only"":true}")), "foreign")
    runStr("mode: always", Subtitles_ModeOf(ParseJson("{""subtitle_mode"":""always"",""forced_subtitles_only"":false}")), "always")
    runStr("mode: older server, forced on", Subtitles_ModeOf(ParseJson("{""forced_subtitles_only"":true}")), "forced")
    runStr("mode: older server, forced off", Subtitles_ModeOf(ParseJson("{""forced_subtitles_only"":false}")), "always")
    runStr("mode: unknown value falls back to the switch", Subtitles_ModeOf(ParseJson("{""subtitle_mode"":""sometimes"",""forced_subtitles_only"":true}")), "forced")
    runStr("mode: no prefs", Subtitles_ModeOf(invalid), "always")
end sub

' The rows of docs/foreign-audio-subtitles.md §2, plus "always" and forced.
sub testPreferredRow()
    both = subFile("[{""index"":2,""codec"":""subrip"",""language"":""eng""},{""index"":3,""codec"":""subrip"",""language"":""eng"",""forced"":true}]", "[]")
    fullOnly = subFile("[{""index"":2,""codec"":""subrip"",""language"":""eng""}]", "[]")
    spanish = subFile("[{""index"":2,""codec"":""subrip"",""language"":""spa""}]", "[]")
    runCase("pick: English audio, full + forced → forced", Subtitles_PreferredRow(both, "en", "foreign", "eng"), 1)
    runCase("pick: English audio, full only → none", Subtitles_PreferredRow(fullOnly, "en", "foreign", "eng"), -1)
    runCase("pick: Japanese audio, full + forced → full", Subtitles_PreferredRow(both, "en", "foreign", "jpn"), 0)
    runCase("pick: Japanese audio, full only → full", Subtitles_PreferredRow(fullOnly, "en", "foreign", "jpn"), 0)
    runCase("pick: Japanese audio, Spanish only → none", Subtitles_PreferredRow(spanish, "en", "foreign", "jpn"), -1)
    runCase("pick: untagged audio is not foreign", Subtitles_PreferredRow(fullOnly, "en", "foreign", "und"), -1)
    runCase("pick: no audio language is not foreign", Subtitles_PreferredRow(fullOnly, "en", "foreign", ""), -1)
    runCase("pick: forced mode, Japanese audio → forced", Subtitles_PreferredRow(both, "en", "forced", "jpn"), 1)
    runCase("pick: forced mode, full only → none", Subtitles_PreferredRow(fullOnly, "en", "forced", "jpn"), -1)
    runCase("pick: always → full before forced", Subtitles_PreferredRow(both, "en", "always", "eng"), 0)
    runCase("pick: always, forced only → forced", Subtitles_PreferredRow(subFile("[{""index"":3,""language"":""eng"",""forced"":true}]", "[]"), "en", "always", "eng"), 0)
    runCase("pick: always, none in the language", Subtitles_PreferredRow(spanish, "en", "always", "eng"), -1)
    runCase("pick: preference Off", Subtitles_PreferredRow(both, "off", "always", "jpn"), -1)
    runCase("pick: en-US matches eng; fre audio is French", Subtitles_PreferredRow(both, "en-US", "foreign", "fre"), 0)
    runCase("pick: French audio with French subtitles isn't foreign", Subtitles_PreferredRow(subFile("[{""index"":2,""language"":""fre""}]", "[]"), "fra", "foreign", "fr"), -1)
end sub

sub testPreferredRowEdges()
    sdh = subFile("[{""index"":2,""language"":""eng"",""sdh"":true},{""index"":3,""language"":""eng""}]", "[]")
    runCase("pick: a plain full track before SDH", Subtitles_PreferredRow(sdh, "en", "always", "eng"), 1)
    runCase("pick: SDH when it's the only full track", Subtitles_PreferredRow(subFile("[{""index"":2,""language"":""eng"",""sdh"":true}]", "[]"), "en", "foreign", "jpn"), 0)
    pgs = subFile("[{""index"":2,""codec"":""hdmv_pgs_subtitle"",""language"":""eng"",""image_based"":true},{""index"":3,""codec"":""subrip"",""language"":""eng""}]", "[]")
    runCase("pick: a picture track never comes on by itself", Subtitles_PreferredRow(pgs, "en", "always", "jpn"), 1)
    runCase("pick: picture only → none", Subtitles_PreferredRow(subFile("[{""index"":2,""language"":""eng"",""image_based"":true}]", "[]"), "en", "always", "jpn"), -1)
    ext = subFile("[{""index"":2,""language"":""jpn""}]", "[{""id"":""X"",""language"":""en"",""url"":""/media/subtitles/F1/external/X""}]")
    runCase("pick: an external file, after the streams", Subtitles_PreferredRow(ext, "en", "foreign", "jpn"), 1)
    runCase("pick: an external file without a url is skipped", Subtitles_PreferredRow(subFile("[]", "[{""id"":""X"",""language"":""en""}]"), "en", "always", "jpn"), -1)
    ' No preferred language: the file's default track (forced modes: only a forced one).
    defFull = subFile("[{""index"":2,""language"":""eng""},{""index"":3,""language"":""fre"",""default"":true}]", "[]")
    defForced = subFile("[{""index"":2,""language"":""eng""},{""index"":3,""language"":""eng"",""forced"":true,""default"":true}]", "[]")
    runCase("pick: no preference, always → the default track", Subtitles_PreferredRow(defFull, "", "always", "eng"), 1)
    runCase("pick: no preference, foreign, default not forced → none", Subtitles_PreferredRow(defFull, "", "foreign", "jpn"), -1)
    runCase("pick: no preference, forced default → it", Subtitles_PreferredRow(defForced, "", "forced", "eng"), 1)
    runCase("pick: no preference, no default → none", Subtitles_PreferredRow(fullOnlyNoDefault(), "", "always", "eng"), -1)
    runCase("pick: no file", Subtitles_PreferredRow(invalid, "en", "always", "eng"), -1)
    runCase("pick: an older server's file (no subtitle fields)", Subtitles_PreferredRow(ParseJson("{""id"":""F1""}"), "en", "always", "eng"), -1)
    runBool("foreign: jpn vs en", Subtitles_IsForeignAudio("jpn", "en"), true)
    runBool("foreign: mul", Subtitles_IsForeignAudio("mul", "en"), false)
    runStr("norm: pt_BR", Subtitles_NormLang("pt_BR"), "pt")
    runStr("norm: invalid", Subtitles_NormLang(invalid), "")
end sub

function fullOnlyNoDefault() as Object
    return subFile("[{""index"":2,""language"":""eng""}]", "[]")
end function

sub runCase(name as String, actual as Integer, expected as Integer)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
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
