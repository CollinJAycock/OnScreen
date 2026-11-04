' Chapters for the player's picker: a file's `chapters` from item detail,
' [{ title, start_ms, end_ms }] in order. Pure (tests/Chapters_test.brs).

' The picker rows: "3. The Heist  ·  1:02:15", a missing title written
' "Chapter 3".
function Chapters_Rows(chapters as Dynamic) as Object
    rows = []
    if chapters = invalid or type(chapters) <> "roArray" then return rows
    n = 0
    for each c in chapters
        n = n + 1
        title = ""
        if c <> invalid and (type(c.title) = "String" or type(c.title) = "roString") then title = c.title.Trim()
        if title = "" or Chapters_IsTimestampTitle(title) then title = "Chapter " + n.ToStr()
        startMs = 0
        if c <> invalid and c.start_ms <> invalid then startMs = c.start_ms
        rows.push(n.ToStr() + ". " + title + "  ·  " + Chapters_FormatTime(startMs))
    end for
    return rows
end function

' A muxer's placeholder name, a timestamp ("(01)00:07:13:307",
' "00:07:13.307", "Chapter 01"), says nothing a row's own time doesn't.
function Chapters_IsTimestampTitle(title as String) as Boolean
    re = CreateObject("roRegex", "^(\(\d+\))?\s*\d{1,2}:\d{2}:\d{2}([:.]\d{1,3})?$|^chapter\s*\d+$", "i")
    return re.IsMatch(title)
end function

' The chapter playing at ms: the last one starting at or before it.
function Chapters_IndexAt(chapters as Dynamic, ms as Dynamic) as Integer
    if chapters = invalid or type(chapters) <> "roArray" or chapters.Count() = 0 then return -1
    if ms = invalid then ms = 0
    idx = 0
    for i = 0 to chapters.Count() - 1
        c = chapters[i]
        if c <> invalid and c.start_ms <> invalid and c.start_ms <= ms then idx = i
    end for
    return idx
end function

' 0:45, 12:03, 1:02:15.
function Chapters_FormatTime(ms as Dynamic) as String
    if ms = invalid or ms < 0 then ms = 0
    total = Int(ms / 1000)
    h = Int(total / 3600)
    mm = Int((total mod 3600) / 60)
    ss = total mod 60
    sStr = ss.ToStr()
    if ss < 10 then sStr = "0" + sStr
    if h > 0
        mStr = mm.ToStr()
        if mm < 10 then mStr = "0" + mStr
        return h.ToStr() + ":" + mStr + ":" + sStr
    end if
    return mm.ToStr() + ":" + sStr
end function
