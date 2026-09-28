' Next item to auto-advance to, picked from the playing item's parent
' /children listing. Kept out of PlayerScene so it's unit-testable under
' brs without a SceneGraph node.

' The row of kids that plays after item, or invalid.
'
' Tracks follow album order: disc, then track number. A multi-disc album
' numbers each disc from 1, so matching index + 1 landed on the wrong disc
' (disc 2 track 5 -> disc 1 track 6) and never crossed from the end of
' disc 1 to disc 2. item comes from /items/{id}, which carries no disc, so
' the track's own row in kids supplies it; a row without disc_number is on
' disc 1.
'
' Other types (episodes, chapters, podcast episodes) take the exact next
' number, as they always have.
function nextSiblingOf(item as Object, kids as Object) as Dynamic
    if item = invalid or kids = invalid or item.index = invalid then return invalid

    if item.type <> "track"
        for each k in kids
            ' k <> invalid guards a malformed children row; k.index is
            ' checked before the comparison for the same reason.
            if k <> invalid and k.type = item.type and k.index <> invalid and k.index = item.index + 1
                return k
            end if
        end for
        return invalid
    end if

    myDisc = 1
    for each k in kids
        if k <> invalid and k.id <> invalid and k.id = item.id then myDisc = siblingDisc(k)
    end for

    ' The nearest (disc, index) after the current one. A scan rather than a
    ' sort: roArray has no comparator sort, and kids is one album.
    best = invalid
    for each k in kids
        if k <> invalid and k.type = "track" and k.index <> invalid
            d = siblingDisc(k)
            if d > myDisc or (d = myDisc and k.index > item.index)
                if best = invalid
                    best = k
                else
                    bestDisc = siblingDisc(best)
                    if d < bestDisc or (d = bestDisc and k.index < best.index) then best = k
                end if
            end if
        end if
    end for
    return best
end function

function siblingDisc(row as Object) as Integer
    if row.disc_number = invalid then return 1
    return row.disc_number
end function
