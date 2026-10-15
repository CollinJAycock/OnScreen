' Music discovery (v2.6) on the artist / album pages: "Similar artists"
' (GET /music/artists/{id}/similar), "Appears on" (GET
' /music/artists/{id}/appears-on) and "Instant mix" (GET /music/mix?seed=)
' — response shapes in internal/api/v1/music.go. Pure — tests/Music_test.brs.

' The rows of a list response worth a card: objects with an id. Anything
' else (an older server's 404 body, a malformed row) drops out.
function Music_Rows(list as Dynamic) as Object
    out = []
    if list = invalid or type(list) <> "roArray" then return out
    for each r in list
        if r <> invalid and type(r) = "roAssociativeArray" and Music_Str(r.id) <> "" then out.push(r)
    end for
    return out
end function

' An instant mix as the player's queue: [{ id, title, type: "track" }] in
' play order — the shape PlayerScene's queue field and nextInQueue expect.
function Music_QueueFromMix(list as Dynamic) as Object
    queue = []
    for each t in Music_Rows(list)
        queue.push({ id: t.id, title: Music_Str(t.title), type: "track" })
    end for
    return queue
end function

' Second line of an "Appears on" card: "Album · Album artist" (just the
' album when the server names no album artist).
function Music_AppearanceSubtitle(row as Object) as String
    album = Music_Str(row.album_title)
    artist = Music_Str(row.album_artist_title)
    if album = "" then return artist
    if artist = "" then return album
    return album + " · " + artist
end function

function Music_Str(v as Dynamic) as String
    if v = invalid then return ""
    if type(v) <> "String" and type(v) <> "roString" then return ""
    return v
end function
