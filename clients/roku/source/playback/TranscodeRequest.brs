' The POST /api/v1/items/{id}/transcode request body, and the two inputs
' the player has to translate before it goes out: the audio track and the
' subtitle to burn in. Pure — tests/TranscodeRequest_test.brs. Depends on
' nothing (the refusal text reads an ApiResult_From result).
'
' audio_stream_index counts AUDIO streams: the server maps `-map 0:a:N`
' (internal/api/v1/transcode.go, internal/transcode/ffmpeg.go), so N is
' the track's position among the file's audio streams, not its ffprobe
' stream index. audio_streams[] is in ffprobe order, the order 0:a:N
' counts in, so the position in that array is the number to send.
'
' burn_subtitle_stream_index is the other way round: the subtitle stream's
' ABSOLUTE index (subtitle_streams[].index). Burning paints the subtitle
' into the picture, so it is always a full re-encode — video_copy false,
' whatever the play decision said.

' The audio_stream_index for the track at position pos in audioStreams,
' or -1 (server default) when pos addresses no track.
function TranscodeRequest_AudioIndex(audioStreams as Dynamic, pos as Integer) as Integer
    if audioStreams = invalid or type(audioStreams) <> "roArray" then return -1
    if pos < 0 or pos >= audioStreams.Count() then return -1
    if audioStreams[pos] = invalid then return -1
    return pos
end function

' The request body. req is the cached start request:
'   { fileId, height, videoCopy, supportsHevc,
'     audioStreamIndex,          ' relative; -1 = server default
'     burnSubtitleStreamIndex }  ' absolute; -1 (or absent) = none
' The burn key is left out entirely when nothing is burned, so a server
' that predates it sees exactly the body it always did.
function TranscodeRequest_Body(req as Object, positionMs as Integer) as Object
    audioIdx = invalid
    if req.audioStreamIndex <> invalid and req.audioStreamIndex >= 0 then audioIdx = req.audioStreamIndex
    body = {
        file_id: req.fileId,
        height: req.height,
        position_ms: positionMs,
        video_copy: req.videoCopy = true,
        audio_stream_index: audioIdx,
        supports_hevc: req.supportsHevc = true
    }
    burn = TranscodeRequest_BurnIndex(req)
    if burn >= 0
        body.burn_subtitle_stream_index = burn
        body.video_copy = false
    end if
    return body
end function

function TranscodeRequest_BurnIndex(req as Dynamic) as Integer
    if req = invalid or type(req) <> "roAssociativeArray" then return -1
    v = req.burnSubtitleStreamIndex
    if v = invalid then return -1
    if type(v) <> "Integer" and type(v) <> "roInt" and type(v) <> "LongInteger" then return -1
    if v < 0 then return -1
    return v
end function

' The short sentence for a start the server refused with 400 — a bad audio
' track, or a subtitle it can't burn in (INVALID_SUBTITLE_STREAM,
' SUBTITLE_NOT_IMAGE, BURN_IN_UNSUPPORTED on a multi-part movie). "" for
' anything that isn't a 400 or a 503 BURN_IN_UNAVAILABLE. A refused track switch keeps the current
' stream playing; the player shows this in a dialog.
function TranscodeRequest_RefusalText(result as Dynamic) as String
    if result = invalid or type(result) <> "roAssociativeArray" then return ""
    code = ""
    if result.errorCode <> invalid then code = result.errorCode
    ' 503 when no transcode worker can burn subtitles in (an older remote
    ' worker); checked before the running session is replaced, like a 400.
    if result.code = 503 and code = "BURN_IN_UNAVAILABLE" then return "This server can't burn in subtitles right now."
    if result.code = invalid or result.code <> 400 then return ""
    if code = "INVALID_SUBTITLE_STREAM" then return "That subtitle track isn't in this file."
    if code = "SUBTITLE_NOT_IMAGE" then return "That subtitle track can't be burned in."
    if code = "BURN_IN_UNSUPPORTED" then return TranscodeRequest_BurnUnsupportedText()
    msg = ""
    if result.errorMessage <> invalid and (type(result.errorMessage) = "String" or type(result.errorMessage) = "roString") then msg = result.errorMessage.Trim()
    if msg <> "" then return msg
    return "The server couldn't switch to that track."
end function

' Also shown without asking when the player already knows the movie is in
' several parts (Subtitles_PickPlan "unsupported").
function TranscodeRequest_BurnUnsupportedText() as String
    return "Subtitles can't be burned into a movie in several parts."
end function

function TranscodeRequest_RefusalTitle() as String
    return "Can't switch tracks"
end function
