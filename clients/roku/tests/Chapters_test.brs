' Unit tests for source/playback/Chapters.brs.

sub Main()
    chapters = [
        { title: "Opening", start_ms: 0, end_ms: 45000 },
        { title: "", start_ms: 45000, end_ms: 723000 },
        { title: "  The Heist ", start_ms: 3735000, end_ms: 4000000 }
    ]
    rows = Chapters_Rows(chapters)
    runCase("rows: count", rows.Count().ToStr(), "3")
    runCase("rows: first", rows[0], "1. Opening  ·  0:00")
    runCase("rows: untitled", rows[1], "2. Chapter 2  ·  0:45")
    runCase("rows: trimmed, hours", rows[2], "3. The Heist  ·  1:02:15")
    runCase("rows: none", Chapters_Rows(invalid).Count().ToStr(), "0")

    runCase("index: start", Chapters_IndexAt(chapters, 0).ToStr(), "0")
    runCase("index: inside second", Chapters_IndexAt(chapters, 60000).ToStr(), "1")
    runCase("index: on a boundary", Chapters_IndexAt(chapters, 45000).ToStr(), "1")
    runCase("index: past the last start", Chapters_IndexAt(chapters, 5000000).ToStr(), "2")
    runCase("index: no chapters", Chapters_IndexAt([], 1000).ToStr(), "-1")

    runCase("time: seconds", Chapters_FormatTime(9000), "0:09")
    runCase("time: minutes", Chapters_FormatTime(723000), "12:03")
    runCase("time: hours", Chapters_FormatTime(3735000), "1:02:15")
    runCase("time: negative", Chapters_FormatTime(-5), "0:00")

    junk = [{ title: "(01)00:00:00:000", start_ms: 0 }, { title: "00:07:13.307", start_ms: 433307 }, { title: "Chapter 03", start_ms: 683432 }, { title: "Act 4", start_ms: 1082790 }]
    jr = Chapters_Rows(junk)
    runCase("rows: muxer timestamp title", jr[0], "1. Chapter 1  ·  0:00")
    runCase("rows: plain timestamp title", jr[1], "2. Chapter 2  ·  7:13")
    runCase("rows: Chapter NN", jr[2], "3. Chapter 3  ·  11:23")
    runCase("rows: a real title stays", jr[3], "4. Act 4  ·  18:02")

    print "DONE: Chapters_test"
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub
