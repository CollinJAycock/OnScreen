package tv.onscreen.android.data.repository

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.Capabilities
import javax.inject.Inject
import javax.inject.Singleton

/**
 * In-memory cache for `/api/v1/system/capabilities`. The endpoint is
 * cheap, public, and stable across a server's lifetime (admin toggles
 * surface only after a refresh). Consumers fetch once via
 * [getCachedOrFetch] and read [observe] to gate UI reactively as the
 * value lands.
 *
 * Why a singleton repository instead of one fetch per consumer:
 *   - Tabs (Live TV / DVR / Requests) are scattered across fragments;
 *     each one independently fetching means N parallel hits on app
 *     boot when a single response would do.
 *   - SSE subscribers and the playback-transfer handler also want the
 *     `web_downloads` flag without a second round-trip.
 *   - Single-flight via [mutex] coalesces concurrent first-call races
 *     (HomeFragment + the prefetch in MainActivity firing simultaneously)
 *     so we never make two parallel calls.
 *
 * Refresh: caller-driven via [refresh]. The repo doesn't poll —
 * capabilities flip rarely, almost always behind a settings change
 * the operator just made on the same device, in which case a manual
 * refresh after the toggle handles it. Server restart blows the
 * runtime probes (ffmpeg / tesseract / fpcalc / pg_dump) and is the
 * other case worth a refresh; the SSE stream's reconnection pattern
 * is a reasonable cue to refire.
 */
@Singleton
open class CapabilitiesRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    private val state = MutableStateFlow<Capabilities?>(null)
    private val mutex = Mutex()

    /** Reactive feed. Null while the first fetch is pending. */
    open fun observe(): StateFlow<Capabilities?> = state.asStateFlow()

    /** Returns the cached value or fetches once. Multiple concurrent
     *  callers share one in-flight request via the mutex. */
    open suspend fun getCachedOrFetch(): Capabilities? {
        state.value?.let { return it }
        mutex.withLock {
            // Re-check inside the lock — another caller may have
            // populated the cache while we were waiting.
            state.value?.let { return it }
            return fetch()
        }
    }

    /** Force-refresh from the server. Caller already lost the cache
     *  semantics by asking, so no need to re-check inside the lock. */
    open suspend fun refresh(): Capabilities? {
        mutex.withLock { return fetch() }
    }

    private suspend fun fetch(): Capabilities? = try {
        val resp = api.getCapabilities().data
        state.value = resp
        resp
    } catch (_: Exception) {
        // Capabilities is best-effort — a transient failure leaves the
        // cache at its prior value (or null on first failure). UI gates
        // fall through to the safe default of "feature off" until a
        // later fetch succeeds.
        state.value
    }
}
