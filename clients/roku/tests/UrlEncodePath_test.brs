' Unit tests for UrlEncodePath in source/api/Endpoints.brs.
'
' Run via: npm test (or `npx brs source/api/Endpoints.brs tests/UrlEncodePath_test.brs`).
'
' Failures are printed as "FAIL: ..." lines that the parent test
' runner (scripts/run-tests.mjs) greps for. brs has no native test
' harness; this is the Roku-channel equivalent of Plex's BCS test
' setups.

sub Main()
    runCase("plain ASCII unchanged", "hello", "hello")
    runCase("preserves slashes", "movies/2024/Action.mkv", "movies/2024/Action.mkv")
    runCase("encodes spaces", "Movie Title.mkv", "Movie%20Title.mkv")
    runCase("encodes parens", "Movie (2024).mkv", "Movie%20%282024%29.mkv")
    runCase("preserves dot, hyphen, underscore, tilde", "a-b_c.d~e", "a-b_c.d~e")
    runCase("encodes ampersand", "A&B", "A%26B")
    runCase("encodes plus", "1+1", "1%2B1")
    runCase("encodes question mark", "what?", "what%3F")
    runCase("empty string is empty", "", "")
    runCase("digits unchanged", "0123456789", "0123456789")
    runCase("uppercase letters unchanged", "ABCXYZ", "ABCXYZ")
    ' Non-ASCII is percent-encoded as UTF-8 bytes (encodeURIComponent).
    runCase("accented letter as two UTF-8 bytes", "Amélie", "Am%C3%A9lie")
    runCase("accented folder keeps its slashes", "Películas/Amélie (2001)/poster.jpg", "Pel%C3%ADculas/Am%C3%A9lie%20%282001%29/poster.jpg")
    runCase("sharp s", "Straße", "Stra%C3%9Fe")
    runCase("euro sign as three bytes", "€", "%E2%82%AC")
    runCase("CJK as three bytes each", "日本/アニメ", "%E6%97%A5%E6%9C%AC/%E3%82%A2%E3%83%8B%E3%83%A1")
    runCase("a character outside the BMP as four bytes", "🎬", "%F0%9F%8E%AC")

    print "DONE: UrlEncodePath_test"
end sub

sub runCase(name as String, input as String, expected as String)
    actual = UrlEncodePath(input)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — input=[" + input + "] expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
