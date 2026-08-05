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
        // Server returns rows ordered by (channel_id, starts_at) and filters
        // on ends_at > now — so when the guide has a GAP (nothing airing
        // right now, next show at 20:00), the first row is a FUTURE program.
        // Assuming index 0 is "on now" mislabeled it; verify it has actually
        // started before treating it as current.
        val now = System.currentTimeMillis()
        val grouped = rows.groupBy { it.channel_id }
        return grouped.mapValues { (_, list) ->
            val first = list.getOrNull(0)
            val firstStarted = first != null && (parseIso(first.starts_at)?.let { it <= now } ?: true)
            if (firstStarted) {
                first to list.getOrNull(1)
            } else {
                // Guide gap: nothing on now; the first row is what's NEXT.
                null to first
            }
        }
    }

    private fun parseIso(ts: String): Long? = try {
        java.time.OffsetDateTime.parse(ts).toInstant().toEpochMilli()
    } catch (_: Exception) {
        null
    }

    suspend fun getRecordings(status: String? = null): List<Recording> =
        api.getRecordings(status = status).data
}
