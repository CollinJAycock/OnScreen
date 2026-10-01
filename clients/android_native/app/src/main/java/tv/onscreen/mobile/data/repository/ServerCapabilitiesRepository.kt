package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.ServerCapabilities
import tv.onscreen.mobile.data.prefs.ServerPrefs
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The connected server's `/api/v1/system/capabilities`, asked once per server
 * for the life of the process. Keyed on the server url, so Settings → change
 * server asks the new one rather than answering with the old one's flags.
 *
 * Every answer defaults to "not supported": a failed or not-yet-possible fetch
 * reads as the feature missing, and is not remembered — the next caller asks
 * again. Concurrent first callers share one request.
 */
@Singleton
open class ServerCapabilitiesRepository @Inject constructor(
    private val api: OnScreenApi,
    private val serverPrefs: ServerPrefs,
) {
    private val mutex = Mutex()
    private var cachedFor: String? = null
    private var cached: ServerCapabilities? = null

    /** Whether a progress report may leave its duration out (see
     *  ServerFeatures.progress_without_duration). False when the server
     *  doesn't say so, or can't be asked. */
    open suspend fun progressWithoutDuration(): Boolean =
        capabilities()?.features?.progress_without_duration == true

    private suspend fun capabilities(): ServerCapabilities? {
        val server = serverPrefs.getServerUrl()?.trimEnd('/')
        if (server.isNullOrEmpty()) return null
        return mutex.withLock {
            if (cachedFor == server) return@withLock cached
            val fresh = try {
                api.getCapabilities().data
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                return@withLock null
            }
            cachedFor = server
            cached = fresh
            fresh
        }
    }
}
