package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.Channel
import tv.onscreen.android.data.model.NowNext
import tv.onscreen.android.data.model.Recording
import javax.inject.Inject
import javax.inject.Singleton

/** Live TV + DVR data access. The "now/next" join is intentionally
 *  done client-side: the channels endpoint is dirt cheap and very
 *  cacheable while the EPG endpoint changes every few minutes. */
@Singleton
class LiveTVRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    suspend fun getChannels(): List<Channel> = api.getChannels(enabledOnly = true).data

    suspend fun getNowAndNext(): List<NowNext> = api.getNowAndNext().data

    /** Convenience: returns (current, next) per channel id. Channels
     *  missing from the now-next payload land as Pair(null, null). */
    suspend fun nowNextByChannel(): Map<String, Pair<NowNext?, NowNext?>> {
        val rows = try { api.getNowAndNext().data } catch (_: Exception) { emptyList() }
        // Server returns rows ordered by (channel_id, starts_at), so the
        // first per channel is the current program and the second is next.
        val grouped = rows.groupBy { it.channel_id }
        return grouped.mapValues { (_, list) ->
            val current = list.getOrNull(0)
            val next = list.getOrNull(1)
            current to next
        }
    }

    suspend fun getRecordings(status: String? = null): List<Recording> =
        api.getRecordings(status = status).data
}
