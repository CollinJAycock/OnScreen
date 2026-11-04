' Unit tests for source/util/LibraryView.brs.

sub Main()
    runCase("default: movies by title", LibraryView_DefaultSort("movie").ToStr(), "0")
    runCase("default: home videos newest added", LibraryView_DefaultSort("home_video").ToStr(), "2")
    runCase("default: no type", LibraryView_DefaultSort(invalid).ToStr(), "0")
    runCase("parse: stored", LibraryView_ParseSort("year:asc").ToStr(), "4")
    runCase("parse: unknown", LibraryView_ParseSort("size:asc").ToStr(), "-1")
    runCase("parse: missing", LibraryView_ParseSort(invalid).ToStr(), "-1")
    runCase("key", LibraryView_SortKey(5), "rating:desc")

    runCase("path: default", LibraryView_ItemsPath("L1", 0, 200, 0, "", ""), "/api/v1/libraries/L1/items?sort=title&sort_dir=asc&limit=200&offset=0")
    runCase("path: added, unwatched", LibraryView_ItemsPath("L1", 200, 200, 2, "unwatched", ""), "/api/v1/libraries/L1/items?sort=created_at&sort_dir=desc&watch=unwatched&limit=200&offset=200")
    runCase("path: genre encoded", LibraryView_ItemsPath("L1", 0, 50, 0, "", "Action & Adventure"), "/api/v1/libraries/L1/items?sort=title&sort_dir=asc&genre=Action%20%26%20Adventure&limit=50&offset=0")
    runCase("path: bad sort falls back", LibraryView_ItemsPath("L1", 0, 50, 9, "", ""), "/api/v1/libraries/L1/items?sort=title&sort_dir=asc&limit=50&offset=0")

    runCase("encode: plain", LibraryView_Encode("Sci-Fi"), "Sci-Fi")
    runCase("encode: slash", LibraryView_Encode("War/Politics"), "War%2FPolitics")
    runCase("encode: two-byte", LibraryView_Encode("Comédie"), "Com%C3%A9die")

    runBool("filters: movies", LibraryView_HasFilters("movie"), true)
    runBool("filters: music", LibraryView_HasFilters("music"), false)
    runCase("watch options", LibraryView_WatchOptions()[2].label, "In progress")

    print "DONE: LibraryView_test"
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
