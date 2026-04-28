' Unit tests for source/util/Json.brs.
'
' Run via: npm test (or `npx brs source/util/Json.brs tests/Json_test.brs`).

sub Main()
    testParseValid()
    testParseInvalid()
    testUnwrapDataPresent()
    testUnwrapDataAbsent()
    testUnwrapDataInvalid()
    testUnwrapListEnvelope()
    testUnwrapListMissing()
    testUnwrapListWrongType()

    print "DONE: Json_test"
end sub

sub testParseValid()
    parsed = Json_Parse("{""a"":1,""b"":""x""}")
    if parsed <> invalid and parsed.a = 1 and parsed.b = "x"
        print "PASS: parse valid object"
    else
        print "FAIL: parse valid object — got " + FormatJson(parsed)
    end if
end sub

sub testParseInvalid()
    parsed = Json_Parse("not json at all {")
    if parsed = invalid
        print "PASS: invalid JSON returns invalid"
    else
        print "FAIL: invalid JSON should be invalid, got " + FormatJson(parsed)
    end if
end sub

sub testUnwrapDataPresent()
    envelope = ParseJson("{""data"":{""id"":""abc"",""title"":""x""}}")
    unwrapped = Json_UnwrapData(envelope)
    if unwrapped <> invalid and unwrapped.id = "abc" and unwrapped.title = "x"
        print "PASS: unwrap envelope with data"
    else
        print "FAIL: unwrap envelope with data — got " + FormatJson(unwrapped)
    end if
end sub

sub testUnwrapDataAbsent()
    ' Object without `data` key — return as-is so plain responses
    ' (auth refresh, etc.) work too.
    envelope = ParseJson("{""token"":""abc""}")
    unwrapped = Json_UnwrapData(envelope)
    if unwrapped <> invalid and unwrapped.token = "abc"
        print "PASS: passthrough when no data key"
    else
        print "FAIL: passthrough when no data key — got " + FormatJson(unwrapped)
    end if
end sub

sub testUnwrapDataInvalid()
    if Json_UnwrapData(invalid) = invalid
        print "PASS: invalid input returns invalid"
    else
        print "FAIL: invalid input should be invalid"
    end if
end sub

sub testUnwrapListEnvelope()
    envelope = ParseJson("{""data"":[{""id"":""1""},{""id"":""2""}],""meta"":{""total"":2}}")
    list = Json_UnwrapList(envelope)
    if list.Count() = 2 and list[0].id = "1" and list[1].id = "2"
        print "PASS: unwrap list envelope"
    else
        print "FAIL: unwrap list envelope — got count " + list.Count().ToStr()
    end if
end sub

sub testUnwrapListMissing()
    ' Envelope with no `data` key → empty array (not nil).
    envelope = ParseJson("{""meta"":{""total"":0}}")
    list = Json_UnwrapList(envelope)
    if list.Count() = 0
        print "PASS: missing data returns empty array"
    else
        print "FAIL: missing data should be empty array, got count " + list.Count().ToStr()
    end if
end sub

sub testUnwrapListWrongType()
    ' `data` is a string, not array → empty array (defensive).
    envelope = ParseJson("{""data"":""not an array""}")
    list = Json_UnwrapList(envelope)
    if list.Count() = 0
        print "PASS: data wrong type returns empty array"
    else
        print "FAIL: data wrong type should be empty array, got count " + list.Count().ToStr()
    end if
end sub
