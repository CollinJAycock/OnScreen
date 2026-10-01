package tv.onscreen.android.playback

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.util.Log
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import tv.onscreen.android.ui.playback.PlaybackHelper

/**
 * Process-wide handoff slot for an ExoPlayer that's transitioning
 * between PlaybackFragment ownership and the MediaSessionService.
 *
 * Why a singleton rather than a proper bound-service binder: Media3's
 * MediaSessionService only exposes a SessionToken-based MediaController
 * connection, not the raw player. To hand a fragment-built player to
 * the service we need a side channel, and a tightly-scoped object is
 * cheaper than threading the ExoPlayer through Hilt as a Singleton
 * (which would force every PlaybackFragment to share one player
 * instance even when there's no audio in flight).
 *
 * The slot holds zero or one player. Parking a second player while
 * one is already parked releases the old one — the user who starts
 * a fresh track expects the previous one to stop, not pile up.
 *
 * Every park gets a new [generation]. The service records the one it
 * attached, so it can tell its own player from the same player parked
 * again since (taken back by a fragment and handed over anew), which a
 * new start of the service will pick up instead.
 */
object AudioHandoff {

    /** Snapshot of the parked item so the service can address
     *  progress reports + auto-advance lookups without re-fetching
     *  the item over the network. Captured at park time on the
     *  fragment side from the same state the player is bound to. */
    data class Metadata(
        val itemId: String,
        val itemType: String,
        val parentId: String?,
        val index: Int?,
        val hlsOffsetMs: Long,
        /** The length listed for the item (the file's, else the item's
         *  runtime: PlaybackHelper.listedDurationMs), carried so the
         *  service's progress reporter can absolutise like the fragment does.
         *  A resumed HLS session's player.duration is only the REMAINING
         *  time, so pairing it with an offset-corrected position reported
         *  e.g. 3 h against a 5 h-remaining duration — crossing the
         *  server's watched threshold hours early. Null when unknown. */
        val itemDurationMs: Long? = null,
        /** The server session the parked player streams from (a transcoded
         *  or remuxed track), or null on direct play. It travels with the
         *  player: see [StreamSession]. */
        val session: StreamSession? = null,
        /** What the background media session names as playing. */
        val nowPlaying: NowPlaying? = null,
    )

    /** How long a park waits before starting the service. A fragment
     *  replaced by another for the same item takes the player back within
     *  a few hundred milliseconds; starting and stopping the service inside
     *  that window crashed the app (see [startServiceWhenSettled]). */
    const val SERVICE_START_DELAY_MS = 1_000L

    private const val TAG = "AudioHandoff"

    private var parked: ExoPlayer? = null
    private var parkedMeta: Metadata? = null
    private var generation = 0L

    private val mainHandler by lazy { Handler(Looper.getMainLooper()) }

    /** Ends the server session of a player released here. OnScreenApp sets
     *  it at startup to the transcode repository's detached stop, which
     *  outlives whoever let the player go. Unset (tests, or before startup),
     *  nothing is sent and the server reaps the session when it idles out. */
    @Volatile
    var sessionEnder: ((StreamSession) -> Unit)? = null

    /** Reports the final position of a player released here, as
     *  (item, content position ms, content duration ms). OnScreenApp sets
     *  it at startup to a detached 'stopped' progress report. Unset, the
     *  last heartbeat (at most 10 s old) stands. */
    @Volatile
    var releaseReporter: ((Metadata, Long, Long) -> Unit)? = null

    private fun end(session: StreamSession?) {
        session?.let { s -> sessionEnder?.invoke(s) }
    }

    /** Release a player leaving the slot for good: report where it got to,
     *  release it, and end its server session. Main thread. */
    private fun releaseOut(player: ExoPlayer, meta: Metadata?) {
        if (meta != null) {
            runCatching {
                if (player.playbackState != Player.STATE_ENDED) {
                    val pos = player.currentPosition + meta.hlsOffsetMs
                    val dur = PlaybackHelper.contentDurationMs(
                        listedDurationMs = meta.itemDurationMs,
                        playerDurationMs = player.duration,
                        hlsOffsetMs = meta.hlsOffsetMs,
                        hlsSession = meta.session != null,
                        playerWindowDynamic = player.isCurrentMediaItemDynamic,
                    )
                    if (pos > 0L && dur > 0L) releaseReporter?.invoke(meta, pos.coerceAtMost(dur), dur)
                }
            }
        }
        runCatching {
            player.stop()
            player.release()
        }
        end(meta?.session)
    }

    /**
     * Whether [player] has anything for the service to play, so is worth
     * parking at all. Not when it finished: ExoPlayer keeps playWhenReady
     * at STATE_ENDED, so the pause test below can't catch the end of a
     * stream. Not when an error stopped it or it never loaded (STATE_IDLE,
     * playWhenReady often still true): parked, the service put up a
     * "playing" notification and sent heartbeats for audio that wasn't
     * playing. Not when paused before it ever began.
     */
    fun parkable(player: Player): Boolean = when {
        player.playbackState == Player.STATE_ENDED -> false
        player.playbackState == Player.STATE_IDLE || player.playerError != null -> false
        !player.playWhenReady && player.currentPosition == 0L -> false
        else -> true
    }

    /** Park a player for pickup by the MediaSessionService and return this
     *  park's generation. A different player still parked is released,
     *  with its final position reported and its server session ended. */
    @Synchronized
    fun park(player: ExoPlayer, meta: Metadata): Long {
        val evicted = parked?.takeIf { it !== player }
        if (evicted != null) releaseOut(evicted, parkedMeta)
        parked = player
        parkedMeta = meta
        generation++
        return generation
    }

    /** The generation of the latest park. */
    @Synchronized
    fun generation(): Long = generation

    /** Whether the park that returned [gen] still holds the slot: nothing
     *  took its player back and nothing was parked since. */
    @Synchronized
    fun isParked(gen: Long): Boolean = parked != null && generation == gen

    /** Whether [player], attached from the park that returned [gen], still
     *  belongs to whoever attached it: the slot holds that very park. False
     *  once a fragment took the player back, or parked it again since. */
    @Synchronized
    fun owns(player: ExoPlayer, gen: Long): Boolean = parked === player && generation == gen

    /** Take the parked player out of the slot. Returns null if none
     *  is currently parked, or if [forItemId] doesn't match — the
     *  caller is asking for a specific item and the parked one is
     *  for something else, so it should keep playing in the service
     *  while the new fragment builds its own player. */
    @Synchronized
    fun take(forItemId: String): ExoPlayer? {
        if (parkedMeta?.itemId != forItemId) return null
        val p = parked
        parked = null
        parkedMeta = null
        return p
    }

    /** Refresh the metadata for the already-parked player. The service
     *  calls this when it auto-advances to a new track in the
     *  background, so a fragment reclaiming the player afterward binds
     *  to the track that's actually playing rather than the one
     *  originally parked. No-op when nothing is parked. */
    @Synchronized
    fun updateMetadata(meta: Metadata) {
        if (parked != null) parkedMeta = meta
    }

    /** Inspection without removal — used by the service to decide
     *  whether to bind a session on first start. */
    @Synchronized
    fun peek(): ExoPlayer? = parked

    /** Metadata snapshot that goes alongside the parked player.
     *  Read by the service immediately after attach() so the
     *  progress reporter knows which item to PUT against and the
     *  auto-advance listener can compute the next sibling. */
    @Synchronized
    fun peekMetadata(): Metadata? = parkedMeta

    /** Unconditional clear — used by the service when it's about
     *  to release the player itself (playback ended, user dismissed
     *  notification). */
    @Synchronized
    fun clear() {
        parked = null
        parkedMeta = null
    }

    /**
     * Start the background service for the park that returned [gen], a
     * moment from now and only if that park still holds the slot.
     *
     * A fragment replaced by another for the same item (a deep link to the
     * item on screen, a Watch Next tile) parks the player and has it taken
     * back a few hundred milliseconds later. Starting the service there
     * made Media3 schedule its move to the foreground; stopping the service
     * before that ran left Media3 to restart it in "must go foreground"
     * mode with nobody to do so, and Android killed the app ten seconds
     * later. The player keeps playing through the delay without a service.
     */
    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    fun startServiceWhenSettled(context: Context, gen: Long) {
        val app = context.applicationContext
        mainHandler.postDelayed({
            if (!isParked(gen)) return@postDelayed
            try {
                app.startService(Intent(app, OnScreenMediaSessionService::class.java))
            } catch (e: Exception) {
                // Android refused the start (background start limits): nothing
                // can keep this player going in the background, so let it go
                // rather than leave it playing with no session or heartbeat.
                Log.w(TAG, "background service start refused; releasing the parked player", e)
                releaseIfParked(gen)
            }
        }, SERVICE_START_DELAY_MS)
    }

    /** Release the player of the park that returned [gen], if it still
     *  holds the slot. Main thread. */
    @Synchronized
    fun releaseIfParked(gen: Long) {
        if (!isParked(gen)) return
        val p = parked ?: return
        releaseOut(p, parkedMeta)
        parked = null
        parkedMeta = null
    }

    /** Stop and release any parked background audio and stop the session
     *  service. Used on every sign-out (voluntary and involuntary): a parked
     *  player otherwise keeps streaming on the signed-out user's already-issued
     *  stream token, and its MediaSession keeps advertising their track. Also
     *  used when a new item starts while another is parked, whether or not
     *  the service has started yet. Release the parked player first so the
     *  service's own teardown has nothing left to hand back, then stop the
     *  service. Idempotent. Must be called on the main thread (ExoPlayer is
     *  main-thread bound). */
    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    fun stopAll(context: Context) {
        synchronized(this) {
            // Released here, so its session ends here: the service's onDestroy
            // finds the slot empty and takes the player to be someone else's.
            // After an involuntary sign-out the stop is refused (it needs the
            // user's credentials) and the server's idle timeout reaps it.
            parked?.let { releaseOut(it, parkedMeta) }
            parked = null
            parkedMeta = null
        }
        runCatching {
            context.stopService(Intent(context, OnScreenMediaSessionService::class.java))
        }
    }
}
