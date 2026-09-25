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

    runCase("host plain", StringUrlHost("http://192.168.1.5:7070"), "192.168.1.5")
    runCase("host userinfo+path", StringUrlHost("http://u:p@Media.Example.com:80/x?y"), "media.example.com")
    runCase("host ipv6", StringUrlHost("http://[FD00::1]:7070/"), "fd00::1")
    runCase("host no scheme", StringUrlHost("nas:7070"), "")

    runBool("local 192.168", StringIsLocalNetworkHost("192.168.1.5"), true)
    runBool("local 10/8", StringIsLocalNetworkHost("10.0.0.163"), true)
    runBool("local 172.16", StringIsLocalNetworkHost("172.20.1.1"), true)
    runBool("not local 172.32", StringIsLocalNetworkHost("172.32.1.1"), false)
    runBool("local loopback", StringIsLocalNetworkHost("127.0.0.1"), true)
    runBool("local link-local", StringIsLocalNetworkHost("169.254.3.4"), true)
    runBool("local cgnat", StringIsLocalNetworkHost("100.101.1.2"), true)
    runBool("not local public v4", StringIsLocalNetworkHost("8.8.8.8"), false)
    runBool("local .local", StringIsLocalNetworkHost("nas.local"), true)
    runBool("local single label", StringIsLocalNetworkHost("nas"), true)
    runBool("not local fqdn", StringIsLocalNetworkHost("media.example.com"), false)
    runBool("local ula", StringIsLocalNetworkHost("fd00::1"), true)
    runBool("local v6 loopback", StringIsLocalNetworkHost("::1"), true)
    runBool("not local public v6", StringIsLocalNetworkHost("2001:db8::1"), false)
    runBool("not local empty", StringIsLocalNetworkHost(""), false)

    print "DONE: Strings_test"
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
        print "FAIL: " + name
    end if
end sub
