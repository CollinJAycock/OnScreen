package tv.onscreen.android.playback

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.session.CommandButton
import androidx.media3.session.DefaultMediaNotificationProvider
import androidx.media3.session.MediaNotification
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.ListenableFuture
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.android.data.api.HeartbeatRefusal
import tv.onscreen.android.data.api.PlaybackStop
import tv.onscreen.android.data.device.ClientName
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.AudiobookRepository
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.NotificationsRepository
import tv.onscreen.android.data.repository.TranscodeRepository
import tv.onscreen.android.ui.playback.PlaybackHelper
import tv.onscreen.android.ui.playback.PlaybackMode
import javax.inject.Inject

/**
 * Media3 session service. Hosts the audio player when the user
 * navigates away from the PlaybackFragment for music or an audiobook
 * (a book, or one of a multi-file book's chapters — see
 * [AudioItemTypes]) — without this, backing out of the player kills
 * the audio.
 *
 * Lifecycle:
 *  - PlaybackFragment parks its audio player in [AudioHandoff] and, a
 *    moment later, starts this service ([AudioHandoff.startServiceWhenSettled]),
 *    which [attach]es it. Media3 moves the service to the foreground while
 *    the session plays; a paused player keeps it there for
 *    [BackgroundPause.HOLD_MS], then the service releases it.
 *  - When the queue runs out (the last track or chapter ends), or the pause
 *    window passes, the service stops itself and releases the player. The
 *    Media3 framework also surfaces play/pause/skip on the system
 *    media-session rail (Bluetooth headphones, lockscreen, Now Playing).
 *  - Re-entering PlaybackFragment for a track that's already playing in
 *    the service takes the same player back through [AudioHandoff.take];
 *    the fragment binds it to the Leanback glue instead of building a
 *    fresh ExoPlayer, so playback is seamless.
 *
 * Video playback intentionally skips this path — the surface-view
 * rendering doesn't translate to a service notification, so video
 * is released when the activity stops rather than handed off here.
 *
 * Audiobook listening speed needs no hand-off of its own: the parked
 * player is the fragment's instance, and ExoPlayer keeps its
 * PlaybackParameters. Only a chain to another book, or out of books,
 * re-decides it ([applyListeningSpeed]); a chain to the book's next
 * chapter keeps it.
 */
@UnstableApi
@AndroidEntryPoint
class OnScreenMediaSessionService : MediaSessionService() {

    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var transcodeRepo: TranscodeRepository
    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var audiobooks: AudiobookRepository
    @Inject lateinit var notifications: NotificationsRepository
    @Inject lateinit var clientName: ClientName

    private var session: MediaSession? = null
    /** The ExoPlayer this service drives. The session wraps it in a
     *  [SessionPlayer], so it is kept here rather than read back from the
     *  session. */
    private var activePlayer: ExoPlayer? = null
    /** The [AudioHandoff] park [activePlayer] came from. The same player
     *  parked again since (a fragment took it back and handed it over anew)
     *  has a newer generation and belongs to a new start of the service. */
    private var attachedGeneration: Long = -1L
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

    /** The active item's authoritative duration, from the handoff metadata.
     *  Needed because a resumed HLS session's player.duration is only the
     *  REMAINING time — pairing it with an offset-corrected position marked
     *  items watched hours early. See PlaybackHelper.contentDurationMs. */
    private var activeItemDurationMs: Long? = null
    /** What the session names as playing (see [SessionPlayer]). */
    private var activeNowPlaying: NowPlaying? = null
    /** The book the player's speed belongs to (null: not a book, so 1×).
     *  A chain to another chapter of it leaves the speed alone. */
    private var speedBookId: String? = null
    /** The server session the player streams from (a transcoded or remuxed
     *  track), or null on direct play. Parked with the player; this service
     *  ends it when it lets the player go (release, or a chain to another
     *  item). See [StreamSession]. */
    private var activeStreamSession: StreamSession? = null
    /** Coroutine ticking PUT /items/{id}/progress every 10 s while
     *  the service-owned player is playing. */
    private var progressJob: Job? = null
    /** Listener for the player's end (auto-advance) and pauses. Stored so
     *  detach can remove it. */
    private var playerListener: Player.Listener? = null
    /** When the player was paused (elapsedRealtime), or null while it
     *  plays. See [BackgroundPause]. */
    private var pausedAtMs: Long? = null
    /** Releases the player once it has been paused for
     *  [BackgroundPause.HOLD_MS]. */
    private var pauseReleaseJob: Job? = null
    /** Obeys an admin "stop this stream" aimed at the player here (see
     *  [watchAdminStops]). */
    private var adminStopJob: Job? = null

    private val mainHandler = Handler(Looper.getMainLooper())

    override fun onCreate() {
        super.onCreate()
        setMediaNotificationProvider(
            HoldingNotificationProvider(DefaultMediaNotificationProvider.Builder(applicationContext).build()),
        )
        // Pick up the player PlaybackFragment parked in the
        // process-wide AudioHandoff slot. If the service was
        // resurrected by the system without a fresh handoff (e.g. a
        // Bluetooth media-button press after the app was swiped
        // away) peek() returns null and there is nothing to bind;
        // onStartCommand then stops the service.
        AudioHandoff.peek()?.let { attach(it) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // Re-check the handoff slot in case the service was already
        // alive when the fragment parked a fresh player — the
        // onCreate hook only fires once per process lifetime.
        // The same player parked again (taken back by a fragment and handed
        // over anew) is a new park too: re-attach so this service owns it.
        AudioHandoff.peek()?.let { p ->
            if (activePlayer !== p || AudioHandoff.generation() != attachedGeneration) attach(p)
        }
        val result = super.onStartCommand(intent, flags, startId)
        if (activePlayer == null) settleStrayStart(startId)
        return result
    }

    /**
     * A start with nothing to play: a player taken back just before its
     * start arrived, or Media3 restarting a service already stopped (in
     * Media3 1.3 a notification update scheduled before the stop still runs
     * afterwards and calls startForegroundService). A service started that
     * way must call startForeground within seconds or Android kills the
     * app, so go to the foreground briefly with a silent notification, then
     * stop. A plain start that isn't allowed to go to the foreground just
     * stops. (Media3 1.10+ settles its own stale restarts the same way before
     * this runs; the taken-back case is still only ours.)
     */
    private fun settleStrayStart(startId: Int) {
        runCatching {
            val manager = getSystemService(NotificationManager::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && manager != null) {
                manager.createNotificationChannel(
                    NotificationChannel(STRAY_CHANNEL_ID, "Playback", NotificationManager.IMPORTANCE_LOW),
                )
            }
            val notification = NotificationCompat.Builder(this, STRAY_CHANNEL_ID)
                .setSmallIcon(android.R.drawable.ic_media_play)
                .setContentTitle("OnScreen")
                .setSilent(true)
                .build()
            ServiceCompat.startForeground(
                this,
                STRAY_NOTIFICATION_ID,
                notification,
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                    ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK
                } else {
                    0
                },
            )
        }
        runCatching { ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE) }
        stopSelf(startId)
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    /** Keep the service in the foreground for a paused player too, for
     *  [BackgroundPause.HOLD_MS]: Media3 drops it from the foreground on a
     *  pause, and Android then stops the idle background service within a
     *  minute, releasing a player the listener meant to resume.
     *
     *  Since Media3 1.6 a player that pauses while the service holds it stays
     *  in the foreground for 10 minutes by itself, but one parked already
     *  paused (paused, then HOME) never played here and gets no such grace,
     *  so the hold stays. It sits on the async variant: Media3's default
     *  path now takes the flag passed here and ignores the one handed on to
     *  super.onUpdateNotification, so overriding that one (as under 1.3)
     *  would silently drop the hold. */
    override fun onUpdateNotificationAsync(
        session: MediaSession,
        startInForegroundRequired: Boolean,
    ): ListenableFuture<Void?> =
        super.onUpdateNotificationAsync(session, startInForegroundRequired || holdsWhilePaused())

    /**
     * Media3's default notification, with one change. A new session decodes
     * the cover art off-thread; when the decode finishes, Media3 rebuilds
     * the notification and decides the foreground from the player alone,
     * which takes a paused player out of it and undoes the hold above (a
     * book paused, then HOME, was dropped a minute later). Re-assert the
     * hold after that update: the art is cached by then, so the rebuild is
     * immediate and doesn't come back here.
     */
    private inner class HoldingNotificationProvider(
        private val base: MediaNotification.Provider,
    ) : MediaNotification.Provider {
        override fun createNotification(
            session: MediaSession,
            mediaButtonPreferences: ImmutableList<CommandButton>,
            actionFactory: MediaNotification.ActionFactory,
            onNotificationChangedCallback: MediaNotification.Provider.Callback,
        ): MediaNotification = base.createNotification(session, mediaButtonPreferences, actionFactory) { notification ->
            onNotificationChangedCallback.onNotificationChanged(notification)
            if (holdsWhilePaused()) {
                mainHandler.post {
                    if (session !== this@OnScreenMediaSessionService.session || !holdsWhilePaused()) return@post
                    // Back through onUpdateNotificationAsync, which applies the
                    // hold. Android 12+ may refuse the foreground start once the
                    // app's allowance has run out; Media3 catches that on this
                    // path itself. The hold is lost then: Android stops the
                    // service later and teardown releases the player.
                    triggerNotificationUpdate()
                }
            }
        }

        override fun handleCustomCommand(session: MediaSession, action: String, extras: Bundle): Boolean =
            base.handleCustomCommand(session, action, extras)

        override fun getNotificationChannelInfo(): MediaNotification.Provider.NotificationChannelInfo =
            base.notificationChannelInfo
    }

    private fun holdsWhilePaused(): Boolean {
        val p = activePlayer ?: return false
        return BackgroundPause.holdsForeground(p.playWhenReady, p.playbackState, pausedAtMs, SystemClock.elapsedRealtime())
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        // When the user swipes the app away from the recents tray we release
        // everything — there's nothing to bring back to the foreground for.
        // Stop UNCONDITIONALLY: the old guard skipped stopSelf() while actively
        // playing, leaving a foreground MediaSession service running with no
        // in-app UI to stop it (only the system media controls), which is the
        // dangling-service the swipe-away was meant to clean up.
        // The listener goes first: its pause report would race the final
        // 'stopped' teardown sends, and could land after it.
        playerListener?.let { l -> activePlayer?.removeListener(l) }
        playerListener = null
        activePlayer?.pause()
        releaseAndStop()
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
        // wiring needed. The fragment already plays audio items with
        // these attributes, so this doesn't touch the audio output.
        player.setAudioAttributes(AudioItemTypes.MUSIC_ATTRIBUTES, /* handleAudioFocus = */ true)
        activePlayer = player
        attachedGeneration = AudioHandoff.generation()

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
            activeItemDurationMs = meta.itemDurationMs
            activeStreamSession = meta.session
            activeNowPlaying = meta.nowPlaying
            // The fragment put the player at this book's speed.
            speedBookId = AudiobookSpeed.bookIdOf(meta.itemType, meta.itemId, meta.parentId)
        }
        pausedAtMs = null

        val sessionPlayer = SessionPlayer(
            player,
            offsetMs = { activeHlsOffsetMs },
            itemDurationMs = { activeItemDurationMs },
            nowPlaying = { activeNowPlaying },
        )
        // Full access for the platform's controllers (the assistant, the
        // system's media controls), which Media3 1.11 would otherwise leave
        // read-only whenever it can't vouch for them.
        val newSession = MediaSession.Builder(this, sessionPlayer)
            .setCallback(FullAccessSessionCallback())
            .build()
        session = newSession
        // Register the session with the service. onGetSession only fires
        // when a *controller* connects, but the park/handoff model never
        // connects one — so without an explicit addSession the service's
        // MediaNotificationManager never tracks the session, never posts
        // the media notification, and never calls startForeground. The OS
        // then kills backgrounded playback within ~a minute (verified: a
        // track parked here on HOME stopped after ~80 s). addSession with
        // an already-playing player triggers the notification + foreground
        // promotion immediately, which is what keeps music alive on HOME.
        addSession(newSession)

        installListener(player)
        startProgressReporter(player)
        watchAdminStops()
        val pendingError = player.playerError
        when {
            // Failed in the moment between the park and this attach.
            pendingError != null -> onFatalError(player, pendingError)
            // Ended in the moment between the park and this attach (the
            // fragment's listener was already gone): handle the end now, or
            // the chain and the final report would never happen.
            player.playbackState == Player.STATE_ENDED -> onItemEnded(player)
            // Parked while paused (paused, then HOME): the pause window starts
            // now. The fragment already reported the pause.
            !player.playWhenReady -> onPaused(player, report = false)
        }
    }

    /** Detach and return the active player. Called when
     *  PlaybackFragment is re-entering for the same item — the
     *  fragment takes the player back, attaches it to its Leanback
     *  glue, and the service shuts down (no second player needed). */
    fun detach(): ExoPlayer? {
        val p = activePlayer ?: return null
        detachInternal()
        // Don't release the player — the caller now owns it.
        stopSelf()
        return p
    }

    /** Whether a player is currently parked in the service. The
     *  fragment uses this on entry to decide between "rebind to the
     *  service's player" vs "build a fresh ExoPlayer". */
    fun hasActivePlayer(): Boolean = activePlayer != null

    /** Tear down the session, listener, and jobs without releasing the
     *  player itself or ending its server session. Used by both detach()
     *  (player ownership returns to the fragment) and the next-attach path
     *  (the previous player was released, and its session ended, by
     *  AudioHandoff.park when the new one was parked). */
    private fun detachInternal() {
        progressJob?.cancel()
        progressJob = null
        pauseReleaseJob?.cancel()
        pauseReleaseJob = null
        adminStopJob?.cancel()
        adminStopJob = null
        playerListener?.let { listener -> activePlayer?.removeListener(listener) }
        playerListener = null
        // Unregister from the service (paired with addSession in attach)
        // so the notification is torn down before the session is released.
        session?.let { removeSession(it) }
        session?.release()
        session = null
        activePlayer = null
        attachedGeneration = -1L
        activeItemId = null
        activeItemType = null
        activeParentId = null
        activeIndex = null
        activeHlsOffsetMs = 0L
        activeItemDurationMs = null
        activeNowPlaying = null
        speedBookId = null
        activeStreamSession = null
        pausedAtMs = null
    }

    /**
     * Obey the admin "stop this stream" SSE event for the item playing here,
     * as the player screen does while it has the player (see
     * PlaybackFragment.startAdminStopWatch, which stops when the player is
     * handed over). The heartbeat's 403 is no backstop for a paused player,
     * which sends none, or for a server transcode, which has no refusal
     * window: without this, both carried on after the stop.
     */
    private fun watchAdminStops() {
        adminStopJob?.cancel()
        adminStopJob = scope.launch {
            while (isActive) {
                try {
                    notifications.subscribePlaybackStops().collect { evt ->
                        val player = activePlayer ?: return@collect
                        val itemId = activeItemId ?: return@collect
                        val mine = PlaybackStop.targets(
                            evt,
                            playingItemId = itemId,
                            sessionId = activeStreamSession?.id,
                            clientName = clientName.value,
                        )
                        if (!mine) return@collect
                        haltForRefusal(player, itemId, HeartbeatRefusal.PlaybackStopped(PlaybackStop.text(evt.message)))
                    }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (_: Exception) {
                    // Stream dropped; reconnect after a short delay.
                }
                delay(5_000)
            }
        }
    }

    /** PUT /items/{id}/progress now, then every 10 s while the
     *  service-owned player is playing — the same cadence as
     *  ProgressTracker on the fragment side. Without this the resume
     *  marker would freeze the moment the user navigates away from the
     *  player. The first beat goes out at once: the fragment's tracker
     *  stops when it hands the player over, and waiting a full interval
     *  left a gap of up to 20 s at every hand-off. */
    private fun startProgressReporter(player: ExoPlayer) {
        progressJob?.cancel()
        progressJob = scope.launch {
            while (isActive) {
                if (!heartbeat(player)) return@launch
                delay(10_000)
            }
        }
    }

    /** One 'playing' heartbeat if the player is actually playing (not
     *  paused, buffering, or finished — a finished player keeps
     *  playWhenReady, which kept reporting "playing" every 10 s after the
     *  last track ended). Returns false once the server refused playback. */
    private suspend fun heartbeat(player: ExoPlayer): Boolean {
        val itemId = activeItemId ?: return true
        if (!player.isPlaying) return true
        // Absolutise BOTH sides of the pair. Position is already
        // offset-corrected; duration must be too, or a resumed HLS
        // session reports content-time position against remaining-time
        // duration and trips the server's watched threshold early.
        val dur = PlaybackHelper.contentDurationMs(
            activeItemDurationMs, player.duration, activeHlsOffsetMs,
        )
        if (dur <= 0) return true
        // A VBR file can play a little past the length it listed.
        val pos = (player.currentPosition + activeHlsOffsetMs).coerceAtMost(dur)
        // Best-effort EXCEPT a refusal: any 403 on this heartbeat means
        // the server no longer lets this profile play the item (watch
        // cap / allowed hours, library access revoked, rating ceiling
        // lowered). Swallowing it kept parked music streaming on its
        // already-issued token. Same decision as the fragment's
        // ProgressTracker — see HeartbeatRefusal.
        val refusal = HeartbeatRefusal.heartbeat {
            itemRepo.updateProgress(itemId, pos, dur, "playing")
        }
        if (refusal != null) {
            haltForRefusal(player, itemId, refusal)
            return false
        }
        return true
    }

    /** A 'paused' or 'stopped' report for the active item at the player's
     *  position, sent on a scope that outlives this service. Main thread
     *  (it reads the player). Skipped for a finished item: the end already
     *  reported it 'stopped' at its full length. */
    private fun reportState(player: ExoPlayer, state: String) {
        val itemId = activeItemId ?: return
        if (player.playbackState == Player.STATE_ENDED) return
        val dur = PlaybackHelper.contentDurationMs(activeItemDurationMs, player.duration, activeHlsOffsetMs)
        if (dur <= 0) return
        val pos = (player.currentPosition + activeHlsOffsetMs).coerceAtMost(dur)
        if (pos <= 0) return
        reports.launch {
            runCatching { itemRepo.updateProgress(itemId, pos, dur, state) }
        }
    }

    /** Stop background playback the server refused mid-session. There is no
     *  UI here to explain it (the fragment's dialog covers the foreground
     *  path); this only makes sure nothing keeps playing or reporting.
     *   - The listener goes first: it would otherwise report a 'stopped'
     *     for this item and chain to the next sibling.
     *   - stop() + clearMediaItems() ends the stream and empties the session,
     *     so the system media rail / notification stops advertising the item.
     *   - releaseAndStop() releases the player, ends its server session and
     *     clears the handoff slot, so a returning fragment builds a fresh
     *     player (and its own start path re-checks access). */
    private fun haltForRefusal(player: ExoPlayer, itemId: String, refusal: HeartbeatRefusal) {
        android.util.Log.w(TAG, "heartbeat refused for $itemId ($refusal); stopping background audio")
        playerListener?.let { player.removeListener(it) }
        playerListener = null
        activeItemId = null
        runCatching {
            player.stop()
            player.clearMediaItems()
        }
        releaseAndStop()
    }

    /** The player's end and its pauses:
     *  - On STATE_ENDED for audio, walk to the next sibling via
     *    NextSiblingResolver and start that item playing: the album's next
     *    track, the book's next chapter file, the series' next book. With
     *    nothing next, the queue is done and the service stops. Mirrors the
     *    fragment-side auto-advance so navigating away mid-album or mid-book
     *    doesn't kill the chain. Episodes intentionally skip this path on
     *    the service — the fragment side surfaces an Up Next overlay for
     *    episodes (visual chrome we can't render in the service
     *    notification), so silent-chain auto-advance for episodes is
     *    fragment-only.
     *  - A pause is reported to the server ('paused', so its resume point is
     *    exact and it stops showing the item as playing) and starts the
     *    [BackgroundPause] window; a pause that outlasts it releases the
     *    player. */
    private fun installListener(player: ExoPlayer) {
        val listener = object : Player.Listener {
            override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
                if (playWhenReady) {
                    pausedAtMs = null
                    pauseReleaseJob?.cancel()
                    pauseReleaseJob = null
                    return
                }
                onPaused(player, report = true)
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED) onItemEnded(player)
            }

            override fun onPlayerError(error: PlaybackException) {
                onFatalError(player, error)
            }
        }
        player.addListener(listener)
        playerListener = listener
    }

    /**
     * The item finished: report it 'stopped' at its full length (so a track
     * completed in the background still scrobbles — even when it's the last
     * in the queue or the next-sibling lookup fails; the fragment-side
     * ProgressTracker only reports on its own onStop, which never fires for
     * this service-owned auto-advance), then walk to the next sibling via
     * NextSiblingResolver and play it. With nothing next, the queue is done
     * and the service lets the player go.
     */
    private fun onItemEnded(player: ExoPlayer) {
        if (!AudioItemTypes.isAudio(activeItemType)) return
        val itemId = activeItemId ?: return
        val type = activeItemType ?: return
        val parentId = activeParentId
        val index = activeIndex
        val dur = PlaybackHelper.contentDurationMs(
            activeItemDurationMs, player.duration, activeHlsOffsetMs,
        )
        if (dur > 0) {
            // On the detached scope: at the end of the queue the service
            // stops right away, and its own scope's cancellation would take
            // this, the item's only completion report, with it.
            reports.launch {
                runCatching { itemRepo.updateProgress(itemId, dur, dur, "stopped") }
            }
        }
        val resolver = NextSiblingResolver(itemRepo)
        scope.launch {
            val next = resolver.resolve(itemId, type, parentId, index)
            // Still sitting at the end of that item? A play from the
            // system media controls while the lookup ran restarted it;
            // chaining now would yank the listener away mid-replay.
            if (activeItemId != itemId || player.playbackState != Player.STATE_ENDED) return@launch
            if (next == null) {
                // The last track or chapter: nothing plays next, so the
                // service's work is done.
                android.util.Log.i(TAG, "queue ended after $itemId; stopping background audio")
                releaseAndStop()
                return@launch
            }
            chainTo(next.id)
        }
    }

    /**
     * The background player failed and stopped (ExoPlayer doesn't recover
     * from a fatal error by itself). An admin stop or a refused session
     * shows up exactly like this for a server stream: its segments start
     * failing. The heartbeat that would have carried the server's refusal
     * only runs while playing, so send one now: a refusal is handled like
     * any other; otherwise the player is let go, its final position
     * reported and its session ended, rather than left dead in the service.
     */
    private fun onFatalError(player: ExoPlayer, error: PlaybackException) {
        android.util.Log.w(TAG, "background player error ${error.errorCodeName}", error)
        // The probe is a 'playing' heartbeat, and the server only refuses
        // those; a paused player sends none, so it is simply let go.
        if (!player.playWhenReady) return releaseAndStop()
        val itemId = activeItemId ?: return releaseAndStop()
        val dur = PlaybackHelper.contentDurationMs(activeItemDurationMs, player.duration, activeHlsOffsetMs)
        val pos = (player.currentPosition + activeHlsOffsetMs).coerceAtMost(maxOf(dur, 0L))
        scope.launch {
            val refusal = if (dur > 0) {
                HeartbeatRefusal.heartbeat { itemRepo.updateProgress(itemId, pos, dur, "playing") }
            } else {
                null
            }
            if (activePlayer !== player) return@launch
            if (refusal != null) haltForRefusal(player, itemId, refusal) else releaseAndStop()
        }
    }

    /** A pause of the background player (not an end, not idle): report it
     *  (unless the fragment already did) and start the [BackgroundPause]
     *  window, after which the player is released. */
    private fun onPaused(player: ExoPlayer, report: Boolean) {
        val state = player.playbackState
        if (state == Player.STATE_ENDED || state == Player.STATE_IDLE) return
        pausedAtMs = SystemClock.elapsedRealtime()
        if (report) reportState(player, "paused")
        pauseReleaseJob?.cancel()
        pauseReleaseJob = scope.launch {
            delay(BackgroundPause.HOLD_MS)
            if (activePlayer === player && !player.playWhenReady) {
                android.util.Log.i(TAG, "paused for ${BackgroundPause.HOLD_MS / 60_000} min; releasing background audio")
                releaseAndStop()
            }
        }
    }

    /** Switch the service-owned player to a different item id.
     *  Re-resolves the file via the item endpoint, asks the server how
     *  to play it (as the fragment's prepare() does), and swaps the
     *  MediaSource: a direct-play URL with the same per-file stream-token
     *  machinery the fragment uses, or a new server session for a track
     *  this device can't decode. */
    private suspend fun chainTo(itemId: String) {
        val player = activePlayer ?: return
        val fromItemId = activeItemId
        val item = try { itemRepo.getItem(itemId) } catch (_: Exception) { return }
        val file = item.files.firstOrNull() ?: return
        val server = prefs.getServerUrl()?.trimEnd('/').orEmpty()
        if (server.isEmpty()) return

        // The session's names for the next item, looked up alongside the
        // play decision rather than after it.
        val (verdict, names) = coroutineScope {
            val names = async { NowPlaying.resolve(itemRepo, item) }
            transcodeRepo.decide(itemId, file.id) to names.await()
        }
        // A track this device can't decode (DSD, ALAC on some panels) comes
        // back as a remux or transcode. Chaining always direct-played, so
        // after a transcoded track the next one failed in the background.
        val mode = PlaybackHelper.modeFor(verdict, file) ?: return
        var url: String? = null
        var next: StreamSession? = null
        if (mode is PlaybackMode.DirectPlay) {
            // Prefer the per-file stream token; fall back to the purpose=asset
            // token (NOT the access token, which the server rejects in a URL).
            val token = file.stream_token ?: prefs.getAssetToken().orEmpty()
            if (token.isEmpty()) return
            // CLEAN url on the MediaItem — this player is wrapped in our
            // MediaSession, whose legacy bridge republishes the item uri to other
            // apps. The credential goes in the vault; the player's resolving data
            // source (built in PlaybackFragment.buildExoPlayer) re-attaches it.
            url = StreamTokenVault.register("$server${file.stream_url}", token)
        } else {
            val started = try {
                transcodeRepo.start(
                    itemId = itemId,
                    height = (mode as? PlaybackMode.Transcode)?.height ?: 0,
                    positionMs = 0L,
                    fileId = file.id,
                    videoCopy = mode is PlaybackMode.Remux,
                    supportsHevc = PlaybackHelper.supportsHevc(),
                    supportsAv1 = PlaybackHelper.supportsAv1(),
                )
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                // A 403 is the server refusing playback (admin stop, watch
                // limit, access revoked), exactly as on a heartbeat. Anything
                // else just ends the chain here.
                HeartbeatRefusal.of("playing", e)?.let { haltForRefusal(player, fromItemId ?: itemId, it) }
                return
            }
            next = StreamSession.opened(started, server, 0L)
        }
        // Still ours, and still at the end of the item this chains from? A
        // session start can take a while, and a play from the system media
        // controls meanwhile restarted that item.
        if (activePlayer !== player || activeItemId != fromItemId || player.playbackState != Player.STATE_ENDED) {
            next?.let { transcodeRepo.stopDetached(it) }
            return
        }

        // The player leaves the old session for good: end it.
        activeStreamSession?.let { transcodeRepo.stopDetached(it) }
        activeStreamSession = next

        activeItemId = itemId
        activeItemType = item.type
        activeParentId = item.parent_id
        activeIndex = item.index
        activeHlsOffsetMs = next?.offsetMs ?: 0L
        // Chapters and books carry their length on the file, not the item.
        activeItemDurationMs = item.duration_ms ?: file.duration_ms
        activeNowPlaying = names

        // Keep the handoff slot's metadata current so a fragment that
        // reclaims this player after the background advance binds to the
        // track now playing (not the one originally parked) — otherwise
        // it shows a stale title and reports progress under the wrong id.
        AudioHandoff.updateMetadata(
            AudioHandoff.Metadata(
                itemId = itemId,
                itemType = item.type,
                parentId = item.parent_id,
                index = item.index,
                hlsOffsetMs = activeHlsOffsetMs,
                itemDurationMs = activeItemDurationMs,
                session = next,
                nowPlaying = names,
            ),
        )

        if (next != null) {
            player.setMediaSource(TranscodeHls.mediaSource(next.playlistUrl))
        } else {
            player.setMediaItem(MediaItem.fromUri(Uri.parse(checkNotNull(url))))
        }
        player.prepare()
        player.playWhenReady = true
        applyListeningSpeed(player, item.id, item.type, item.parent_id)
    }

    /**
     * Speed for the item just chained to. The player carries its speed
     * across items (it's the fragment's player, handed over at HOME / BACK,
     * sped up there or not), which is right within a book but not across
     * them: the next book in a series gets its own saved speed, and anything
     * that isn't an audiobook plays at 1×. The book's next chapter file is
     * the same book, so it keeps the speed as it is — no lookup that could
     * only make it jump ([AudiobookSpeed.keepsSpeed]).
     */
    private fun applyListeningSpeed(player: ExoPlayer, itemId: String, type: String, parentId: String?) {
        val bookId = AudiobookSpeed.bookIdOf(type, itemId, parentId)
        val sameBook = AudiobookSpeed.keepsSpeed(speedBookId, bookId)
        speedBookId = bookId
        if (sameBook) return
        if (bookId == null) {
            if (!AudiobookSpeed.same(player.playbackParameters.speed, AudiobookSpeed.NORMAL)) {
                player.setPlaybackSpeed(AudiobookSpeed.NORMAL)
            }
            return
        }
        scope.launch {
            val rate = audiobooks.rate(itemId, bookId) ?: return@launch
            // Still on that book, and still ours?
            if (activeItemId == itemId && activePlayer === player) player.setPlaybackSpeed(rate)
        }
    }

    /** Let go of the player now and stop the service. Done here rather
     *  than left to onDestroy, which doesn't run while a system controller
     *  is still bound to the session. */
    private fun releaseAndStop() {
        teardown()
        stopSelf()
    }

    /**
     * Drop the session and the player. The service owns the player only
     * while the handoff slot still holds the very park it attached.
     * Otherwise a fragment took the player back via AudioHandoff.take() and
     * then called stopService() — which lands us here — or took it and
     * parked it again, for a new start of the service to pick up. Releasing
     * the player then would tear down one a fragment is driving (or is about
     * to take back), so just the session wrapper and listener go, and the
     * player and its server session are left alone. If the slot still holds
     * our park, the service genuinely owns the player (queue ended, pause
     * window passed, task removed, refusal) and it goes: final position
     * reported, player released, server session ended.
     */
    private fun teardown() {
        progressJob?.cancel()
        progressJob = null
        pauseReleaseJob?.cancel()
        pauseReleaseJob = null
        adminStopJob?.cancel()
        adminStopJob = null
        val sess = session
        val player = activePlayer
        // Unregister from the service (paired with addSession) before
        // releasing — drops the media notification + foreground state.
        sess?.let { removeSession(it) }
        if (player != null) {
            val owned = AudioHandoff.owns(player, attachedGeneration)
            playerListener?.let { player.removeListener(it) }
            playerListener = null
            if (owned) {
                reportState(player, "stopped")
                player.release()
                activeStreamSession?.let { transcodeRepo.stopDetached(it) }
                AudioHandoff.clear()
            }
        }
        sess?.release()
        session = null
        activePlayer = null
        attachedGeneration = -1L
        activeStreamSession = null
        activeItemId = null
    }

    override fun onDestroy() {
        scope.cancel()
        teardown()
        super.onDestroy()
    }

    private companion object {
        const val TAG = "OnScreenMediaSession"
        const val STRAY_CHANNEL_ID = "playback_service"
        const val STRAY_NOTIFICATION_ID = 0x0A5E

        /** The pause / stop reports. Outlives the service: the final
         *  'stopped' report is sent as it is destroyed, when its own scope is
         *  already cancelled. One lane for every instance, so a 'paused'
         *  never lands after the 'stopped' that follows it. */
        val reports = ReportLane(CoroutineScope(SupervisorJob() + Dispatchers.IO))
    }
}
