' The detail page's meta line: "2019  ·  PG-13  ·  2h 12m  ·  ★ 7.5  ·
' Drama  ·  A24, Plan B". Pure — tests/DetailMeta_test.brs.

function DetailMeta_Line(item as Object) as String
    parts = []
    if item.year <> invalid then parts.push(item.year.ToStr())
    if item.content_rating <> invalid and item.content_rating <> "" then parts.push(item.content_rating)
    if item.duration_ms <> invalid and item.duration_ms > 0
        mins = Int(item.duration_ms / 60000)
        if mins >= 60
            h = Int(mins / 60)
            mm = mins mod 60
            parts.push(h.ToStr() + "h " + mm.ToStr() + "m")
        else
            parts.push(mins.ToStr() + "m")
        end if
    end if
    if item.rating <> invalid and item.rating > 0
        parts.push("★ " + Str(item.rating))
    end if
    if item.genres <> invalid and item.genres.Count() > 0
        parts.push(item.genres[0])
    end if
    studios = DetailMeta_Studios(item.studios, 2)
    if studios <> "" then parts.push(studios)
    out = ""
    for i = 0 to parts.Count() - 1
        if i > 0 then out = out + "  ·  "
        out = out + parts[i]
    end for
    return out
end function

' A movie's studios or a show's networks (studios[].name, v2.6), the first
' max of them in the server's order, joined with ", ". "" when the item has
' none (older servers omit the field).
function DetailMeta_Studios(studios as Dynamic, max as Integer) as String
    if studios = invalid or type(studios) <> "roArray" then return ""
    out = ""
    n = 0
    for each s in studios
        if n >= max then exit for
        if s <> invalid and type(s) = "roAssociativeArray" and DetailMeta_IsText(s.name)
            if out <> "" then out = out + ", "
            out = out + s.name
            n = n + 1
        end if
    end for
    return out
end function

function DetailMeta_IsText(v as Dynamic) as Boolean
    if v = invalid then return false
    if type(v) <> "String" and type(v) <> "roString" then return false
    return v <> ""
end function
