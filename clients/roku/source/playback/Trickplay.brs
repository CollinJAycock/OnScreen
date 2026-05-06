' Trickplay sprite-sheet helpers for the Roku player.
'
' OnScreen's trickplay shape (matches Plex / Emby / Jellyfin
' conventions): a WebVTT index + N sprite-sheet JPEGs. Each VTT
' cue carries a `sprite_N.jpg#xywh=x,y,w,h` URL fragment naming
' the rectangular region inside that sprite that should be shown
' for the cue's time range.
'
' This file ships only the data side — fetch + parse + lookup —
' so the cues are unit-testable without standing up a Video node.
' The visual side (cropped Poster overlaid on the seekbar during
' scrub) is tracked on roku/todo.md as a follow-up: SceneGraph's
' Poster has no `imageRegion` field, so the right path is either
' a server-side BIF endpoint (Roku firmware renders trickplay
' from BIF natively) or a Group clippingRect + offset-Poster
' combo that needs device-tier validation.

' Parse a WebVTT-format trickplay index into an array of cues.
' Each returned cue is { start_ms, end_ms, sprite_path, x, y, w, h }.
'
' Robust to:
'   - blank lines / spurious whitespace
'   - cues missing the xywh fragment (skipped — the cue carries
'     no useful position so rendering can't proceed)
'   - HH:MM:SS.mmm and MM:SS.mmm time forms
function Trickplay_ParseVtt(text as String) as Object
    cues = []
    if text = invalid or Len(text) = 0 then return cues

    ' Normalize line endings. Roku roUrlTransfer returns the body
    ' as-is, so a server-side \r\n stays \r\n; strip the \r so the
    ' line-by-line walk works either way.
    normalized = ""
    for i = 0 to Len(text) - 1
        ch = Mid(text, i + 1, 1)
        if ch <> Chr(13) then normalized = normalized + ch
    end for

    lines = []
    cur = ""
    for i = 0 to Len(normalized) - 1
        ch = Mid(normalized, i + 1, 1)
        if ch = Chr(10)
            lines.push(cur)
            cur = ""
        else
            cur = cur + ch
        end if
    end for
    if Len(cur) > 0 then lines.push(cur)

    ' Walk lines in pairs of (timing, payload). Skip the WEBVTT
    ' header, blank lines, and any cue identifier (a non-timing
    ' line preceding a timing line). State machine: looking_for_timing
    ' → looking_for_payload → emit cue → repeat.
    state = "timing"
    pendingStart = 0
    pendingEnd = 0
    for each raw in lines
        line = trickplayTrim(raw)
        if line = "" then
            ' Blank line resets to looking-for-timing — both before
            ' and after a cue. Drops state if a cue was half-built.
            state = "timing"
        else if state = "timing"
            arrowAt = Instr(1, line, "-->")
            if arrowAt > 0
                ' Don't bind `left` / `right` as locals — the names
                ' shadow brs's built-in Left() / Right() functions
                ' for the rest of the scope.
                lhs = trickplayTrim(Left(line, arrowAt - 1))
                rhs = trickplayTrim(Mid(line, arrowAt + 3))
                pendingStart = trickplayParseTimestampMs(lhs)
                pendingEnd = trickplayParseTimestampMs(rhs)
                if pendingStart >= 0 and pendingEnd > pendingStart
                    state = "payload"
                else
                    state = "timing"
                end if
            end if
        else if state = "payload"
            cue = trickplayParseCueLine(line, pendingStart, pendingEnd)
            if cue <> invalid then cues.push(cue)
            state = "timing"
        end if
    end for

    return cues
end function

' Find the cue covering posMs. Returns invalid when the cues
' array is empty or no cue brackets posMs (allowed: posMs is
' before the first cue or after the last). Linear scan is fine
' — a 2-hour movie at 10s cadence is ~720 cues; per-tick lookup
' is a few thousand ops, well below the position observer's
' tick budget.
function Trickplay_FindCue(cues as Object, posMs as Integer) as Object
    if cues = invalid or cues.Count() = 0 then return invalid
    for each cue in cues
        if posMs >= cue.start_ms and posMs < cue.end_ms then return cue
    end for
    return invalid
end function

' ── Internal helpers ──────────────────────────────────────────────

' Parse "HH:MM:SS.mmm" or "MM:SS.mmm" into total milliseconds.
' Returns -1 on parse failure so the caller can skip the cue.
function trickplayParseTimestampMs(s as String) as Integer
    if s = "" then return -1

    ' Split on dot to separate the seconds + millis tail.
    dotAt = Instr(1, s, ".")
    head = s
    millis = 0
    if dotAt > 0
        head = Left(s, dotAt - 1)
        tail = Mid(s, dotAt + 1)
        if Len(tail) > 0
            ' Tail might be 1-3 digits — pad to 3 for ms.
            if Len(tail) = 1 then tail = tail + "00"
            if Len(tail) = 2 then tail = tail + "0"
            if Len(tail) > 3 then tail = Left(tail, 3)
            millis = Val(tail)
        end if
    end if

    parts = trickplaySplit(head, ":")
    h = 0
    mn = 0
    sec = 0
    if parts.Count() = 3
        h = Val(parts[0])
        mn = Val(parts[1])
        sec = Val(parts[2])
    else if parts.Count() = 2
        mn = Val(parts[0])
        sec = Val(parts[1])
    else
        return -1
    end if

    return ((h * 3600) + (mn * 60) + sec) * 1000 + millis
end function

' Parse a payload line like:
'   sprite_0.jpg#xywh=0,0,160,90
'   /api/v1/items/abc/trickplay/sprite_0.jpg#xywh=160,0,160,90
' Returns a cue dict or invalid when the xywh fragment is missing
' or malformed.
function trickplayParseCueLine(line as String, startMs as Integer, endMs as Integer) as Object
    hashAt = Instr(1, line, "#xywh=")
    if hashAt <= 0 then return invalid
    spritePath = trickplayTrim(Left(line, hashAt - 1))
    coords = Mid(line, hashAt + Len("#xywh="))
    parts = trickplaySplit(coords, ",")
    if parts.Count() <> 4 then return invalid
    return {
        start_ms: startMs,
        end_ms: endMs,
        sprite_path: spritePath,
        x: Val(parts[0]),
        y: Val(parts[1]),
        w: Val(parts[2]),
        h: Val(parts[3]),
    }
end function

' Lightweight string-split. BrightScript ships no built-in equivalent;
' Strings.brs has StringSplit but only by single char + only on the
' standard channel — keeping this self-contained so the test runner
' can load Trickplay.brs in isolation without dragging Strings.brs in.
function trickplaySplit(s as String, sep as String) as Object
    out = []
    if s = "" then return out
    cur = ""
    sepLen = Len(sep)
    i = 1
    while i <= Len(s)
        if i + sepLen - 1 <= Len(s) and Mid(s, i, sepLen) = sep
            out.push(cur)
            cur = ""
            i = i + sepLen
        else
            cur = cur + Mid(s, i, 1)
            i = i + 1
        end if
    end while
    out.push(cur)
    return out
end function

function trickplayTrim(s as String) as String
    if s = invalid then return ""
    start = 1
    finish = Len(s)
    while start <= finish and (Mid(s, start, 1) = " " or Mid(s, start, 1) = Chr(9))
        start = start + 1
    end while
    while finish >= start and (Mid(s, finish, 1) = " " or Mid(s, finish, 1) = Chr(9))
        finish = finish - 1
    end while
    if finish < start then return ""
    return Mid(s, start, finish - start + 1)
end function
