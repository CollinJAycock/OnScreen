sub init()
    m.top.functionName = "runTranscodeStart"
end sub

sub runTranscodeStart()
    ' audio_stream_index is relative (the track's position among the audio
    ' streams); burn_subtitle_stream_index is absolute and forces a full
    ' re-encode. See source/playback/TranscodeRequest.brs.
    body = TranscodeRequest_Body({
        fileId: m.top.fileId,
        height: m.top.height,
        videoCopy: m.top.videoCopy,
        supportsHevc: m.top.supportsHevc,
        audioStreamIndex: m.top.audioStreamIndex,
        burnSubtitleStreamIndex: m.top.burnSubtitleStreamIndex
    }, m.top.positionMs)
    ' RequestSync rather than PostSync: a refused start carries its reason
    ' in the error envelope, and an admin stop (403 PLAYBACK_STOPPED) or
    ' the account's stream limit (429 TOO_MANY_SESSIONS) has to reach the
    ' player as its message rather than a silent bail.
    res = Client_RequestSync("POST", ApiItemTranscode(m.top.itemId), body, invalid, true)
    r = ApiResult_From(res.code, res.raw)
    m.top.stopMessage = PlaybackStop_FromResult(r)
    m.top.limitMessage = PlaybackLimit_FromResult(r)
    m.top.refusal = TranscodeRequest_RefusalText(r)
    if r.ok and r.data <> invalid and type(r.data) = "roAssociativeArray"
        m.top.result = r.data
    else
        m.top.result = {}
    end if
    m.top.control = "DONE"
end sub
