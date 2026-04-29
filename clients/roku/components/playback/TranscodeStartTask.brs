sub init()
    m.top.functionName = "runTranscodeStart"
end sub

sub runTranscodeStart()
    body = {
        file_id: m.top.fileId,
        height: m.top.height,
        position_ms: 0,
        video_copy: m.top.videoCopy,
        audio_stream_index: invalid,
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
