package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.ReadingPosition
import tv.onscreen.mobile.data.model.ReadingProgressRequest
import tv.onscreen.mobile.data.prefs.ReadingPositionStore
import tv.onscreen.mobile.ui.book.ReadingSync
import javax.inject.Inject
import javax.inject.Singleton
import tv.onscreen.mobile.data.backgroundScope

/** Reading position sync (v2.6): the server's copy (GET/PUT
 *  /items/{id}/reading-progress) and this device's ([ReadingPositionStore]). */
@Singleton
open class ReadingProgressRepository @Inject constructor(
    private val api: OnScreenApi,
    private val local: ReadingPositionStore,
) {
    /** Saves outlive the reader: the last one goes out as it closes, when its
     *  viewModelScope is already cancelled. Never cancelled. */
    private val detachedScope = backgroundScope("ReadingProgressRepository")

    /** The server's position, or null — none saved, an older server (404),
     *  or unreachable (the local copy is the fallback then). */
    open suspend fun server(itemId: String): ReadingPosition? = try {
        ReadingSync.fromServer(api.getReadingProgress(itemId).data)
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }

    open fun local(itemId: String): ReadingPosition? = local.get(itemId)

    /** Record [position] here at once; the server save is the caller's
     *  (debounced) [saveDetached]. */
    open fun recordLocal(itemId: String, position: ReadingPosition) = local.put(itemId, position)

    /** Send [position] to the server, fire-and-forget. A failure leaves the
     *  local copy, which the next open weighs against the server's. */
    open fun saveDetached(itemId: String, position: ReadingPosition, deviceName: String?) {
        detachedScope.launch {
            try {
                api.putReadingProgress(
                    itemId,
                    ReadingProgressRequest(
                        locator = position.locator,
                        progression = position.progression,
                        device_name = deviceName,
                        updated_at = ReadingSync.formatTime(position.updatedAtMs),
                    ),
                )
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // Offline / an older server: the local copy stands.
            }
        }
    }
}
