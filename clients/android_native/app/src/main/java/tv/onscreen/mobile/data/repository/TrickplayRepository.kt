package tv.onscreen.mobile.data.repository

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.TrickplayCue
import tv.onscreen.mobile.data.model.TrickplayStatus
import tv.onscreen.mobile.trickplay.TrickplayVtt
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
     *  silently falls back to no-preview seek. Parsing is delegated
     *  to [TrickplayVtt.parse] — pure module, fully unit-tested. */
    open suspend fun fetchCues(itemId: String): List<TrickplayCue>? {
        return try {
            val req = Request.Builder()
                .url("http://localhost/trickplay/$itemId/index.vtt")
                .build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return null
                val body = resp.body?.string() ?: return null
                TrickplayVtt.parse(body).takeIf { it.isNotEmpty() }
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

    // VTT parsing + cue-at-position lookup live in
    // [tv.onscreen.mobile.trickplay.TrickplayVtt] so the pure
    // string→List<TrickplayCue> mapping is unit-testable in the JVM
    // sourceset without mocking OkHttp or BitmapFactory.
}
