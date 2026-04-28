package tv.onscreen.android.ui.playback

import android.graphics.Bitmap
import androidx.leanback.widget.PlaybackSeekDataProvider
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import tv.onscreen.android.data.model.TrickplayCue
import tv.onscreen.android.data.repository.TrickplayRepository

/**
 * Backs Leanback's seek-bar with trickplay sprite thumbnails. Leanback
 * asks for a seek-position array up front (rendered as a strip of
 * thumbnail slots above the seek bar) and then lazily requests one
 * Bitmap per slot as the user scrubs into it.
 *
 * The sprite-sheet JPEGs are cached in-memory keyed by filename so
 * adjacent cues that share a sheet don't re-download or re-decode.
 * For a typical 2 h movie at 10 s intervals (~720 cues, ~7 cues per
 * 320x180 sheet packed 8x12) this is ~60 sheets ≈ 30 MB cached —
 * acceptable for the duration of a single playback session, cleared
 * along with the provider on fragment teardown.
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

    private val sheetCache = mutableMapOf<String, Bitmap>()
    private val activeJobs = mutableMapOf<Int, Job>()

    override fun getSeekPositions(): LongArray = positionsArr

    override fun getThumbnail(index: Int, callback: ResultCallback) {
        val cue = cues.getOrNull(index) ?: return
        // Cancel any in-flight load for the same slot — happens when
        // the user scrubs fast and Leanback re-requests positions
        // before the previous load resolves.
        activeJobs[index]?.cancel()
        activeJobs[index] = scope.launch {
            try {
                val sheet = sheetCache[cue.file] ?: withContext(Dispatchers.IO) {
                    repo.fetchSprite(itemId, cue.file)
                }?.also { sheetCache[cue.file] = it } ?: return@launch
                // Crop the cue's sub-region. Defensive bounds-check
                // because a malformed VTT (or a sprite-sheet
                // resolution mismatch) would otherwise crash the
                // player.
                val w = cue.w.coerceAtMost(sheet.width - cue.x).coerceAtLeast(1)
                val h = cue.h.coerceAtMost(sheet.height - cue.y).coerceAtLeast(1)
                if (cue.x < 0 || cue.y < 0 || cue.x >= sheet.width || cue.y >= sheet.height) {
                    return@launch
                }
                val cropped = withContext(Dispatchers.Default) {
                    Bitmap.createBitmap(sheet, cue.x, cue.y, w, h)
                }
                callback.onThumbnailLoaded(cropped, index)
            } catch (_: Exception) {
                // Best-effort — a failed thumbnail just shows the
                // generic seek-bar placeholder for that slot.
            } finally {
                activeJobs.remove(index)
            }
        }
    }

    override fun reset() {
        // Cancel everything in-flight; sheet cache is cleared with
        // the provider since fragment teardown drops the only
        // reference. (Not nulled here so a Leanback-internal reset
        // — e.g. seek-bar resize — doesn't force a re-download.)
        activeJobs.values.forEach { it.cancel() }
        activeJobs.clear()
    }
}
