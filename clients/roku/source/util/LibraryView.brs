' A library grid's sort and filters: the choices (as the TV web app's
' lib/librarySort.ts and Android TV's LibraryFragment name them) and the
' items path they make. Pure (tests/LibraryView_test.brs). The labels come
' from the catalog (source/util/I18n.brs).

function LibraryView_SortOptions() as Object
    return [
        { sort: "title", dir: "asc", label: I18n_T("library.sortTitleAsc") },
        { sort: "title", dir: "desc", label: I18n_T("library.sortTitleDesc") },
        { sort: "created_at", dir: "desc", label: I18n_T("library.sortRecent") },
        { sort: "year", dir: "desc", label: I18n_T("library.sortNewest") },
        { sort: "year", dir: "asc", label: I18n_T("library.sortOldest") },
        { sort: "rating", dir: "desc", label: I18n_T("library.sortRating") }
    ]
end function

' The sort a library opens with: newest added first for the date-driven
' kinds (home videos, photos, recordings), else by title.
function LibraryView_DefaultSort(libraryType as Dynamic) as Integer
    if libraryType = "home_video" or libraryType = "photo" or libraryType = "dvr" then return 2
    return 0
end function

' A stored sort ("created_at:desc") back to its option; -1 for anything
' else.
function LibraryView_ParseSort(stored as Dynamic) as Integer
    if stored = invalid or (type(stored) <> "String" and type(stored) <> "roString") then return -1
    opts = LibraryView_SortOptions()
    for i = 0 to opts.Count() - 1
        if opts[i].sort + ":" + opts[i].dir = stored then return i
    end for
    return -1
end function

function LibraryView_SortKey(idx as Integer) as String
    o = LibraryView_SortOptions()[idx]
    return o.sort + ":" + o.dir
end function

' The server's ?watch= values; "" is everything.
function LibraryView_WatchOptions() as Object
    return [
        { value: "", label: I18n_T("library.watchAll") },
        { value: "unwatched", label: I18n_T("library.watchUnwatched") },
        { value: "in_progress", label: I18n_T("library.watchInProgress") },
        { value: "watched", label: I18n_T("library.watchWatched") }
    ]
end function

' Watch state and genre mean something for video libraries; music,
' photos and the spoken-word kinds browse without them.
function LibraryView_HasFilters(libraryType as Dynamic) as Boolean
    if libraryType = invalid then return true
    for each t in ["music", "photo", "audiobook", "podcast", "book"]
        if libraryType = t then return false
    end for
    return true
end function

' GET /libraries/{id}/items for a page in this view.
function LibraryView_ItemsPath(libraryId as String, offset as Integer, limit as Integer, sortIdx as Integer, watch as String, genre as String) as String
    opts = LibraryView_SortOptions()
    if sortIdx < 0 or sortIdx >= opts.Count() then sortIdx = 0
    o = opts[sortIdx]
    p = "/api/v1/libraries/" + libraryId + "/items?sort=" + o.sort + "&sort_dir=" + o.dir
    if watch <> "" then p = p + "&watch=" + watch
    if genre <> "" then p = p + "&genre=" + LibraryView_Encode(genre)
    return p + "&limit=" + limit.ToStr() + "&offset=" + offset.ToStr()
end function

' encodeURIComponent: UTF-8, every byte outside A-Z a-z 0-9 - _ . ~ as %XX.
function LibraryView_Encode(s as String) as String
    out = ""
    for i = 1 to Len(s)
        ch = Mid(s, i, 1)
        c = Asc(ch)
        if (ch >= "a" and ch <= "z") or (ch >= "A" and ch <= "Z") or (ch >= "0" and ch <= "9") or ch = "-" or ch = "_" or ch = "." or ch = "~"
            out = out + ch
        else if c < &H80
            out = out + LibraryView_Hex(c)
        else if c < &H800
            out = out + LibraryView_Hex(&HC0 or (c >> 6)) + LibraryView_Hex(&H80 or (c and &H3F))
        else
            out = out + LibraryView_Hex(&HE0 or (c >> 12)) + LibraryView_Hex(&H80 or ((c >> 6) and &H3F)) + LibraryView_Hex(&H80 or (c and &H3F))
        end if
    end for
    return out
end function

function LibraryView_Hex(b as Integer) as String
    digits = "0123456789ABCDEF"
    return "%" + Mid(digits, ((b >> 4) and &HF) + 1, 1) + Mid(digits, (b and &HF) + 1, 1)
end function
