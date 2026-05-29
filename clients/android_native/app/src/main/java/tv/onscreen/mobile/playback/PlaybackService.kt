package tv.onscreen.mobile.playback

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
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
 * Background-audio session for music / audiobook / podcast playback.
 *
 * The service OWNS the ExoPlayer (it is not handed a screen-owned
 * player) and publishes a [MediaSession] so the OS surfaces lockscreen,
 * notification, Bluetooth, and Android Auto transport controls plus the
 * now-playing widget. The UI ([tv.onscreen.mobile.ui.player.PlayerScreen]
 * audio branch) drives playback through a `MediaController`; backing out
 * of the screen releases only the controller, so audio keeps playing.
 *
 * Why this and not the earlier handoff: the previous attempt parked a
 * screen-owned player into a process slot and called
 * `startForegroundService()` from `PlayerScreen`'s `onDispose`. Starting
 * an FGS while the screen tears down (app no longer reliably foreground)
 * throws `ForegroundServiceStartNotAllowedException` on Android 12+/14+.
 * Here Media3 promotes the service to a `mediaPlayback` foreground
 * service when playback begins — which is always a foreground tap — and
 * tears it down on stop. No manual FGS start.
 *
 * Active-item bookkeeping rides [MediaItem] metadata that the UI sets:
 * `mediaId` = OnScreen item id, and an extras [Bundle] carries
 * type/parentId/index so progress reporting + auto-advance work without
 * a re-fetch. Video never uses this path — picture-in-picture is its
 * "keep going outside the player" story.
 */
@UnstableApi
@AndroidEntryPoint
class PlaybackService : MediaSessionService() {

    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var prefs: ServerPrefs

    private var session: MediaSession? = null
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private var progressJob: Job? = null

    // Guards against double-publishing the terminal 'stopped' for one
    // item (STATE_ENDED can be followed by teardown). Reset when a new
    // item becomes current so replaying the same track reports again.
    private var reportedStoppedFor: String? = null

    companion object {
        // MediaItem metadata extras keys (set by the UI, read here).
        const val EXTRA_TYPE = "onscreen.type"
        const val EXTRA_PARENT_ID = "onscreen.parentId"
        const val EXTRA_INDEX = "onscreen.index"
    }

    override fun onCreate() {
        super.onCreate()
        val dsFactory = DefaultDataSource.Factory(this, DefaultHttpDataSource.Factory())
        val player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(dsFactory))
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(C.USAGE_MEDIA)
                    .setContentType(C.AUDIO_CONTENT_TYPE_MUSIC)
                    .build(),
                /* handleAudioFocus = */ true,
            )
            .setHandleAudioBecomingNoisy(true)
            .build()
        player.addListener(playerListener(player))
        session = MediaSession.Builder(this, player).build()
        startProgressReporter(player)
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    override fun onTaskRemoved(rootIntent: Intent?) {
        // Swiping the app away with nothing actively playing should not
        // leave a dangling session in the system controls.
        val player = session?.player
        if (player == null || !player.playWhenReady || player.mediaItemCount == 0) {
            stopSelf()
        }
    }

    override fun onDestroy() {
        reportStopped(session?.player)
        progressJob?.cancel()
        scope.cancel()
        session?.run {
            player.release()
            release()
        }
        session = null
        super.onDestroy()
    }

    private fun playerListener(player: ExoPlayer) = object : Player.Listener {
        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            // A new item is current — clear the stopped-guard so it (and
            // a future replay of the same track) can report on its own end.
            reportedStoppedFor = null
        }

        override fun onPlaybackStateChanged(state: Int) {
            if (state == Player.STATE_ENDED) {
                // The track finished: report it stopped (≈full duration →
                // scrobbles) before chaining, so the listen lands even if
                // the next resolve fails or the queue is exhausted.
                reportStopped(player)
                maybeAutoAdvance(player)
            }
        }
    }

    /** Periodic 'playing' heartbeat so the resume marker stays fresh
     *  while the user browses other screens with audio in the background. */
    private fun startProgressReporter(player: Player) {
        progressJob?.cancel()
        progressJob = scope.launch {
            while (isActive) {
                delay(10_000)
                if (!player.playWhenReady) continue
                val id = player.currentMediaItem?.mediaId ?: continue
                val dur = player.duration
                if (dur <= 0 || dur == C.TIME_UNSET) continue
                val pos = player.currentPosition.coerceAtLeast(0)
                try {
                    itemRepo.updateProgress(id, pos, dur, "playing")
                } catch (_: Exception) {
                    // Best-effort; the next tick retries.
                }
            }
        }
    }

    /** Publish the terminal 'stopped' for the current item — the
     *  server's scrobble trigger. Deduped per item id. */
    private fun reportStopped(player: Player?) {
        val item = player?.currentMediaItem ?: return
        val id = item.mediaId
        if (id.isEmpty() || id == reportedStoppedFor) return
        val dur = player.duration
        if (dur <= 0 || dur == C.TIME_UNSET) return
        val pos = player.currentPosition.coerceAtLeast(0)
        reportedStoppedFor = id
        scope.launch {
            try {
                itemRepo.updateProgress(id, pos, dur, "stopped")
            } catch (_: Exception) {
                // Best-effort.
            }
        }
    }

    /** On end-of-track for a track/audiobook, resolve the next sibling
     *  and chain to it so an album keeps playing in the background. */
    private fun maybeAutoAdvance(player: Player) {
        val item = player.currentMediaItem ?: return
        val md = item.mediaMetadata
        val type = md.extras?.getString(EXTRA_TYPE)
        if (type != "track" && type != "audiobook") return
        val itemId = item.mediaId.ifEmpty { return }
        val parentId = md.extras?.getString(EXTRA_PARENT_ID)
        val index = md.extras?.getInt(EXTRA_INDEX, -1)?.takeIf { it >= 0 }
        scope.launch {
            val next = NextSiblingResolver(itemRepo).resolve(itemId, type, parentId, index) ?: return@launch
            chainTo(next.id)
        }
    }

    private suspend fun chainTo(nextId: String) {
        val player = session?.player ?: return
        val item = try { itemRepo.getItem(nextId) } catch (_: Exception) { return }
        val file = item.files.firstOrNull() ?: return
        val url = buildDirectPlayUrl(file.stream_url, file.stream_token) ?: return

        val extras = Bundle().apply {
            putString(EXTRA_TYPE, item.type)
            item.parent_id?.let { putString(EXTRA_PARENT_ID, it) }
            item.index?.let { putInt(EXTRA_INDEX, it) }
        }
        val mediaItem = MediaItem.Builder()
            .setUri(Uri.parse(url))
            .setMediaId(nextId)
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(item.title)
                    .setExtras(extras)
                    .build(),
            )
            .build()
        player.setMediaItem(mediaItem)
        player.prepare()
        player.playWhenReady = true
    }

    /** Mirrors PlayerViewModel.buildDirectPlayUrl: prefer the per-file
     *  stream token, else the purpose=asset token (the access token is
     *  rejected in a query string by the asset-route middleware). */
    private suspend fun buildDirectPlayUrl(streamPath: String, streamToken: String?): String? {
        val server = prefs.getServerUrl()?.trimEnd('/') ?: return null
        val token = if (!streamToken.isNullOrEmpty()) streamToken else prefs.getAssetToken()
        val base = "$server$streamPath"
        if (token.isNullOrEmpty()) return base
        val sep = if (streamPath.contains("?")) "&" else "?"
        return "$base${sep}token=$token"
    }
}
