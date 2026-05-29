package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.ScrobbleStatusResponse
import tv.onscreen.mobile.data.model.SetListenBrainzRequest
import javax.inject.Inject
import javax.inject.Singleton

/** Per-user external-scrobble link (ListenBrainz today). Thin wrapper over the
 *  authenticated /users/me/scrobble endpoints — the token is write-only, so we
 *  only ever read back linked/enabled status. */
@Singleton
open class ScrobbleRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun status(): ScrobbleStatusResponse = api.scrobbleStatus().data

    /** Link or update the ListenBrainz token. An empty token unlinks the
     *  account (and the server forces enabled=false in that case). */
    open suspend fun setListenBrainz(token: String, enabled: Boolean) =
        api.setListenBrainz(SetListenBrainzRequest(token, enabled))
}
