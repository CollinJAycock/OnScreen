' Unit tests for source/api/ApiResult.brs — the { code, ok, data,
' errorCode, errorMessage } shape every ApiCallTask result takes.

sub Main()
    testSuccessEnvelope()
    testNoContent()
    testErrorEnvelope()
    testNetworkFailure()
    testNonJsonError()

    print "DONE: ApiResult_test"
end sub

sub testSuccessEnvelope()
    r = ApiResult_From(200, "{""data"":{""mode"":""next"",""episode"":{""id"":""e5""}}}")
    runBool("success: ok", r.ok, true)
    runStr("success: data unwrapped", r.data.mode, "next")
    runStr("success: nested data", r.data.episode.id, "e5")
    runStr("success: no error code", r.errorCode, "")

    r = ApiResult_From(201, "{""data"":{""id"":""issue-1"",""kind"":""audio""}}")
    runBool("created: 201 is ok", r.ok, true)
    runStr("created: data", r.data.kind, "audio")

    r = ApiResult_From(200, "{""data"":[{""id"":""a""}]}")
    runStr("success: list data", r.data[0].id, "a")
end sub

sub testNoContent()
    r = ApiResult_From(204, "")
    runBool("204: ok", r.ok, true)
    runBool("204: no data", r.data = invalid, true)
end sub

sub testErrorEnvelope()
    raw = "{""error"":{""code"":""PLAYBACK_STOPPED"",""message"":""Playback was stopped by the server admin: bedtime"",""request_id"":""r1""}}"
    r = ApiResult_From(403, raw)
    runBool("error: not ok", r.ok, false)
    runStr("error: code", r.errorCode, "PLAYBACK_STOPPED")
    runStr("error: message", r.errorMessage, "Playback was stopped by the server admin: bedtime")
    runBool("error: no data", r.data = invalid, true)

    ' The rate limiter writes the same envelope without a request id.
    r = ApiResult_From(429, "{""error"":{""code"":""RATE_LIMITED"",""message"":""rate limit exceeded""}}")
    runStr("error: rate limited code", r.errorCode, "RATE_LIMITED")
    runStr("error: status kept", r.code.ToStr(), "429")
end sub

sub testNetworkFailure()
    ' roUrlTransfer reports curl failures as negative codes; 0 = never
    ' completed (timeout / no server).
    r = ApiResult_From(-28, "")
    runBool("network: not ok", r.ok, false)
    runStr("network: no error code", r.errorCode, "")
    r = ApiResult_From(0, invalid)
    runBool("never sent: not ok", r.ok, false)
end sub

sub testNonJsonError()
    r = ApiResult_From(502, "<html>Bad Gateway</html>")
    runBool("non-json: not ok", r.ok, false)
    runStr("non-json: no code", r.errorCode, "")
    r = ApiResult_From(500, "{""error"":""flat string""}")
    runStr("flat error: no code", r.errorCode, "")
    r = ApiResult_From(500, "{""error"":{""code"":5,""message"":true}}")
    runStr("typed wrong: code ignored", r.errorCode, "")
    runStr("typed wrong: message ignored", r.errorMessage, "")
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

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
