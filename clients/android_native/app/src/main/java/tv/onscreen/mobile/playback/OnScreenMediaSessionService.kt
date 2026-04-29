package tv.onscreen.mobile.playback

import android.content.Intent
import android.net.Uri
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
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
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import javax.inject.Inject

/**
 * Media3 session service. Hosts the audio player when the user
 * navigates away from [tv.onscreen.mobile.ui.player.PlayerScreen] for
 * music — without this, backing out of the player kills the audio.
 *
 * Lifecycle:
 *  - PlayerScreen parks its audio player in [AudioHandoff] before its
 *    DisposableEffect releases it. The service starts itself foreground
 *    (Media3 manages the notification automatically when a session has
 *    playWhenReady=true) and keeps playing.
 *  - Re-entering PlayerScreen for a track that's already playing in
 *    the service takes the same player back via [AudioHandoff.take];
 *    PlayerScreen binds it to its PlayerView instead of building a
 *    fresh ExoPlayer, so playback is seamless.
 *  - Progress reporter ticks PUT /items/{id}/progress every 10 s so
 *    the resume marker stays fresh while the user is browsing other
 *    screens.
 *  - Auto-advance fires NextSiblingResolver on STATE_ENDED so an
 *    album played in the background still chains track-to-track.
 *
 * Video playback intentionally skips this path — picture-in-picture
 * is the right "keep watching outside the player" UX for video, and
 * the surface-view rendering doesn't translate to a service
 * notification.
 */
@UnstableApi
@AndroidEntryPoint
class OnScreenMediaSessionService : MediaSessionService() {

    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var prefs: ServerPrefs

    private var session: MediaSession? = null
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)

    private var activeItemId: String? = null
    private var activeItemType: String? = null
    private var activeParentId: String? = null
    private var activeIndex: Int? = null
    private var activeHlsOffsetMs: Long = 0L

    private var progressJob: Job? = null
    private var autoAdvanceListener: Player.Listener? = null

    override fun onCreate() {
        super.onCreate()
        AudioHandoff.peek()?.let { attach(it) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
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
     *  by this service. Called by PlayerScreen when it's about to be
     *  disposed but the user is still listening to music. */
    fun attach(player: ExoPlayer) {
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

    fun detach(): ExoPlayer? {
        val p = session?.player as? ExoPlayer ?: return null
        detachInternal()
        stopSelf()
        return p
    }

    fun hasActivePlayer(): Boolean = session?.player != null

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

    private suspend fun chainTo(itemId: String) {
        val player = session?.player as? ExoPlayer ?: return
        val item = try { itemRepo.getItem(itemId) } catch (_: Exception) { return }
        val file = item.files.firstOrNull() ?: return
        val server = prefs.getServerUrl()?.trimEnd('/').orEmpty()
        if (server.isEmpty()) return
        val token = file.stream_token ?: prefs.getAccessToken().orEmpty()
        if (token.isEmpty()) return

        // Music files always direct-play — PlaybackHelper.decide()
        // would short-circuit to DirectPlay for the same input, so
        // skip the remux/transcode branch here.
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
