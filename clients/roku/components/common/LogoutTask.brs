sub init()
    m.top.functionName = "runLogout"
end sub

sub runLogout()
    Client_LogoutSync()
    m.top.control = "DONE"
end sub
