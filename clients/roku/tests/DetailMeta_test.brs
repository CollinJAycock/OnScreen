' Unit tests for source/util/DetailMeta.brs. Fixtures are parsed JSON so
' missing keys are missing exactly as the server omits them.

sub Main()
    testLine()
    testStudios()
    testFitWidths()

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

sub testFitWidths()
    ' Play + Mark + Status + Report: 1560 + 72 fits 1760 as is.
    runStr("fit: row that fits is unchanged", joinInts(DetailMeta_FitWidths([400, 360, 440, 360], 24, 1760)), "400,360,440,360")
    ' + Version (480): 2040 + 96 > 1760 → every width × 1664 / 2040.
    fitted = DetailMeta_FitWidths([400, 480, 360, 440, 360], 24, 1760)
    runStr("fit: overflowing row scaled down", joinInts(fitted), "326,391,293,358,293")
    total = 0
    for each w in fitted
        total = total + w
    end for
    runBool("fit: scaled row fits", total + 96 <= 1760, true)
    runStr("fit: exactly full is unchanged", joinInts(DetailMeta_FitWidths([868, 868], 24, 1760)), "868,868")
    runStr("fit: one button", joinInts(DetailMeta_FitWidths([400], 24, 1760)), "400")
    runStr("fit: none", joinInts(DetailMeta_FitWidths([], 24, 1760)), "")
end sub

function joinInts(list as Object) as String
    out = ""
    for i = 0 to list.Count() - 1
        if i > 0 then out = out + ","
        out = out + list[i].ToStr()
    end for
    return out
end function

sub runStr(name as String, actual as Dynamic, expected as String)
    got = "<invalid>"
    if actual <> invalid then got = actual
    if got = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + got + "]"
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name
    end if
end sub
