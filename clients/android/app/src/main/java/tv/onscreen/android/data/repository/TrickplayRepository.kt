package tv.onscreen.android.data.repository

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.TrickplayCue
import tv.onscreen.android.data.model.TrickplayStatus
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Trickplay = seek-bar thumbnail previews. The server generates a
 * WebVTT index pointing at sprite-sheet JPEGs (one PNG per N cues,
 * each cue cropping a subregion via #xywh=). The Leanback player
 * consumes this through [PlaybackSeekDataProvider]:
 * [getCues] → seek positions array; [fetchSprite] → bitmap to crop
 * per cue.
 *
 * The .vtt and sprite endpoints sit under `/trickplay/<id>/` on the
 * server (RequiredAllowQueryToken middleware). The injected
 * OkHttpClient already carries the BaseUrlInterceptor (rewrites
 * localhost → configured server) and AuthInterceptor (Bearer
 * header) — the same wiring the Retrofit JSON API uses — so
 * raw-bytes fetches authenticate identically without per-call
 * token plumbing.
 */
@Singleton
open class TrickplayRepository @Inject constructor(
    private val api: OnScreenApi,
    private val client: OkHttpClient,
) {
    /** GET /api/v1/items/{id}/trickplay. Best-effort: a server that
     *  doesn't have the trickplay handler wired returns 404, which
     *  we map to a `not_started`-status sentinel so the caller can
     *  uniformly check `status == "done"`. */
    open suspend fun status(itemId: String): TrickplayStatus =
        try { api.getTrickplayStatus(itemId).data }
        catch (_: Exception) { TrickplayStatus(status = "not_started") }

    /** Fetches /trickplay/{id}/index.vtt and parses out the cues.
     *  Returns null on any failure (network, parse) so the caller
     *  silently falls back to no-preview seek. */
    open suspend fun fetchCues(itemId: String): List<TrickplayCue>? {
        return try {
            val req = Request.Builder()
                .url("http://localhost/trickplay/$itemId/index.vtt")
                .build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return null
                val body = resp.body?.string() ?: return null
                parseVtt(body)
            }
        } catch (_: Exception) { null }
    }

    /** Fetches /trickplay/{id}/{file} and decodes to a Bitmap. The
     *  caller crops to the cue's x/y/w/h via Bitmap.createBitmap.
     *  Returns null on any failure. */
    open suspend fun fetchSprite(itemId: String, file: String): Bitmap? {
        return try {
            val req = Request.Builder()
                .url("http://localhost/trickplay/$itemId/$file")
                .build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return null
                val bytes = resp.body?.bytes() ?: return null
                BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
            }
        } catch (_: Exception) { null }
    }

    companion object {
        /** WebVTT trickplay parser. Cues look like:
         *
         *      00:00:10.000 --> 00:00:20.000
         *      sprite_000.jpg#xywh=0,0,320,180
         *
         *  Lines without a payload, malformed timecodes, or missing
         *  #xywh fragments are skipped silently — partial parse is
         *  preferable to bailing on the whole file because of one
         *  bad cue. */
        internal fun parseVtt(text: String): List<TrickplayCue> {
            val out = mutableListOf<TrickplayCue>()
            val lines = text.split('\n')
            var i = 0
            while (i < lines.size) {
                val line = lines[i].trim()
                val arrow = line.indexOf("-->")
                if (arrow < 0) { i++; continue }
                val start = parseVttTime(line.substring(0, arrow).trim())
                val end = parseVttTime(line.substring(arrow + 3).trim())
                val payload = lines.getOrNull(i + 1)?.trim().orEmpty()
                val hash = payload.indexOf("#xywh=")
                if (start < 0 || end < 0 || hash < 0) { i++; continue }
                val file = payload.substring(0, hash)
                val coords = payload.substring(hash + 6).split(',').mapNotNull { it.toIntOrNull() }
                if (coords.size < 4) { i++; continue }
                out += TrickplayCue(start, end, file, coords[0], coords[1], coords[2], coords[3])
                i += 2
            }
            return out
        }

        /** Accepts HH:MM:SS.mmm or MM:SS.mmm. Returns -1 on bad input. */
        private fun parseVttTime(s: String): Long {
            val m = Regex("""^(?:(\d+):)?(\d+):(\d+)(?:\.(\d+))?$""").matchEntire(s) ?: return -1
            val (hStr, mStr, secStr, msStr) = m.destructured
            val h = if (hStr.isEmpty()) 0L else hStr.toLong()
            val mi = mStr.toLong()
            val se = secStr.toLong()
            val ms = if (msStr.isEmpty()) 0L else msStr.padEnd(3, '0').substring(0, 3).toLong()
            return h * 3_600_000 + mi * 60_000 + se * 1_000 + ms
        }
    }
}
