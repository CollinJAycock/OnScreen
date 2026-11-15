' How often a paused player re-reads the item for a position another device
' reported (PlayerScene's syncTimer, which ticks every 5 s). Pure
' (tests/PausedSync_test.brs).

' The wait between reads after pausedMs of pause: every tick for the
' first 2 minutes (the "continue on the phone, come back" case), every
' 30 s to half an hour, then every 5 minutes. At a flat 5 s a Roku left
' paused overnight sent ~35,000 full item reads (and kept its token fresh).
function PausedSync_IntervalMs(pausedMs as Integer) as Integer
    if pausedMs < 120000 then return 5000
    if pausedMs < 1800000 then return 30000
    return 300000
end function

' Whether a tick sinceLastMs after the last read should read again. A
' second of slack, as the timer's 5 s ticks don't land exactly 5000 ms
' apart. sinceLastMs < 0 means no read yet in this pause.
function PausedSync_Due(pausedMs as Integer, sinceLastMs as Integer) as Boolean
    if sinceLastMs < 0 then return true
    return sinceLastMs >= PausedSync_IntervalMs(pausedMs) - 1000
end function
