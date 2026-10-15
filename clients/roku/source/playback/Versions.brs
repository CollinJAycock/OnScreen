' Versions of an item for the version picker and the player: each file is a
' version, except that the parts of a multi-part movie ("cd1" + "cd2",
' files[].part_index, v2.6) are ONE version together. The server only sets
' part_index on files that really play as a stack, and a transcode started
' with part 1's file_id streams every part as one movie — so a part is never
' offered on its own and never direct-played. Mirrors web/src/lib/
' fileVersions.ts. Pure — tests/Versions_test.brs.

' Parts of one stack share an edition and a container (the server also
' requires the same folder, which the API doesn't expose). "" for a file
' that isn't a part.
function Versions_StackKey(f as Dynamic) as String
    if f = invalid or type(f) <> "roAssociativeArray" then return ""
    if f.part_index = invalid then return ""
    return "part|" + LCase(Versions_Str(f.edition).Trim()) + "|" + LCase(Versions_Str(f.container))
end function

' The parts of the stack f belongs to, in part order; invalid when f is not
' a part of a stack of two or more.
function Versions_StackOf(files as Dynamic, f as Dynamic) as Dynamic
    key = Versions_StackKey(f)
    if key = "" or files = invalid or type(files) <> "roArray" then return invalid
    parts = []
    for each g in files
        if Versions_StackKey(g) = key then Versions_InsertByPart(parts, g)
    end for
    if parts.Count() < 2 then return invalid
    return parts
end function

' Insert f into parts keeping part_index order (roArray has no comparator
' sort; a stack is a handful of files).
sub Versions_InsertByPart(parts as Object, f as Object)
    i = parts.Count()
    while i > 0 and parts[i - 1].part_index > f.part_index
        i = i - 1
    end while
    out = []
    for j = 0 to parts.Count() - 1
        if j = i then out.push(f)
        out.push(parts[j])
    end for
    if i = parts.Count() then out.push(f)
    parts.Clear()
    parts.Append(out)
end sub

' files grouped into versions, in the server's order (best first):
' [{ primary, files }] where primary is the file playback starts from
' (part 1 of a stack) and files is every file of the version in part order.
function Versions_Group(files as Dynamic) as Object
    out = []
    if files = invalid or type(files) <> "roArray" then return out
    seen = {}
    for each f in files
        if f <> invalid and type(f) = "roAssociativeArray" and Versions_Str(f.id) <> "" and not seen.DoesExist(f.id)
            parts = Versions_StackOf(files, f)
            if parts = invalid then parts = [f]
            for each p in parts
                seen[p.id] = true
            end for
            out.push({ primary: parts[0], files: parts })
        end if
    end for
    return out
end function

' The version to play: the one holding fileId (a part names its whole
' stack), else the first. invalid when there are no files.
function Versions_Pick(files as Dynamic, fileId as Dynamic) as Dynamic
    versions = Versions_Group(files)
    if versions.Count() = 0 then return invalid
    want = Versions_Str(fileId)
    if want <> ""
        for each v in versions
            for each p in v.files
                if p.id = want then return v
            end for
        end for
    end if
    return versions[0]
end function

' Picker label: the edition the file name gives ("Director's Cut"), else
' the technical description ("4K · HEVC"), else "Version n"; plus the part
' count for a multi-part version.
function Versions_Label(v as Object, n as Integer) as String
    base = Versions_Str(v.primary.edition).Trim()
    if base = "" then base = Versions_TechLabel(v.primary)
    if base = "" then base = "Version " + n.ToStr()
    if v.files.Count() > 1 then base = base + " · " + v.files.Count().ToStr() + " parts"
    return base
end function

' "4K · HEVC", "1080p · H.264", or "" when the file says neither.
function Versions_TechLabel(f as Object) as String
    parts = []
    h = 0
    if f.resolution_h <> invalid then h = f.resolution_h
    if h >= 2000
        parts.push("4K")
    else if h > 0
        parts.push(h.ToStr() + "p")
    end if
    codec = LCase(Versions_Str(f.video_codec))
    if codec = "h264" or codec = "avc"
        parts.push("H.264")
    else if codec = "hevc" or codec = "h265"
        parts.push("HEVC")
    else if codec <> ""
        parts.push(UCase(codec))
    end if
    out = ""
    for each p in parts
        if out <> "" then out = out + " · "
        out = out + p
    end for
    return out
end function

function Versions_Str(v as Dynamic) as String
    if v = invalid then return ""
    if type(v) <> "String" and type(v) <> "roString" then return ""
    return v
end function
