' Unit tests for source/util/Strings.brs.

sub Main()
    runCase("trim removes leading spaces", StringTrim("   hello"), "hello")
    runCase("trim removes trailing spaces", StringTrim("hello   "), "hello")
    runCase("trim removes both", StringTrim("  hello  "), "hello")
    runCase("trim leaves inner spaces", StringTrim("hello world"), "hello world")
    runCase("trim empty string", StringTrim(""), "")
    runCase("trim only spaces", StringTrim("     "), "")

    runCase("strip trailing slash present", StringStripTrailingSlash("http://x/"), "http://x")
    runCase("strip trailing slash absent (no-op)", StringStripTrailingSlash("http://x"), "http://x")
    runCase("strip trailing slash empty", StringStripTrailingSlash(""), "")
    runCase("strip strips only one slash", StringStripTrailingSlash("http://x//"), "http://x/")

    print "DONE: Strings_test"
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
