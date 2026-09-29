' Unit tests for source/playback/PlaybackStop.brs, fed real response
' bodies through ApiResult_From the way the player's tasks see them.

sub Main()
    testText()
    testFromStoppedResponse()
    testFromOtherResponses()

    print "DONE: PlaybackStop_test"
end sub

sub testText()
    runStr("text: with message", PlaybackStop_Text("bedtime"), "Playback was stopped by the server admin: bedtime")
    runStr("text: trims the message", PlaybackStop_Text("  bedtime  "), "Playback was stopped by the server admin: bedtime")
    runStr("text: no message", PlaybackStop_Text(""), "Playback was stopped by the server admin.")
    runStr("text: blank message", PlaybackStop_Text("   "), "Playback was stopped by the server admin.")
    runStr("text: invalid message", PlaybackStop_Text(invalid), "Playback was stopped by the server admin.")
end sub

sub testFromStoppedResponse()
    ' The server already phrases the sentence (playbackStoppedText).
    raw = "{""error"":{""code"":""PLAYBACK_STOPPED"",""message"":""Playback was stopped by the server admin: family movie night"",""request_id"":""r""}}"
    runStr("stopped: server sentence", PlaybackStop_FromResult(ApiResult_From(403, raw)), "Playback was stopped by the server admin: family movie night")

    raw = "{""error"":{""code"":""PLAYBACK_STOPPED"",""message"":""""}}"
    runStr("stopped: empty message falls back", PlaybackStop_FromResult(ApiResult_From(403, raw)), "Playback was stopped by the server admin.")
end sub

sub testFromOtherResponses()
    ' The parental watch-time gate is also a 403 on the progress beacon —
    ' not an admin stop.
    raw = "{""error"":{""code"":""PARENTAL_LIMIT"",""message"":""daily limit reached""}}"
    runStr("other: parental limit", PlaybackStop_FromResult(ApiResult_From(403, raw)), "")

    raw = "{""error"":{""code"":""PLAYBACK_STOPPED"",""message"":""x""}}"
    runStr("other: code on a non-403", PlaybackStop_FromResult(ApiResult_From(500, raw)), "")

    runStr("other: success", PlaybackStop_FromResult(ApiResult_From(204, "")), "")
    ' Media bytes (the direct-play probe) answer 206 with one byte.
    runStr("other: probe got bytes", PlaybackStop_FromResult(ApiResult_From(206, "x")), "")
    runStr("other: network failure", PlaybackStop_FromResult(ApiResult_From(-6, "")), "")
    runStr("other: invalid", PlaybackStop_FromResult(invalid), "")
end sub

sub runStr(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
