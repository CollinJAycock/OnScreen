' Unit tests for the server play decision mapping in
' source/playback/Decide.brs (Playback_PlanFromServer / Playback_FromVerdict),
' fed real response bodies through ApiResult_From the way the player's
' ApiCallTask sees them.

sub Main()
    testTooManyStreams()
    testOptimizedVersion()
    testDemotion()
    testServerVerdictIsFinal()
    testUnplayable()
    testStackAfterVerdict()
    testFallsBackToLocal()
    testFromVerdict()
    testHeightHint()

    print "DONE: PlaybackPlan_test"
end sub

function localDirect() as Object
    return { mode: "direct", height: 2160, videoCopy: false, supportsHevc: true }
end function

function localRemux() as Object
    return { mode: "remux", height: 1080, videoCopy: true, supportsHevc: false }
end function

function localTranscode() as Object
    return { mode: "transcode", height: 2160, videoCopy: false, supportsHevc: true }
end function

function decisionResult(code as Integer, raw as String) as Object
    return ApiResult_From(code, raw)
end function

sub testTooManyStreams()
    raw = "{""error"":{""code"":""TOO_MANY_SESSIONS"",""message"":""Your account can play 2 streams at a time, and 2 are already playing on other devices. Stop one to watch here.""}}"
    plan = Playback_PlanFromServer(decisionResult(429, raw), localDirect())
    runStr("limit: kind", plan.kind, "limit")
    runStr("limit: server sentence", plan.message, "Your account can play 2 streams at a time, and 2 are already playing on other devices. Stop one to watch here.")
    ' Not a local fallback: the plan carries no decision to play.
    runBool("limit: nothing to play", plan.decision = invalid, true)

    raw = "{""error"":{""code"":""RATE_LIMITED"",""message"":""slow down""}}"
    runStr("limit: other 429 plays locally", Playback_PlanFromServer(decisionResult(429, raw), localDirect()).kind, "play")
end sub

sub testOptimizedVersion()
    raw = "{""data"":{""decision"":""directPlay"",""file_id"":""f1"",""reason"":""optimized_version"",""optimized_version"":{""id"":""v1"",""file_id"":""f1"",""profile"":""tv_1080"",""label"":""TV 1080p"",""container"":""mp4"",""video_codec"":""h264"",""audio_codec"":""aac"",""audio_channels"":2,""stream_url"":""/media/optimized/v1"",""stream_token"":""vtok""},""source_decision"":""transcode"",""source_reason"":""quality_profile""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localDirect())
    runStr("optimized: kind", plan.kind, "optimized")
    runStr("optimized: url", plan.url, "/media/optimized/v1")
    runStr("optimized: token", plan.token, "vtok")
    runStr("optimized: fallback is the source verdict", plan.fallback.mode, "transcode")
    runInt("optimized: fallback keeps the height hint", plan.fallback.height, 2160)

    ' No stream_token (older build) → "" so the player uses the asset token.
    raw = "{""data"":{""decision"":""directPlay"",""reason"":""optimized_version"",""optimized_version"":{""stream_url"":""/media/optimized/v2""},""source_decision"":""directStream""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localDirect())
    runStr("optimized: missing token", plan.token, "")
    runStr("optimized: directStream source → remux", plan.fallback.mode, "remux")
    runBool("optimized: remux fallback copies video", plan.fallback.videoCopy, true)

    ' Unknown source verdict → the local decision.
    raw = "{""data"":{""decision"":""directPlay"",""reason"":""optimized_version"",""optimized_version"":{""stream_url"":""/media/optimized/v3""}}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localRemux())
    runStr("optimized: no source verdict falls back locally", plan.fallback.mode, "remux")

    ' The reason without a usable version is not an optimized play.
    raw = "{""data"":{""decision"":""directPlay"",""reason"":""optimized_version"",""optimized_version"":{""stream_url"":""""}}}"
    runStr("optimized: empty stream_url plays locally", Playback_PlanFromServer(decisionResult(200, raw), localDirect()).kind, "play")
end sub

sub testDemotion()
    ' The viewer's quality profile turns a direct play into a transcode.
    raw = "{""data"":{""decision"":""transcode"",""file_id"":""f1"",""reason"":""quality_profile"",""max_bitrate_kbps"":8000}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localDirect())
    runStr("demote: kind", plan.kind, "play")
    runStr("demote: direct → transcode", plan.decision.mode, "transcode")
    runBool("demote: no video copy", plan.decision.videoCopy, false)
    runInt("demote: height hint kept", plan.decision.height, 2160)

    ' A resolution ceiling has no reason, still a transcode.
    raw = "{""data"":{""decision"":""transcode"",""file_id"":""f1""}}"
    runStr("demote: remux → transcode", Playback_PlanFromServer(decisionResult(200, raw), localRemux()).decision.mode, "transcode")
end sub

sub testServerVerdictIsFinal()
    ' Upgrade: a local remux the server direct-plays becomes direct.
    raw = "{""data"":{""decision"":""directPlay"",""file_id"":""f1""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localRemux())
    runStr("final: kind", plan.kind, "play")
    runStr("final: remux → direct", plan.decision.mode, "direct")
    runBool("final: direct copies nothing", plan.decision.videoCopy, false)
    runInt("final: height hint kept", plan.decision.height, 1080)
    runBool("final: hevc flag kept", plan.decision.supportsHevc, false)

    ' The other way: a local direct the server remuxes becomes remux.
    raw = "{""data"":{""decision"":""directStream"",""file_id"":""f1""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localDirect())
    runStr("final: direct → remux", plan.decision.mode, "remux")
    runBool("final: remux copies video", plan.decision.videoCopy, true)
    runInt("final: remux keeps height hint", plan.decision.height, 2160)
    runBool("final: remux keeps hevc flag", plan.decision.supportsHevc, true)

    ' A local transcode the server can stream-copy or play as-is.
    runStr("final: transcode → remux", Playback_PlanFromServer(decisionResult(200, raw), localTranscode()).decision.mode, "remux")
    raw = "{""data"":{""decision"":""directPlay"",""file_id"":""f1""}}"
    runStr("final: transcode → direct", Playback_PlanFromServer(decisionResult(200, raw), localTranscode()).decision.mode, "direct")

    ' Agreement stands.
    runStr("final: direct stays direct", Playback_PlanFromServer(decisionResult(200, raw), localDirect()).decision.mode, "direct")
    raw = "{""data"":{""decision"":""transcode"",""file_id"":""f1""}}"
    runStr("final: transcode stays transcode", Playback_PlanFromServer(decisionResult(200, raw), localTranscode()).decision.mode, "transcode")
end sub

sub testUnplayable()
    raw = "{""data"":{""decision"":""unsupported"",""file_id"":""f1""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localDirect())
    runStr("unsupported: kind", plan.kind, "unplayable")
    runStr("unsupported: message", plan.message, "Dolby Vision is not supported.")
    runBool("unsupported: nothing to play", plan.decision = invalid, true)

    raw = "{""data"":{""decision"":""damaged"",""file_id"":""f1""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localRemux())
    runStr("damaged: kind", plan.kind, "unplayable")
    runStr("damaged: message", plan.message, "This file is damaged and can't be played.")
    runBool("damaged: nothing to play", plan.decision = invalid, true)

    ' The local Dolby Vision gate says the same thing.
    runStr("unsupported: shared message", Playback_UnsupportedMessage(), "Dolby Vision is not supported.")
end sub

sub testStackAfterVerdict()
    ' PlayerScene runs Playback_ForStack on whatever it plays, so a stack the
    ' server (or a failed call's local decision) leaves direct still remuxes.
    raw = "{""data"":{""decision"":""directPlay"",""file_id"":""f1""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localRemux())
    runStr("stack: server direct → remux", Playback_ForStack(plan.decision).mode, "remux")
    runBool("stack: copies video", Playback_ForStack(plan.decision).videoCopy, true)
    raw = "{""data"":{""decision"":""transcode"",""file_id"":""f1""}}"
    plan = Playback_PlanFromServer(decisionResult(200, raw), localDirect())
    runStr("stack: server transcode stands", Playback_ForStack(plan.decision).mode, "transcode")
    plan = Playback_PlanFromServer(decisionResult(-28, ""), localDirect())
    runStr("stack: failed call, local direct → remux", Playback_ForStack(plan.decision).mode, "remux")
end sub

sub testFallsBackToLocal()
    runStr("fallback: older server 404", Playback_PlanFromServer(decisionResult(404, "{""error"":{""code"":""NOT_FOUND"",""message"":""not found""}}"), localDirect()).decision.mode, "direct")
    runStr("fallback: network failure", Playback_PlanFromServer(decisionResult(-28, ""), localRemux()).decision.mode, "remux")
    runStr("fallback: invalid result", Playback_PlanFromServer(invalid, localDirect()).decision.mode, "direct")
    runStr("fallback: data not an object", Playback_PlanFromServer(decisionResult(200, "{""data"":[]}"), localDirect()).decision.mode, "direct")
    runStr("fallback: server error", Playback_PlanFromServer(decisionResult(500, "{""error"":{""code"":""INTERNAL"",""message"":""boom""}}"), localRemux()).decision.mode, "remux")
    runStr("fallback: 415 plays locally", Playback_PlanFromServer(decisionResult(415, "{""error"":{""code"":""UNSUPPORTED_CONTAINER"",""message"":""not playable""}}"), localDirect()).kind, "play")
    raw = "{""data"":{""decision"":""teleport"",""file_id"":""f1""}}"
    runStr("fallback: unknown verdict", Playback_PlanFromServer(decisionResult(200, raw), localDirect()).decision.mode, "direct")
    raw = "{""data"":{""file_id"":""f1""}}"
    runStr("fallback: missing verdict", Playback_PlanFromServer(decisionResult(200, raw), localRemux()).decision.mode, "remux")
end sub

sub testFromVerdict()
    runStr("verdict: directPlay", Playback_FromVerdict("directPlay", localRemux()).mode, "direct")
    runStr("verdict: directStream", Playback_FromVerdict("directStream", localDirect()).mode, "remux")
    runStr("verdict: transcode", Playback_FromVerdict("transcode", localDirect()).mode, "transcode")
    runBool("verdict: hevc flag carried", Playback_FromVerdict("transcode", localDirect()).supportsHevc, true)
    runBool("verdict: unsupported", Playback_FromVerdict("unsupported", localDirect()) = invalid, true)
    runBool("verdict: damaged", Playback_FromVerdict("damaged", localDirect()) = invalid, true)
    runBool("verdict: missing", Playback_FromVerdict(invalid, localDirect()) = invalid, true)
end sub

sub testHeightHint()
    ' Every video verdict carries the source's height hint now, so a demoted
    ' direct play transcodes at the source's size.
    runInt("height: 4K direct", Playback_Decide({ container: "mp4", video_codec: "h264", audio_codec: "aac", resolution_h: 2160 }, false).height, 2160)
    runInt("height: 720p remux", Playback_Decide({ container: "matroska", video_codec: "h264", audio_codec: "aac", resolution_h: 720 }, false).height, 1080)
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
