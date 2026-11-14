package tv.onscreen.mobile.ui.player

import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.api.HeartbeatRefusal
import tv.onscreen.mobile.i18n.UiText

/** The sentence for a server PARENTAL_LIMIT reason code
 *  (`outside_allowed_hours`, `daily_limit_reached`, …). The code itself is
 *  never shown. */
internal fun watchLimitText(reason: String?): UiText = when (reason) {
    "outside_allowed_hours" -> UiText.res(R.string.player_limit_outside_hours)
    "daily_limit_reached" -> UiText.res(R.string.player_limit_daily)
    else -> UiText.res(R.string.player_limit_blocked)
}

/** The sentence for a refusal the server answered a playback call with: the
 *  play decision on the phone (PlayerViewModel) and for a Chromecast
 *  (CastPlaybackController), as for a refused heartbeat. */
internal fun refusalText(refusal: HeartbeatRefusal): UiText = when (refusal) {
    is HeartbeatRefusal.WatchLimit -> watchLimitText(refusal.reason)
    is HeartbeatRefusal.PlaybackStopped -> refusal.message
    HeartbeatRefusal.ContentRevoked -> UiText.res(R.string.player_limit_blocked)
}
