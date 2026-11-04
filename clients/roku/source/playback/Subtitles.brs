' What picking a subtitle row does. Pure — tests/Subtitles_test.brs.
'
' A remux / transcode session's HLS carries no subtitles, so a pick there
' never reaches the firmware's availableSubtitleTracks. Image-based tracks
' (PGS, VobSub, DVB — subtitle_streams[].image_based) are the ones the
' server can paint into the picture instead: the player re-issues the
' session at the playhead with burn_subtitle_stream_index (a full
' re-encode; TranscodeRequest_Body). The owner's rule is "OCR text if
' ready, else burn in", but this channel never side-loads external
' subtitle files (no subtitleTracks / external_subtitles anywhere), so a
' stream's text_version_id is treated as absent and an image pick in a
' session always burns.
'
' A direct play keeps today's behaviour: the firmware demuxes the
' container's own tracks, image ones included. A server that predates
' image_based never marks a track, so nothing burns there either.

function Subtitles_IsImage(stream as Dynamic) as Boolean
    if stream = invalid or type(stream) <> "roAssociativeArray" then return false
    return stream.image_based = true
end function

' True when picking stream in this context burns it in: an image track
' with a known absolute index, while playing through a server session.
function Subtitles_WillBurn(stream as Dynamic, serverSession as Boolean) as Boolean
    if not serverSession then return false
    if not Subtitles_IsImage(stream) then return false
    return Subtitles_StreamIndex(stream) >= 0
end function

' The stream's absolute index (subtitle_streams[].index), -1 when absent.
function Subtitles_StreamIndex(stream as Dynamic) as Integer
    if stream = invalid or type(stream) <> "roAssociativeArray" then return -1
    v = stream.index
    if v = invalid then return -1
    if type(v) <> "Integer" and type(v) <> "roInt" and type(v) <> "LongInteger" then return -1
    if v < 0 then return -1
    return v
end function

' The plan for a pick. rowPos is the row's position in streams (-1 = Off);
' ctx is { serverSession, stacked, burnIndex } — burnIndex the absolute
' index burned into the current session, -1 for none. Returns
'   { kind: "firmware" }               hand the pick to the Video node
'   { kind: "reissue", burnIndex: n }  re-issue the session burning n
'                                      (-1 = stop burning; a text pick
'                                      then goes to the Video node too)
'   { kind: "none" }                   already showing; nothing to do
'   { kind: "unsupported" }            an image track on a multi-part
'                                      movie: the server can't burn it
function Subtitles_PickPlan(streams as Dynamic, rowPos as Integer, ctx as Object) as Object
    burned = -1
    if ctx.burnIndex <> invalid and ctx.burnIndex >= 0 then burned = ctx.burnIndex
    if rowPos < 0
        if burned >= 0 then return { kind: "reissue", burnIndex: -1 }
        return { kind: "firmware" }
    end if
    stream = invalid
    if streams <> invalid and type(streams) = "roArray" and rowPos < streams.Count() then stream = streams[rowPos]
    if stream = invalid then return { kind: "none" }
    if Subtitles_WillBurn(stream, ctx.serverSession = true)
        idx = Subtitles_StreamIndex(stream)
        if idx = burned then return { kind: "none" }
        if ctx.stacked = true then return { kind: "unsupported" }
        return { kind: "reissue", burnIndex: idx }
    end if
    if burned >= 0 then return { kind: "reissue", burnIndex: -1 }
    return { kind: "firmware" }
end function

' The language part of a picker row: "und" when the stream has none, and
' "<lang> (burned in)" on a track a pick would burn in.
function Subtitles_LangLabel(stream as Dynamic, willBurn as Boolean) as String
    lang = "und"
    if stream <> invalid and type(stream) = "roAssociativeArray"
        l = stream.language
        if l <> invalid and (type(l) = "String" or type(l) = "roString") and l <> "" then lang = l
    end if
    if willBurn then return lang + " (burned in)"
    return lang
end function

' ── Side-loaded subtitles ──────────────────────────────────────────
'
' The Video node fetches WebVTT itself from content.SubtitleTracks
' ([{ TrackName: url, Language, Description }]); the server serves an
' embedded text track as /media/subtitles/{fileId}/{index} and an external
' file at its own url, both authenticated by ?token= (the asset token).
' A session's HLS carries no subtitles, so there every text track is
' side-loaded; a direct play's own tracks come from the container, so only
' the external files are. Picker rows: the file's subtitle_streams, then
' its external_subtitles.

' { tracks, urlByRow } for file. urlByRow maps a picker row ("3") to the
' side-loaded URL that row selects.
function Subtitles_Sideload(file as Dynamic, serverUrl as String, token as String, serverSession as Boolean) as Object
    out = { tracks: [], urlByRow: {} }
    if file = invalid or type(file) <> "roAssociativeArray" or file.id = invalid then return out
    streams = file.subtitle_streams
    n = 0
    if streams <> invalid and type(streams) = "roArray" then n = streams.Count()
    if serverSession
        for i = 0 to n - 1
            s = streams[i]
            idx = Subtitles_StreamIndex(s)
            if idx >= 0 and not Subtitles_IsImage(s)
                url = serverUrl + "/media/subtitles/" + file.id + "/" + idx.ToStr() + "?token=" + token
                out.tracks.push({ TrackName: url, Language: Subtitles_LangLabel(s, false), Description: Subtitles_Str(s.title) })
                out.urlByRow[i.ToStr()] = url
            end if
        end for
    end if
    ext = file.external_subtitles
    if ext <> invalid and type(ext) = "roArray"
        for j = 0 to ext.Count() - 1
            e = ext[j]
            path = Subtitles_Str(e.url)
            if path <> ""
                sep = "?"
                if Instr(1, path, "?") > 0 then sep = "&"
                url = serverUrl + path + sep + "token=" + token
                out.tracks.push({ TrackName: url, Language: Subtitles_LangLabel(e, false), Description: Subtitles_Str(e.title) })
                out.urlByRow[(n + j).ToStr()] = url
            end if
        end for
    end if
    return out
end function

' A picker row for an external file: "eng · external · forced · Title".
function Subtitles_ExternalLabel(e as Dynamic) as String
    parts = [Subtitles_LangLabel(e, false), "external"]
    if e <> invalid and e.forced = true then parts.push("forced")
    if e <> invalid and e.sdh = true then parts.push("sdh")
    t = Subtitles_Str(e.title)
    if t <> "" then parts.push(t)
    return parts.Join(" · ")
end function

function Subtitles_Str(v as Dynamic) as String
    if v = invalid then return ""
    if type(v) <> "String" and type(v) <> "roString" then return ""
    return v
end function
