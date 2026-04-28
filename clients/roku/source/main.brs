' OnScreen Roku channel entry point.
'
' Roku spawns the BrightScript runtime and looks for a function
' literally named `Main` (or `RunUserInterface` for SceneGraph
' channels) in source/. We use Main → instantiate the SceneGraph
' MainScene → hand control to its BrightScript on-create handler.
' Beyond this point everything is event-driven through the
' SceneGraph message port.
sub Main(args as Object)
    screen = CreateObject("roSGScreen")
    port = CreateObject("roMessagePort")
    screen.SetMessagePort(port)

    scene = screen.CreateScene("MainScene")
    screen.Show()

    ' Forward any deep-link args (e.g., resume-with-item from voice
    ' search or external apps) to the scene so it can route past the
    ' first-run setup screen if appropriate.
    if args <> invalid and args.contentId <> invalid
        scene.deepLinkContentId = args.contentId
    end if

    ' Main message-pump loop. roSGScreen events fire here as the user
    ' navigates. Channel exit fires roSGScreenEvent.isScreenClosed
    ' which we use to break out cleanly.
    while true
        msg = wait(0, port)
        if type(msg) = "roSGScreenEvent" and msg.isScreenClosed()
            return
        end if
    end while
end sub
