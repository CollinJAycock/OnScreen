' Unit tests for source/playback/PausedSync.brs.

sub Main()
    runCase("interval: just paused", PausedSync_IntervalMs(0).ToStr(), "5000")
    runCase("interval: a minute in", PausedSync_IntervalMs(60000).ToStr(), "5000")
    runCase("interval: past 2 minutes", PausedSync_IntervalMs(120000).ToStr(), "30000")
    runCase("interval: 29 minutes in", PausedSync_IntervalMs(1740000).ToStr(), "30000")
    runCase("interval: past half an hour", PausedSync_IntervalMs(1800000).ToStr(), "300000")
    runCase("interval: overnight", PausedSync_IntervalMs(8 * 3600000).ToStr(), "300000")

    runBool("due: first read of a pause", PausedSync_Due(0, -1), true)
    runBool("due: next 5 s tick, early on", PausedSync_Due(10000, 4990), true)
    runBool("due: 5 s tick, later on", PausedSync_Due(300000, 5000), false)
    runBool("due: 30 s on, later on", PausedSync_Due(300000, 29995), true)
    runBool("due: 30 s on, overnight", PausedSync_Due(8 * 3600000, 30000), false)
    runBool("due: 5 min on, overnight", PausedSync_Due(8 * 3600000, 299990), true)

    ' An 8-hour pause: reads per tick of a 5 s timer.
    reads = 0
    sinceLast = -1
    for t = 0 to 8 * 3600000 step 5000
        if PausedSync_Due(t, sinceLast)
            reads = reads + 1
            sinceLast = 0
        end if
        sinceLast = sinceLast + 5000
    end for
    runBool("overnight: under 200 reads (was ~5,760)", reads < 200, true)

    print "DONE: PausedSync_test"
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
