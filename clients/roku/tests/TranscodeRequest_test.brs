' Unit tests for source/playback/TranscodeRequest.brs: the audio track's
' relative index, the transcode body (burn-in included) and the 400
' refusals. Fixtures are parsed JSON, the way item detail arrives.

sub Main()
    testAudioIndexIsRelative()
    testBodyPlain()
    testBodyBurn()
    testRefusals()

    print "DONE: TranscodeRequest_test"
end sub

' A file whose audio streams sit at ffprobe indexes 1, 2 and 5 (video at
' 0, subtitles between): the server reads audio_stream_index as
' `-map 0:a:N`, so the third track is 2, never its stream index 5.
sub testAudioIndexIsRelative()
    streams = ParseJson("[{""index"":1,""language"":""eng""},{""index"":2,""language"":""jpn""},{""index"":5,""language"":""fre""}]")
    runInt("audio: first track", TranscodeRequest_AudioIndex(streams, 0), 0)
    runInt("audio: second track", TranscodeRequest_AudioIndex(streams, 1), 1)
    runInt("audio: third track is its position, not stream 5", TranscodeRequest_AudioIndex(streams, 2), 2)
    runInt("audio: past the end", TranscodeRequest_AudioIndex(streams, 3), -1)
    runInt("audio: negative", TranscodeRequest_AudioIndex(streams, -1), -1)
    runInt("audio: no streams", TranscodeRequest_AudioIndex(invalid, 0), -1)
    runInt("audio: index-less rows still count", TranscodeRequest_AudioIndex(ParseJson("[{},{}]"), 1), 1)
end sub

function baseReq() as Object
    return {
        fileId: "f1",
        height: 1080,
        videoCopy: true,
        supportsHevc: false,
        audioStreamIndex: -1,
        burnSubtitleStreamIndex: -1
    }
end function

sub testBodyPlain()
    b = TranscodeRequest_Body(baseReq(), 0)
    runStr("body: file_id", b.file_id, "f1")
    runInt("body: height", b.height, 1080)
    runInt("body: position", b.position_ms, 0)
    runBool("body: remux keeps video_copy", b.video_copy, true)
    runBool("body: supports_hevc", b.supports_hevc, false)
    runBool("body: default audio is null", b.audio_stream_index = invalid, true)
    runBool("body: no burn key", b.DoesExist("burn_subtitle_stream_index"), false)
    runBool("body: serialises audio as null", Instr(1, FormatJson(b), """audio_stream_index"":null") > 0, true)

    req = baseReq()
    req.audioStreamIndex = 2
    b = TranscodeRequest_Body(req, 754000)
    runInt("body: picked audio", b.audio_stream_index, 2)
    runInt("body: re-issue position", b.position_ms, 754000)

    ' A request cached before burn-in existed has no burn field.
    req = baseReq()
    req.Delete("burnSubtitleStreamIndex")
    runBool("body: absent burn field", TranscodeRequest_Body(req, 0).DoesExist("burn_subtitle_stream_index"), false)
end sub

sub testBodyBurn()
    req = baseReq()
    req.burnSubtitleStreamIndex = 4
    b = TranscodeRequest_Body(req, 60000)
    runInt("burn: absolute stream index", b.burn_subtitle_stream_index, 4)
    runBool("burn: forces a full re-encode", b.video_copy, false)
    runBool("burn: serialised", Instr(1, FormatJson(b), """burn_subtitle_stream_index"":4") > 0, true)

    ' An audio switch while burned keeps both.
    req.audioStreamIndex = 1
    b = TranscodeRequest_Body(req, 60000)
    runInt("burn: with audio switch, audio", b.audio_stream_index, 1)
    runInt("burn: with audio switch, burn", b.burn_subtitle_stream_index, 4)

    ' Stream 0 is a legal absolute index.
    req = baseReq()
    req.burnSubtitleStreamIndex = 0
    runInt("burn: index 0", TranscodeRequest_Body(req, 0).burn_subtitle_stream_index, 0)

    ' Un-burning a remux goes back to copying the video.
    req = baseReq()
    req.burnSubtitleStreamIndex = -1
    runBool("burn: off restores video_copy", TranscodeRequest_Body(req, 0).video_copy, true)

    ' A transcode stays a transcode either way.
    req = baseReq()
    req.videoCopy = false
    runBool("burn: transcode without burn", TranscodeRequest_Body(req, 0).video_copy, false)

    runInt("burn index: invalid req", TranscodeRequest_BurnIndex(invalid), -1)
    runInt("burn index: string", TranscodeRequest_BurnIndex({ burnSubtitleStreamIndex: "3" }), -1)
end sub

sub testRefusals()
    raw = "{""error"":{""code"":""INVALID_SUBTITLE_STREAM"",""message"":""burn_subtitle_stream_index 9 is not a subtitle stream""}}"
    runStr("refusal: invalid stream", TranscodeRequest_RefusalText(ApiResult_From(400, raw)), "That subtitle track isn't in this file.")
    raw = "{""error"":{""code"":""SUBTITLE_NOT_IMAGE"",""message"":""x""}}"
    runStr("refusal: not image", TranscodeRequest_RefusalText(ApiResult_From(400, raw)), "That subtitle track can't be burned in.")
    raw = "{""error"":{""code"":""BURN_IN_UNSUPPORTED"",""message"":""x""}}"
    runStr("refusal: stacked", TranscodeRequest_RefusalText(ApiResult_From(400, raw)), "Subtitles can't be burned into a movie in several parts.")
    raw = "{""error"":{""code"":""BAD_REQUEST"",""message"":""audio_stream_index 3 is out of range: this file has 2 audio tracks""}}"
    runStr("refusal: other 400 shows the server's sentence", TranscodeRequest_RefusalText(ApiResult_From(400, raw)), "audio_stream_index 3 is out of range: this file has 2 audio tracks")
    runStr("refusal: bare 400", TranscodeRequest_RefusalText(ApiResult_From(400, "")), "The server couldn't switch to that track.")
    raw = "{""error"":{""code"":""BURN_IN_UNAVAILABLE"",""message"":""x""}}"
    runStr("refusal: no worker can burn", TranscodeRequest_RefusalText(ApiResult_From(503, raw)), "This server can't burn in subtitles right now.")
    raw = "{""error"":{""code"":""SERVICE_UNAVAILABLE"",""message"":""x""}}"
    runStr("refusal: other 503", TranscodeRequest_RefusalText(ApiResult_From(503, raw)), "")
    raw = "{""error"":{""code"":""SUBTITLE_NOT_IMAGE"",""message"":""x""}}"
    runStr("refusal: not a 400", TranscodeRequest_RefusalText(ApiResult_From(500, raw)), "")
    runStr("refusal: success", TranscodeRequest_RefusalText(ApiResult_From(200, "{""data"":{}}")), "")
    runStr("refusal: stream limit is not a refusal", TranscodeRequest_RefusalText(ApiResult_From(429, "{""error"":{""code"":""TOO_MANY_SESSIONS"",""message"":""x""}}")), "")
    runStr("refusal: invalid", TranscodeRequest_RefusalText(invalid), "")
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
