' Admin "stop this stream" — the viewer side, for a client without an event
' stream.
'
' POST /api/v1/sessions/{id}/stop (admin, Now Playing) publishes a
' playback.stop SSE event and, for direct play / direct stream / remux,
' refuses that (user, item, client IP) for ~2 minutes: the 'playing'
' progress beacon, media bytes and transcode start answer
'   403 {"error":{"code":"PLAYBACK_STOPPED","message":<sentence>}}
' (internal/api/v1/playback_stop.go). The Roku channel has no SSE
' subscription, so the 403 is how it learns: the next heartbeat (every
' 10 s while playing), a refused transcode start, or a probe of the stream
' URL after the Video node errors. Mirrors web/src/lib/playback-stop.ts and
' the Android app's PlaybackStop. Pure — tests/PlaybackStop_test.brs.

function PlaybackStop_ErrorCode() as String
    return "PLAYBACK_STOPPED"
end function

' The sentence the player shows — the same wording the server's 403 uses.
' message is the admin's optional note.
function PlaybackStop_Text(message as Dynamic) as String
    m_ = ""
    if message <> invalid and (type(message) = "String" or type(message) = "roString") then m_ = message.Trim()
    if m_ = "" then return "Playback was stopped by the server admin."
    return "Playback was stopped by the server admin: " + m_
end function

' The sentence to show when an ApiResult_From result is the server refusing
' a stopped stream, else "". The server already phrases the sentence; fall
' back to the generic one when its message is empty.
function PlaybackStop_FromResult(result as Dynamic) as String
    if result = invalid or type(result) <> "roAssociativeArray" then return ""
    if result.code = invalid or result.code <> 403 then return ""
    if result.errorCode = invalid or result.errorCode <> PlaybackStop_ErrorCode() then return ""
    msg = ""
    if result.errorMessage <> invalid and (type(result.errorMessage) = "String" or type(result.errorMessage) = "roString") then msg = result.errorMessage.Trim()
    if msg = "" then return PlaybackStop_Text("")
    return msg
end function
