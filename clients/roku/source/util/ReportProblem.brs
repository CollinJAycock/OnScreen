' "Report a problem" — pure helpers behind the detail page's report menu.
' Server contract: POST /api/v1/items/{id}/issues { kind, file_id? }
' (internal/api/v1/issues.go). The optional note is skipped on Roku — typing
' on the on-screen keyboard costs more than the note is worth. Wording
' mirrors web/src/lib/reportProblem.ts and the Android app's ReportProblem.
' Tested in tests/ReportProblem_test.brs.

' The kinds in the order the menu lists them.
function ReportProblem_Kinds() as Object
    return [
        { value: "video", label: "Video won't play or looks wrong" },
        { value: "audio", label: "Audio problem" },
        { value: "subtitles", label: "Subtitles problem" },
        { value: "wrong_match", label: "Wrong movie/show" },
        { value: "other", label: "Something else" }
    ]
end function

' Item types the detail page (and the episode options menu) offers the
' report on — the same set as the Android TV app.
function ReportProblem_IsReportable(itemType as Dynamic) as Boolean
    if itemType = invalid then return false
    if type(itemType) <> "String" and type(itemType) <> "roString" then return false
    return itemType = "movie" or itemType = "episode" or itemType = "show"
end function

' Request body. file_id is included only when known (movie / episode
' detail); the server checks it belongs to the item.
function ReportProblem_Body(kind as String, fileId as Dynamic) as Object
    body = { kind: kind }
    if fileId <> invalid and (type(fileId) = "String" or type(fileId) = "roString") and fileId <> "" then body.file_id = fileId
    return body
end function

' The sentence shown after a create, from an ApiResult_From result.
function ReportProblem_ResultText(result as Dynamic) as String
    if result = invalid or type(result) <> "roAssociativeArray" then return "Couldn't send the report."
    if result.ok = true then return "Thanks — an admin will take a look."
    code = ""
    if result.errorCode <> invalid then code = result.errorCode
    if code = "ALREADY_REPORTED" then return "You already reported this problem — an admin will take a look."
    if code = "TOO_MANY_OPEN_ISSUES" then return "You have several reports waiting for an admin. Try again once they have been looked at."
    if code = "RATE_LIMITED" or result.code = 429 then return "Too many reports in a short time. Try again in a minute."
    if result.code = 404 then return "This title is no longer available."
    if result.errorMessage <> invalid and result.errorMessage <> "" then return "Couldn't send the report: " + result.errorMessage
    return "Couldn't send the report."
end function
