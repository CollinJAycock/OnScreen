package tv.onscreen.android.playback

import android.content.Intent
import android.net.Uri
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.TranscodeRepository
import javax.inject.Inject

/**
 * Media3 session service. Hosts the audio player when the user
 * navigates away from the PlaybackFragment for music — without
 * this, backing out of the player kills the audio.
 *
 * Lifecycle:
 *  - PlaybackFragment hands its audio player to the service via
 *    [attach] before navigating away. The service starts itself
 *    foreground (Media3 manages the notification automatically when
 *    a session has playWhenReady=true) and keeps playing.
 *  - When playback ends or pauses past the auto-stop window, the
 *    service stops itself. The Media3 framework also surfaces
 *    play/pause/skip on the system media-session rail (Bluetooth
 *    headphones, lockscreen, Watch Next, Now Playing on Android
 *    Auto if the device exposes one).
 *  - Re-entering PlaybackFragment for a track that's already
 *    playing in the service returns the same player instance via
 *    [activePlayer]; the fragment binds it to the Leanback glue
 *    instead of building a fresh ExoPlayer, so playback is
 *    seamless.
 *
 * Video playback intentionally skips this path — the floating PiP
 * window is the right "keep watching outside the player" UX for
 * video, and the surface-view rendering doesn't translate to a
 * service notification.
 */
@UnstableApi
@AndroidEntryPoint
class OnScreenMediaSessionService : MediaSessionService() {

    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var transcodeRepo: TranscodeRepository
    @Inject lateinit var prefs: ServerPrefs

    private var session: MediaSession? = null
    /** Service-scoped coroutine scope. Cancelled in onDestroy so the
     *  progress reporter + auto-advance jobs don't leak past the
     *  service's lifetime. */
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    /** Active item id of the player parked here, used by the progress
     *  reporter to address PUT /items/{id}/progress and by the
     *  auto-advance listener to compute the next-sibling lookup. */
    private var activeItemId: String? = null
    /** Active item type (track / episode / etc.) — needed for the
     *  next-sibling lookup. */
    private var activeItemType: String? = null
    /** Active item parent_id + index for the next-sibling lookup. */
    private var activeParentId: String? = null
    private var activeIndex: Int? = null
    /** HLS offset captured from the active transcode session, if any.
     *  Without it, progress reports for HLS streams send player time
     *  (offset from the segment start) instead of content time. */
    private var activeHlsOffsetMs: Long = 0L
    /** Coroutine ticking PUT /items/{id}/progress every 10 s while
     *  the service-owned player is playing. */
    private var progressJob: Job? = null
    /** Player.Listener that handles auto-advance on STATE_ENDED.
     *  Stored so detach() can remove it. */
    private var autoAdvanceListener: Player.Listener? = null

    override fun onCreate() {
        super.onCreate()
        // Pick up the player PlaybackFragment parked in the
        // process-wide AudioHandoff slot. If the service was
        // resurrected by the system without a fresh handoff (e.g. a
        // Bluetooth media-button press after the app was swiped
        // away) peek() returns null and we have nothing to bind
        // until the next attach() call.
        AudioHandoff.peek()?.let { attach(it) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // Re-check the handoff slot in case the service was already
        // alive when the fragment parked a fresh player — the
        // onCreate hook only fires once per process lifetime.
        AudioHandoff.peek()?.let { p ->
            if (session?.player !== p) attach(p)
        }
        return super.onStartCommand(intent, flags, startId)
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    override fun onTaskRemoved(rootIntent: Intent?) {
        // When the user swipes the app away from the recents tray
        // we release everything — there's nothing to bring back to
        // the foreground for, and dangling media sessions clutter
        // the system controls.
        val player = session?.player
        if (player != null && (!player.playWhenReady || player.mediaItemCount == 0)) {
            stopSelf()
        }
    }

    /** Bind an externally-owned ExoPlayer to a MediaSession exposed
     *  by this service. Called by PlaybackFragment when it's about
     *  to be destroyed but the user is still listening to music.
     *  AudioHandoff.peek() carries the parked player's metadata
     *  alongside it; we read those alongside the player so the
     *  service can address progress reports and auto-advance
     *  lookups without having to re-fetch the item. */
    fun attach(player: ExoPlayer) {
        // Tear down anything from a previous attach (player release
        // is the caller's responsibility — they parked it; we just
        // detach + release the session wrapper).
        detachInternal()

        // Audio focus + becoming-noisy handling: Media3's defaults
        // cover both, so no AudioManager.OnAudioFocusChangeListener
        // wiring needed.
        player.setAudioAttributes(
            AudioAttributes.Builder()
                .setUsage(C.USAGE_MEDIA)
                .setContentType(C.AUDIO_CONTENT_TYPE_MUSIC)
                .build(),
            /* handleAudioFocus = */ true,
        )
        session = MediaSession.Builder(this, player).build()

        // Pull the metadata snapshot the fragment captured at park
        // time. Without this the service can publish progress but
        // can't address WHICH item — and would have no way to look
        // up next-sibling for the auto-advance.
        val meta = AudioHandoff.peekMetadata()
        if (meta != null) {
            activeItemId = meta.itemId
            activeItemType = meta.itemType
            activeParentId = meta.parentId
            activeIndex = meta.index
            activeHlsOffsetMs = meta.hlsOffsetMs
        }

        startProgressReporter(player)
        installAutoAdvance(player)
    }

    /** Detach and return the active player. Called when
     *  PlaybackFragment is re-entering for the same item — the
     *  fragment takes the player back, attaches it to its Leanback
     *  glue, and the service shuts down (no second player needed). */
    fun detach(): ExoPlayer? {
        val p = session?.player as? ExoPlayer ?: return null
        detachInternal()
        // Don't release the player — the caller now owns it.
        stopSelf()
        return p
    }

    /** Whether a player is currently parked in the service. The
     *  fragment uses this on entry to decide between "rebind to the
     *  service's player" vs "build a fresh ExoPlayer". */
    fun hasActivePlayer(): Boolean = session?.player != null

    /** Tear down the session, listener, and progress job without
     *  releasing the player itself. Used by both detach() (player
     *  ownership returns to the fragment) and the next-attach path
     *  (we own the new player; the previous one's session was
     *  already released by the caller via AudioHandoff.park). */
    private fun detachInternal() {
        progressJob?.cancel()
        progressJob = null
        autoAdvanceListener?.let { listener ->
            session?.player?.removeListener(listener)
        }
        autoAdvanceListener = null
        session?.release()
        session = null
        activeItemId = null
        activeItemType = null
        activeParentId = null
        activeIndex = null
        activeHlsOffsetMs = 0L
    }

    /** Tick PUT /items/{id}/progress every 10 s while the
     *  service-owned player is playing. Same cadence as
     *  ProgressTracker on the fragment side — without this the
     *  resume marker would freeze the moment the user navigates
     *  away from the player. */
    private fun startProgressReporter(player: ExoPlayer) {
        progressJob?.cancel()
        progressJob = scope.launch {
            while (isActive) {
                delay(10_000)
                val itemId = activeItemId ?: continue
                if (!player.playWhenReady) continue
                val dur = player.duration
                if (dur <= 0 || dur == Long.MAX_VALUE) continue
                val pos = player.currentPosition + activeHlsOffsetMs
                try {
                    itemRepo.updateProgress(itemId, pos, dur, "playing")
                } catch (_: Exception) {
                    // Best-effort; the next tick will retry.
                }
            }
        }
    }

    /** On STATE_ENDED for music tracks, walk to the next sibling
     *  via NextSiblingResolver and start that item playing. Mirrors
     *  the fragment-side auto-advance so navigating away mid-album
     *  doesn't kill the chain. Episodes intentionally skip this
     *  path on the service — the fragment side surfaces an Up Next
     *  overlay for episodes (visual chrome we can't render in the
     *  service notification), so silent-chain auto-advance for
     *  episodes is fragment-only. */
    private fun installAutoAdvance(player: ExoPlayer) {
        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                if (state != Player.STATE_ENDED) return
                if (activeItemType != "track" && activeItemType != "audiobook") return
                val resolver = NextSiblingResolver(itemRepo)
                val itemId = activeItemId ?: return
                val type = activeItemType ?: return
                val parentId = activeParentId
                val index = activeIndex
                scope.launch {
                    val next = resolver.resolve(itemId, type, parentId, index) ?: return@launch
                    chainTo(next.id)
                }
            }
        }
        player.addListener(listener)
        autoAdvanceListener = listener
    }

    /** Switch the service-owned player to a different item id.
     *  Re-resolves the file URL via the item endpoint and the same
     *  per-file stream-token machinery the fragment uses, then
     *  swaps the MediaSource. */
    private suspend fun chainTo(itemId: String) {
        val player = session?.player as? ExoPlayer ?: return
        val item = try { itemRepo.getItem(itemId) } catch (_: Exception) { return }
        val file = item.files.firstOrNull() ?: return
        val server = prefs.getServerUrl()?.trimEnd('/').orEmpty()
        if (server.isEmpty()) return
        val token = file.stream_token ?: prefs.getAccessToken().orEmpty()
        if (token.isEmpty()) return

        // Audio direct-play URL — the service-side auto-advance is
        // music-only, and music files are uniformly direct-playable
        // (no transcode negotiation). The fragment's PlaybackHelper
        // would short-circuit to DirectPlay for the same input.
        val sep = if (file.stream_url.contains("?")) "&" else "?"
        val url = "$server${file.stream_url}${sep}token=$token"

        activeItemId = itemId
        activeItemType = item.type
        activeParentId = item.parent_id
        activeIndex = item.index
        activeHlsOffsetMs = 0L

        player.setMediaItem(MediaItem.fromUri(Uri.parse(url)))
        player.prepare()
        player.playWhenReady = true
    }

    override fun onDestroy() {
        scope.cancel()
        session?.run {
            player.release()
            release()
        }
        session = null
        AudioHandoff.clear()
        super.onDestroy()
    }
}
