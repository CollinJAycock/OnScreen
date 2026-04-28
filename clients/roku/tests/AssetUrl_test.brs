' Unit tests for the asset URL builders in source/api/Endpoints.brs.
' Pinning the exact format of the URLs keeps us from accidentally
' breaking the contract the Go server's asset-route middleware
' expects (`?token=` carries the bearer for static-file requests
' that can't attach an Authorization header).

sub Main()
    testStreamUrlBasic()
    testStreamUrlPreservesFileId()
    testArtworkUrlBasic()
    testArtworkUrlEncodesSpecialChars()
    testArtworkUrlIncludesWidth()
    testArtworkUrlIncludesToken()

    print "DONE: AssetUrl_test"
end sub

sub testStreamUrlBasic()
    actual = AssetStream("http://example.com", "abc-123", "tok")
    expected = "http://example.com/media/stream/abc-123?token=tok"
    runCase("stream url basic", actual, expected)
end sub

sub testStreamUrlPreservesFileId()
    ' UUIDs contain hyphens — must pass through unencoded.
    actual = AssetStream("http://x", "550e8400-e29b-41d4-a716-446655440000", "t")
    expected = "http://x/media/stream/550e8400-e29b-41d4-a716-446655440000?token=t"
    runCase("stream url preserves uuid", actual, expected)
end sub

sub testArtworkUrlBasic()
    actual = AssetArtwork("http://x", "movies/poster.jpg", 500, "t")
    expected = "http://x/artwork/movies/poster.jpg?w=500&token=t"
    runCase("artwork url basic", actual, expected)
end sub

sub testArtworkUrlEncodesSpecialChars()
    actual = AssetArtwork("http://x", "Movies (2024)/poster.jpg", 500, "t")
    expected = "http://x/artwork/Movies%20%282024%29/poster.jpg?w=500&token=t"
    runCase("artwork url encodes parens + spaces", actual, expected)
end sub

sub testArtworkUrlIncludesWidth()
    actual = AssetArtwork("http://x", "p.jpg", 1200, "t")
    if Instr(0, actual, "w=1200") > 0
        print "PASS: artwork url includes width"
    else
        print "FAIL: artwork url includes width — got " + actual
    end if
end sub

sub testArtworkUrlIncludesToken()
    actual = AssetArtwork("http://x", "p.jpg", 500, "abcdef")
    if Instr(0, actual, "token=abcdef") > 0
        print "PASS: artwork url includes token"
    else
        print "FAIL: artwork url includes token — got " + actual
    end if
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
