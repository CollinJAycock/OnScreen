package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.WatchLimitData
import javax.inject.Inject
import javax.inject.Singleton

/** The caller's own parental watch policy + today's usage + current
 *  allowed/blocked state. Thin wrapper over GET /users/me/watch-limit — the
 *  player reads it to block a restricted user before a stream starts. */
@Singleton
open class WatchLimitRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun get(): WatchLimitData = api.getWatchLimit().data
}
