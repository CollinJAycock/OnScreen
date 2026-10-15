' Unit tests for source/util/DetailMeta.brs. Fixtures are parsed JSON so
' missing keys are missing exactly as the server omits them.

sub Main()
    testLine()
    testStudios()

    print "DONE: DetailMeta_test"
end sub

sub testLine()
    movie = ParseJson("{""year"":2019,""content_rating"":""R"",""duration_ms"":7920000,""genres"":[""Drama""],""studios"":[{""id"":""s1"",""name"":""A24"",""kind"":""studio""},{""id"":""s2"",""name"":""Plan B"",""kind"":""studio""}]}")
    runStr("line: studios after the genre", DetailMeta_Line(movie), "2019  ·  R  ·  2h 12m  ·  Drama  ·  A24, Plan B")
    old = ParseJson("{""year"":2019,""duration_ms"":1800000,""genres"":[]}")
    runStr("line: no studios (older server)", DetailMeta_Line(old), "2019  ·  30m")
    runStr("line: empty item", DetailMeta_Line({}), "")
end sub

sub testStudios()
    three = ParseJson("[{""name"":""HBO"",""kind"":""network""},{""name"":""Sky""},{""name"":""Canal+""}]")
    runStr("studios: capped", DetailMeta_Studios(three, 2), "HBO, Sky")
    runStr("studios: one", DetailMeta_Studios(three, 1), "HBO")
    runStr("studios: malformed rows skipped", DetailMeta_Studios([invalid, { name: "" }, { name: 5 }, { name: "AMC" }], 2), "AMC")
    runStr("studios: missing", DetailMeta_Studios(invalid, 2), "")
    runStr("studios: not a list", DetailMeta_Studios("A24", 2), "")
end sub

sub runStr(name as String, actual as Dynamic, expected as String)
    got = "<invalid>"
    if actual <> invalid then got = actual
    if got = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + got + "]"
    end if
end sub
