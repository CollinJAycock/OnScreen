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
    parsed = Client_PostSync(ApiItemTranscode(m.top.itemId), body, true)
    if parsed = invalid
        m.top.result = {}
    else
        m.top.result = parsed
    end if
    m.top.control = "DONE"
end sub
