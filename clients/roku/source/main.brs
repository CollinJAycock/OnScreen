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

    ' Deep links while the channel runs (Roku Search, another app, ECP
    ' /input) arrive as roInputEvents on the same port.
    input = CreateObject("roInput")
    input.SetMessagePort(port)

    ' What this Roku claims (X-Client-Capabilities), on the debug console
    ' (port 8085): the first thing to compare with the server's play
    ' decisions on a new device.
    print "OnScreen capabilities: "; Client_CapabilitiesHeader()

    ' Memory: Roku stops an app that runs past its limit. Log the limit and
    ' the warnings (roAppMemoryMonitor at 80% of it, roDeviceInfo when the
    ' device itself runs low) on the debug console, where a leak shows.
    memory = CreateObject("roAppMemoryMonitor")
    if memory <> invalid
        memory.SetMessagePort(port)
        memory.EnableMemoryWarningEvent(true)
        print "memory: limit "; FormatJson(memory.GetChannelMemoryLimit()); " available "; memory.GetChannelAvailableMemory(); " used "; memory.GetMemoryLimitPercent(); "%"
    end if
    device = CreateObject("roDeviceInfo")
    device.SetMessagePort(port)
    device.EnableLowGeneralMemoryEvent(true)

    scene = screen.CreateScene("MainScene")
    screen.Show()

    ' A launch deep link (contentId + mediaType in args) goes to the scene
    ' before start(), so the first screen mounted is the linked title and
    ' not Home on its way out. MainScene ignores a link that isn't an item.
    if args <> invalid then scene.deepLink = args
    scene.callFunc("start")

    ' Main message-pump loop. roSGScreen events fire here as the user
    ' navigates. Channel exit fires roSGScreenEvent.isScreenClosed
    ' which we use to break out cleanly.
    while true
        msg = wait(0, port)
        if type(msg) = "roSGScreenEvent" and msg.isScreenClosed()
            return
        else if type(msg) = "roInputEvent" and msg.IsInput()
            scene.deepLink = msg.GetInfo()
        else if type(msg) = "roAppMemoryMonitorEvent"
            print "memory warning: "; msg.GetInfo(); " used "; memory.GetMemoryLimitPercent(); "%, available "; memory.GetChannelAvailableMemory()
        else if type(msg) = "roDeviceInfoEvent"
            mi = msg.GetInfo()
            if mi <> invalid and mi.generalMemoryLevel <> invalid then print "device memory: "; mi.generalMemoryLevel
        end if
    end while
end sub
