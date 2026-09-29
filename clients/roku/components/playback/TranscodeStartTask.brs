sub init()
    m.top.functionName = "runTranscodeStart"
end sub

sub runTranscodeStart()
    audioIdx = invalid
    if m.top.audioStreamIndex >= 0 then audioIdx = m.top.audioStreamIndex
    body = {
        file_id: m.top.fileId,
        height: m.top.height,
        position_ms: m.top.positionMs,
        video_copy: m.top.videoCopy,
        audio_stream_index: audioIdx,
        supports_hevc: m.top.supportsHevc
    }
    ' RequestSync rather than PostSync: a refused start carries its reason
    ' in the error envelope, and an admin stop (403 PLAYBACK_STOPPED) has
    ' to reach the player as its message rather than a silent bail.
    res = Client_RequestSync("POST", ApiItemTranscode(m.top.itemId), body, invalid, true)
    r = ApiResult_From(res.code, res.raw)
    m.top.stopMessage = PlaybackStop_FromResult(r)
    if r.ok and r.data <> invalid and type(r.data) = "roAssociativeArray"
        m.top.result = r.data
    else
        m.top.result = {}
    end if
    m.top.control = "DONE"
end sub
