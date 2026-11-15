package tv.onscreen.android.ui.playback

import android.graphics.Bitmap
import androidx.leanback.widget.PlaybackSeekDataProvider
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.TrickplayCue
import tv.onscreen.android.data.repository.TrickplayRepository

/**
 * Backs Leanback's seek-bar with trickplay sprite thumbnails. Leanback
 * asks for a seek-position array up front (rendered as a strip of
 * thumbnail slots above the seek bar) and then lazily requests one
 * Bitmap per slot as the user scrubs into it.
 *
 * A sprite sheet is large — by default the server packs 10x10 tiles of
 * 320x180 (internal/trickplay/vtt.go), a 3200x1800 image — and Leanback asks for
 * neighbouring slots together, which mostly share one. So:
 *  - each sheet is downloaded and decoded once at a time: a slot asking for
 *    a sheet already on its way waits for that load ([sheetLoads]); it used
 *    to start its own, five to seven parallel downloads and decodes of the
 *    same sheet;
 *  - at most [MAX_SHEETS] decoded sheets are kept, least recently used
 *    recycled first ([sheetCache]); the cache used to be unbounded, ~8
 *    sheets (~184 MB as ARGB) after a scrub through a film, on a 1-2 GB
 *    stick. TrickplayRepository decodes them as RGB_565, half that per sheet.
 *
 * Main thread only: Leanback calls in on it, and [scope] (the view's) runs
 * there, so the caches need no locking and a sheet is never recycled while
 * a slot is being cropped from it.
 *
 * Positions are emitted in player-time (content-time minus the HLS
 * offset captured at session start) so Leanback's seek bar position
 * lookups match what ExoPlayer reports for `currentPosition`.
 */
class TrickplaySeekProvider(
    private val itemId: String,
    private val cues: List<TrickplayCue>,
    private val repo: TrickplayRepository,
    private val scope: CoroutineScope,
    private val hlsOffsetMs: Long,
) : PlaybackSeekDataProvider() {

    private val positionsArr: LongArray = cues
        .map { (it.startMs - hlsOffsetMs).coerceAtLeast(0L) }
        .toLongArray()

    /** Decoded sheets by file name, least recently used first. */
    private val sheetCache = LinkedHashMap<String, Bitmap>(MAX_SHEETS + 1, 0.75f, true)

    /** The one download + decode of a sheet in flight, by file name. */
    private val sheetLoads = mutableMapOf<String, Deferred<Bitmap?>>()

    private val activeJobs = mutableMapOf<Int, Job>()

    override fun getSeekPositions(): LongArray = positionsArr

    override fun getThumbnail(index: Int, callback: ResultCallback) {
        val cue = cues.getOrNull(index) ?: return
        // Cancel any in-flight load for the same slot — happens when
        // the user scrubs fast and Leanback re-requests positions
        // before the previous load resolves. (The sheet load it waits on
        // carries on for the other slots.)
        activeJobs[index]?.cancel()
        activeJobs[index] = scope.launch {
            try {
                val sheet = sheet(cue.file) ?: return@launch
                // Defensive bounds-check because a malformed VTT (or a
                // sprite-sheet resolution mismatch) would otherwise crash
                // the player.
                if (sheet.isRecycled ||
                    cue.x < 0 || cue.y < 0 || cue.x >= sheet.width || cue.y >= sheet.height
                ) {
                    return@launch
                }
                val w = cue.w.coerceAtMost(sheet.width - cue.x).coerceAtLeast(1)
                val h = cue.h.coerceAtMost(sheet.height - cue.y).coerceAtLeast(1)
                callback.onThumbnailLoaded(crop(sheet, cue.x, cue.y, w, h), index)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // Best-effort — a failed thumbnail just shows the
                // generic seek-bar placeholder for that slot.
            } finally {
                if (activeJobs[index] === coroutineContext[Job]) activeJobs.remove(index)
            }
        }
    }

    /** [file]'s decoded sheet: cached, or from the load already under way,
     *  or a new load. Null when it can't be fetched. */
    private suspend fun sheet(file: String): Bitmap? {
        sheetCache[file]?.let { if (!it.isRecycled) return it }
        // Not tied to the asking slot's job: cancelling that slot must not
        // abort the load other slots wait on.
        val load = sheetLoads.getOrPut(file) { scope.async { repo.fetchSprite(itemId, file) } }
        val bitmap = load.await()
        // The first waiter back files it; the rest find it filed.
        if (sheetLoads[file] === load) {
            sheetLoads.remove(file)
            if (bitmap != null) cache(file, bitmap)
        }
        return bitmap
    }

    private fun cache(file: String, bitmap: Bitmap) {
        sheetCache[file] = bitmap
        while (sheetCache.size > MAX_SHEETS) {
            val eldest = sheetCache.entries.iterator().next()
            sheetCache.remove(eldest.key)
            if (!eldest.value.isRecycled) eldest.value.recycle()
        }
    }

    /** The cue's tile as a bitmap of its own. createBitmap hands back the
     *  SOURCE itself when the region is all of it (a one-tile sheet), and a
     *  sheet is recycled on eviction while Leanback may still be showing the
     *  thumbnail, so that case is copied. */
    private fun crop(sheet: Bitmap, x: Int, y: Int, w: Int, h: Int): Bitmap =
        if (x == 0 && y == 0 && w == sheet.width && h == sheet.height) {
            sheet.copy(sheet.config ?: Bitmap.Config.RGB_565, false)
        } else {
            Bitmap.createBitmap(sheet, x, y, w, h)
        }

    override fun reset() {
        // Cancel everything in-flight; sheet cache is deliberately KEPT here so
        // a Leanback-internal reset (e.g. seek-bar resize) doesn't force a
        // re-download. Bitmaps are recycled in release() on fragment teardown.
        activeJobs.values.forEach { it.cancel() }
        activeJobs.clear()
    }

    /** Cancels in-flight loads and recycles the cached sprite-sheet bitmaps
     *  so their native pixel memory is reclaimed immediately rather than
     *  waiting for a GC pass — important on 1 GB TV boxes loading the next
     *  item's ExoPlayer buffers. Call from the host fragment's onDestroyView.
     *  Mirrors the phone client's recycleSpriteCache. */
    fun release() {
        activeJobs.values.forEach { it.cancel() }
        activeJobs.clear()
        sheetLoads.values.forEach { it.cancel() }
        sheetLoads.clear()
        sheetCache.values.forEach { if (!it.isRecycled) it.recycle() }
        sheetCache.clear()
    }

    internal companion object {
        /** Decoded sheets kept: the one being scrubbed through and the one
         *  before it, for scrubbing back across the boundary. */
        const val MAX_SHEETS = 2
    }
}
