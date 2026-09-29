' Unit tests for source/util/ReportProblem.brs.

sub Main()
    testKinds()
    testReportable()
    testBody()
    testResultText()

    print "DONE: ReportProblem_test"
end sub

sub testKinds()
    kinds = ReportProblem_Kinds()
    values = ""
    for each k in kinds
        if values <> "" then values = values + ","
        values = values + k.value
    end for
    ' The server's IsValidIssueKind set, in the web / Android menu order.
    runStr("kinds: values in order", values, "video,audio,subtitles,wrong_match,other")
    runStr("kinds: first label", kinds[0].label, "Video won't play or looks wrong")
    runStr("kinds: wrong match label", kinds[3].label, "Wrong movie/show")
end sub

sub testReportable()
    runBool("reportable: movie", ReportProblem_IsReportable("movie"), true)
    runBool("reportable: episode", ReportProblem_IsReportable("episode"), true)
    runBool("reportable: show", ReportProblem_IsReportable("show"), true)
    runBool("reportable: season is not", ReportProblem_IsReportable("season"), false)
    runBool("reportable: track is not", ReportProblem_IsReportable("track"), false)
    runBool("reportable: invalid", ReportProblem_IsReportable(invalid), false)
end sub

sub testBody()
    b = ReportProblem_Body("audio", "f-1")
    runStr("body: kind", b.kind, "audio")
    runStr("body: file id", b.file_id, "f-1")
    b = ReportProblem_Body("other", invalid)
    runBool("body: no file id key", b.DoesExist("file_id"), false)
    runStr("body: no file keeps kind", b.kind, "other")
    runBool("body: empty file id omitted", ReportProblem_Body("video", "").DoesExist("file_id"), false)
    ' No note on Roku — the key isn't sent at all.
    runBool("body: no note", b.DoesExist("note"), false)
end sub

sub testResultText()
    runStr("result: created", ReportProblem_ResultText(ApiResult_From(201, "{""data"":{""id"":""i""}}")), "Thanks — an admin will take a look.")
    runStr("result: already reported", ReportProblem_ResultText(ApiResult_From(409, err("ALREADY_REPORTED", "you already have an open report"))), "You already reported this problem — an admin will take a look.")
    runStr("result: too many open", ReportProblem_ResultText(ApiResult_From(429, err("TOO_MANY_OPEN_ISSUES", "you have 10 open reports"))), "You have several reports waiting for an admin. Try again once they have been looked at.")
    runStr("result: rate limited", ReportProblem_ResultText(ApiResult_From(429, err("RATE_LIMITED", "rate limit exceeded"))), "Too many reports in a short time. Try again in a minute.")
    runStr("result: gone", ReportProblem_ResultText(ApiResult_From(404, err("NOT_FOUND", "resource not found"))), "This title is no longer available.")
    runStr("result: server message", ReportProblem_ResultText(ApiResult_From(422, err("VALIDATION", "invalid file_id"))), "Couldn't send the report: invalid file_id")
    runStr("result: network", ReportProblem_ResultText(ApiResult_From(-7, "")), "Couldn't send the report.")
    runStr("result: invalid", ReportProblem_ResultText(invalid), "Couldn't send the report.")
end sub

function err(code as String, message as String) as String
    return "{""error"":{""code"":""" + code + """,""message"":""" + message + """}}"
end function

sub runStr(name as String, actual as String, expected as String)
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
