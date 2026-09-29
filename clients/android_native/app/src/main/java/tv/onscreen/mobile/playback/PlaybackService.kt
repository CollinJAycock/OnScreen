package tv.onscreen.mobile.playback

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.SystemClock
import android.widget.Toast
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.Timeline
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.common.util.Util
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.ResolvingDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import tv.onscreen.mobile.data.api.HeartbeatRefusal
import tv.onscreen.mobile.data.prefs.PlaybackPrefs
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.AudiobookRepository
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.NotificationsRepository
import tv.onscreen.mobile.ui.player.playbackStoppedMessage
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
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
 * type/parentId/index (+ the file's ReplayGain tags) so progress reporting,
 * the queue and the gain stage work without a re-fetch. Video never uses
 * this path — picture-in-picture is its "keep going outside the player"
 * story.
 *
 * Music queue + gapless: the UI hands over ONE track; the service turns it
 * into the whole album around it ([MusicQueue]) — lazily-resolved entries
 * before and after the playing item — and appends the next album as the end
 * approaches. ExoPlayer then plays across track boundaries gaplessly, and the
 * session exposes a real queue (next / previous / shuffle on the lock screen,
 * Bluetooth and the now-playing screen).
 *
 * ReplayGain: a [ReplayGainAudioProcessor] in the audio sink, fed each
 * track's tags at the exact buffer where the output moves to it
 * ([ReplayGainAudioRenderer]); mode + preamp come from [PlaybackPrefs].
 *
 * Audiobooks: listening speed follows the item ([applyListeningSpeed]) — a
 * book, or a chapter of one, plays at the book's saved speed and anything
 * else at 1×. A multi-file book chains chapter to chapter on STATE_ENDED
 * ([maybeAutoAdvance]); the speed rides along because ExoPlayer keeps its
 * PlaybackParameters across items. The sleep timer's "end of chapter" arms
 * [StopAfterItem] so the chain doesn't start the next chapter.
 */
@UnstableApi
@AndroidEntryPoint
class PlaybackService : MediaSessionService() {

    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var playbackPrefs: PlaybackPrefs
    @Inject lateinit var notifications: NotificationsRepository
    @Inject lateinit var audiobooks: AudiobookRepository

    private var session: MediaSession? = null
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private var progressJob: Job? = null
    private var expandJob: Job? = null
    private var extendJob: Job? = null
    private var stopEventsJob: Job? = null
    private var speedJob: Job? = null

    // The book the player's current speed belongs to (null: not a book, so
    // 1×). Moving to another chapter of the same book leaves the speed alone.
    private var speedBookId: String? = null

    // Guards against double-publishing the terminal 'stopped' for one
    // item (STATE_ENDED can be followed by teardown). Reset when a new
    // item becomes current so replaying the same track reports again.
    private var reportedStoppedFor: String? = null

    // Set once the account signed out under us (see haltForSignOut); no
    // further progress is reported for it.
    private var signedOut = false

    // The single UI-provided item the queue was last built around (identity:
    // a controller's setMediaItem always arrives as a fresh instance), so a
    // queue that later shrinks back to one item isn't rebuilt.
    private var lastExpandedAnchor: MediaItem? = null

    // The SSE event, the heartbeat 403 and the media 403 can all land for one
    // admin stop — act (and toast) once per playback (reset in
    // onMediaItemTransition, so a replay's refusal is acted on).
    private val adminStops = AdminStopDedupe()

    /** The ReplayGain stage. Settings pushed from [PlaybackPrefs]. */
    private val replayGain = ReplayGainAudioProcessor(onAppliedChanged = BackgroundAudioEvents::publishReplayGainDb)

    /** ReplayGain tags of lazily-resolved queue entries, by item id. Written
     *  by the resolver on the loader thread, read by the renderer on the
     *  playback thread. */
    private val resolvedTags = ConcurrentHashMap<String, ReplayGainInfo>()

    /** Last duration seen per item (main thread; bounded). A queue replaced
     *  mid-track has already dropped the old item's timeline window by the
     *  time its 'stopped' is reported. */
    private val knownDurations = object : LinkedHashMap<String, Long>(16, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Long>?): Boolean = size > 32
    }

    /** Clean stream url per resolved queue entry (credential in the vault). */
    private val resolvedUrls = ConcurrentHashMap<String, Resolved>()
    private data class Resolved(val url: String, val atMs: Long)

    companion object {
        private const val TAG = "PlaybackService"

        // MediaItem metadata extras keys (set by the UI, read here).
        const val EXTRA_TYPE = "onscreen.type"
        const val EXTRA_PARENT_ID = "onscreen.parentId"
        const val EXTRA_INDEX = "onscreen.index"

        /** User-Agent product token for this player's stream requests. The
         *  server names a direct-play stream after the UA prefix (items.go
         *  StreamFile → Now Playing card), and an admin stop aimed at it
         *  carries that name as `client_name` — so a stable name here is what
         *  lets [tv.onscreen.mobile.data.model.PlaybackStopEvent.targets]
         *  recognise a stop meant for this phone's music (the default
         *  "Dalvik" UA is shared by every Android player). */
        const val STREAM_CLIENT_NAME = "OnScreenPhone"

        /** Re-fetch a queue entry's stream url after this long: stream tokens
         *  live 24 h, a queue can outlast that. */
        private const val RESOLVE_TTL_MS = 6 * 60 * 60 * 1000L
        private const val RESOLVE_TIMEOUT_MS = 15_000L

        /** Id of the item the service player has current, or null when idle.
         *  The now-playing screen reads it to bind to an already-playing
         *  queue item instead of starting a second player for it. */
        @Volatile
        var currentItemId: String? = null
            private set
    }

    override fun onCreate() {
        super.onCreate()
        // Resolver chain, outermost first:
        //  1. queue placeholders (onscreen-item://<id>) → the file's clean
        //     stream url, fetched when ExoPlayer first opens the entry;
        //  2. StreamTokenVault re-attaches that url's `?token=` below the
        //     session, so it never reaches the platform MediaSession metadata;
        //  3. DefaultDataSource (http / file) with a named User-Agent.
        val http = DefaultHttpDataSource.Factory()
            .setUserAgent(Util.getUserAgent(this, STREAM_CLIENT_NAME))
        val dsFactory = ResolvingDataSource.Factory(
            StreamTokenVault.resolverFactory(DefaultDataSource.Factory(this, http)),
        ) { dataSpec ->
            val id = MusicQueue.itemIdFromPlaceholder(dataSpec.uri.toString())
            if (id == null) dataSpec else dataSpec.withUri(Uri.parse(resolveQueueItem(id)))
        }
        val player = ExoPlayer.Builder(this, ReplayGainRenderersFactory(this, replayGain, ::tagsFor))
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
        watchSignOut(player)
        watchReplayGainSettings()
    }

    /** Push Settings → Playback → ReplayGain into the audio stage. A change
     *  lands on the next buffer (ramped), so it applies to the playing track. */
    private fun watchReplayGainSettings() {
        scope.launch {
            combine(playbackPrefs.replayGainMode, playbackPrefs.replayGainPreampDb) { mode, preamp ->
                ReplayGainAudioProcessor.Settings(mode, preamp)
            }.collect { replayGain.setSettings(it) }
        }
    }

    /** Tags for the renderer: the UI-provided item carries them in its extras;
     *  a lazily-resolved queue entry has them from its resolve. Unknown →
     *  null (plays at unity). */
    private fun tagsFor(item: MediaItem?): ReplayGainInfo? {
        if (item == null) return null
        return ReplayGainExtras.read(item.mediaMetadata.extras) ?: resolvedTags[item.mediaId]
    }

    /**
     * Resolve a queue placeholder to the clean stream url of the item's file.
     * Runs on ExoPlayer's loader thread, which may block. Throws IOException,
     * which ExoPlayer surfaces as a source error for that entry.
     */
    private fun resolveQueueItem(id: String): String {
        resolvedUrls[id]?.let { r ->
            val fresh = SystemClock.elapsedRealtime() - r.atMs < RESOLVE_TTL_MS
            // The vault is a bounded LRU: if the credential was evicted the
            // bare url would 401, so fetch a new one instead.
            if (fresh && StreamTokenVault.resolve(r.url) != null) return r.url
        }
        val url = try {
            runBlocking(Dispatchers.IO) {
                withTimeout(RESOLVE_TIMEOUT_MS) {
                    val item = itemRepo.getItem(id)
                    val file = item.files.firstOrNull() ?: throw IOException("no playable file for $id")
                    resolvedTags[id] = ReplayGain.fromFile(file)
                    buildDirectPlayUrl(file.stream_url, file.stream_token)
                        ?: throw IOException("no server url")
                }
            }
        } catch (e: IOException) {
            throw e
        } catch (e: Exception) {
            throw IOException("resolving queue item $id failed", e)
        }
        resolvedUrls[id] = Resolved(url, SystemClock.elapsedRealtime())
        return url
    }

    /**
     * Stop the audio ourselves on the logged-in → logged-out edge.
     *
     * SignOutTeardown / SettingsViewModel call stopService(), but that cannot
     * destroy a service with a bound MediaController — and MiniPlayerBar holds
     * one for as long as the activity lives, including while it sits stopped
     * in the background (collectAsStateWithLifecycle means AppNav does not
     * even reroute to /pair until the app is foregrounded). A remote revoke of
     * a phone playing music in the pocket therefore kept streaming the revoked
     * account's track, on its stream token, with its title on the lock screen.
     */
    private fun watchSignOut(player: ExoPlayer) {
        scope.launch {
            var wasLoggedIn = prefs.isLoggedIn.first()
            prefs.isLoggedIn.collect { loggedIn ->
                if (wasLoggedIn && !loggedIn) haltForSignOut(player)
                wasLoggedIn = loggedIn
            }
        }
    }

    private fun haltForSignOut(player: ExoPlayer) {
        // Set first: stop() below drives STATE_IDLE → reportStopped, and the
        // terminal PUT must not go out — the session is gone, so it would
        // travel unauthenticated, and a 401 retried after a quick re-pair
        // could land the previous account's position on the new one.
        signedOut = true
        progressJob?.cancel()
        expandJob?.cancel()
        extendJob?.cancel()
        speedJob?.cancel()
        speedBookId = null
        // The previous account's resolved stream urls / tags must not serve
        // the next one.
        resolvedUrls.clear()
        resolvedTags.clear()
        knownDurations.clear()
        stopAndClear(player)
    }

    /** Stop background playback the server refused mid-session (any 403 on
     *  the 'playing' heartbeat — see HeartbeatRefusal). There is no UI here to
     *  explain it; PlayerViewModel's own heartbeat shows the message when the
     *  player screen is open. The heartbeat keeps running for whatever a
     *  still-bound controller plays next; it skips while the queue is empty. */
    private fun haltForRefusal(player: Player, itemId: String, refusal: HeartbeatRefusal) {
        android.util.Log.w(TAG, "heartbeat refused for $itemId ($refusal); stopping background audio")
        // Mark the item as already reported BEFORE stop(): stop() drives
        // STATE_IDLE → reportStopped, and no further report may go out for an
        // item the server just refused. clearMediaItems() then resets the guard
        // via onMediaItemTransition(null), but with the queue empty there is
        // no current item left to report.
        reportedStoppedFor = itemId
        stopAndClear(player)
    }

    /**
     * An admin stopped this stream (Now Playing → Stop): the `playback.stop`
     * SSE event, a 403 PLAYBACK_STOPPED on the 'playing' heartbeat, or one on
     * a media request. Stop now and drop the queue — NOT skip to the next
     * track — and tell the listener why: a toast (the mini player goes away
     * with the queue) and, when the now-playing screen is open, its own
     * message via [BackgroundAudioEvents]. Same behaviour as the web player.
     *
     * Unlike [haltForRefusal] the cut-off position IS reported 'stopped' (the
     * server never refuses a stop report), so the resume point survives.
     * Idempotent per playback ([AdminStopDedupe]).
     */
    private fun haltForAdminStop(player: Player, itemId: String, message: String) {
        if (!adminStops.first(itemId)) return
        android.util.Log.i(TAG, "admin stopped $itemId; stopping background audio")
        reportStopped(player)
        reportedStoppedFor = itemId
        stopAndClear(player)
        BackgroundAudioEvents.emitAdminStop(BackgroundAudioEvents.AdminStop(itemId, message))
        runCatching { Toast.makeText(applicationContext, message, Toast.LENGTH_LONG).show() }
    }

    private fun stopAndClear(player: Player) {
        BackgroundAudioEvents.publishReplayGainDb(null)
        runCatching {
            player.stop()
            // Empty the timeline so the platform session stops advertising
            // the track (lock screen, notification).
            player.clearMediaItems()
        }
        stopSelf()
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
        currentItemId = null
        BackgroundAudioEvents.publishReplayGainDb(null)
        session?.run {
            player.release()
            release()
        }
        session = null
        super.onDestroy()
    }

    private fun playerListener(player: ExoPlayer) = object : Player.Listener {
        override fun onTimelineChanged(timeline: Timeline, reason: Int) {
            if (reason == Player.TIMELINE_CHANGE_REASON_PLAYLIST_CHANGED) maybeExpandQueue(player)
        }

        override fun onPositionDiscontinuity(
            oldPosition: Player.PositionInfo,
            newPosition: Player.PositionInfo,
            reason: Int,
        ) {
            // Leaving an item for another without STATE_ENDED (a gapless
            // hand-off, a skip, the queue replaced mid-track) is that item's
            // terminal 'stopped' — see StopReports for the exact rules.
            val left = oldPosition.mediaItem ?: return
            val newId = newPosition.mediaItem?.mediaId
            if (!StopReports.onDiscontinuity(reason, player.playbackState, left.mediaId, newId)) return
            val durationMs = durationOfWindow(player, oldPosition.mediaItemIndex, left.mediaId)
            reportStoppedAt(left.mediaId, oldPosition.positionMs, durationMs)
        }

        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            // A new item is current — clear the stopped-guard so it (and
            // a future replay of the same track) can report on its own end,
            // and the admin-stop guard so a stop of it is acted on.
            reportedStoppedFor = null
            if (mediaItem != null) adminStops.reset()
            currentItemId = mediaItem?.mediaId
            updateStopSubscription(player)
            applyListeningSpeed(player, mediaItem)
            // Reached the last queued track by playing / skipping into it:
            // queue what comes next now, so that boundary is gapless too.
            if (reason == Player.MEDIA_ITEM_TRANSITION_REASON_AUTO ||
                reason == Player.MEDIA_ITEM_TRANSITION_REASON_SEEK
            ) {
                maybeExtendQueue(player)
            }
        }

        override fun onTracksChanged(tracks: Tracks) {
            // A queued track this device can't decode (ALAC, DSD — the UI
            // routes those to a server transcode, but the queue plays files
            // directly) would otherwise "play" as silence for its whole
            // length: ExoPlayer just selects no audio track. Skip it.
            // "Nothing selected" rather than "not supported": a hi-res file can
            // report EXCEEDS_CAPABILITIES yet play fine, and ExoPlayer selects it.
            if (!tracks.containsType(C.TRACK_TYPE_AUDIO) || tracks.isTypeSelected(C.TRACK_TYPE_AUDIO)) return
            val item = player.currentMediaItem ?: return
            if (!player.hasNextMediaItem()) return
            android.util.Log.w(TAG, "no decoder for queued ${item.mediaId}; skipping")
            reportedStoppedFor = item.mediaId // never played — nothing to scrobble
            player.seekToNextMediaItem()
        }

        override fun onPlayerError(error: PlaybackException) {
            val item = player.currentMediaItem ?: return
            // An admin stop refuses this stream's media requests with 403
            // PLAYBACK_STOPPED — end playback with the admin's message.
            playbackStoppedMessage(error)?.let { msg ->
                haltForAdminStop(player, item.mediaId, msg)
                return
            }
            // A queued file the device can't decode: drop it and carry on
            // with the rest of the queue instead of dying mid-album.
            if (isUnplayable(error) && player.hasNextMediaItem()) {
                android.util.Log.w(TAG, "unplayable queue item ${item.mediaId} (${error.errorCodeName}); skipping")
                reportedStoppedFor = item.mediaId
                player.removeMediaItem(player.currentMediaItemIndex)
                player.prepare()
            }
        }

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
            updateStopSubscription(player)
        }

        override fun onPlaybackStateChanged(state: Int) {
            updateStopSubscription(player)
            // Listeners run in a batch; onPlayerError above may already have
            // re-prepared past this IDLE. Act on the live state only.
            if (state != player.playbackState) return
            if (state == Player.STATE_ENDED) {
                // The queue ran out: report the last track stopped (≈full
                // duration → scrobbles) before chaining, so the listen lands
                // even if the next resolve fails or nothing follows.
                reportStopped(player)
                advanceAfterEnd(player)
            }
            if (state == Player.STATE_IDLE) {
                // stop() — the mini-player's ✕ (or a fatal player error).
                // Publish the terminal position while currentMediaItem still
                // exists, then tear the service down so the notification
                // doesn't linger as a paused ghost the user can't dismiss.
                reportStopped(player)
                stopSelf()
            }
        }
    }

    private fun isUnplayable(error: PlaybackException): Boolean = when (error.errorCode) {
        PlaybackException.ERROR_CODE_DECODER_INIT_FAILED,
        PlaybackException.ERROR_CODE_DECODER_QUERY_FAILED,
        PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED,
        PlaybackException.ERROR_CODE_DECODING_FORMAT_EXCEEDS_CAPABILITIES,
        PlaybackException.ERROR_CODE_PARSING_CONTAINER_UNSUPPORTED,
        -> true
        else -> false
    }

    /** Duration of the playlist window at [index] if it still holds [itemId],
     *  else the last duration the heartbeat saw for it. */
    private fun durationOfWindow(player: Player, index: Int, itemId: String): Long {
        val tl = player.currentTimeline
        if (index in 0 until tl.windowCount) {
            val w = tl.getWindow(index, Timeline.Window())
            if (w.mediaItem.mediaId == itemId && w.durationMs != C.TIME_UNSET) return w.durationMs
        }
        return knownDurations[itemId] ?: C.TIME_UNSET
    }

    // ── Queue ─────────────────────────────────────────────────────────────

    /**
     * The UI just handed over a single track: queue its album around it —
     * the tracks before it in front, the ones after it behind — without
     * touching the playing item, so playback never hiccups. Tracks only; a
     * failed /children fetch just leaves the single-item queue, which
     * [maybeExtendQueue] repairs at its end.
     */
    private fun maybeExpandQueue(player: Player) {
        if (player.mediaItemCount != 1) return
        val anchor = player.getMediaItemAt(0)
        if (anchor === lastExpandedAnchor) return
        // One of our own entries left alone (the rest were skipped/removed).
        if (MusicQueue.itemIdFromPlaceholder(anchor.localConfiguration?.uri?.toString()) != null) return
        val extras = anchor.mediaMetadata.extras ?: return
        val type = extras.getString(EXTRA_TYPE)
        if (!MusicQueue.expandsQueue(type)) return
        val parentId = extras.getString(EXTRA_PARENT_ID) ?: return
        lastExpandedAnchor = anchor
        expandJob?.cancel()
        expandJob = scope.launch {
            val children = try {
                itemRepo.getChildren(parentId)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                return@launch
            }
            val plan = MusicQueue.planAround(anchor.mediaId, type!!, children) ?: return@launch
            // Still the same single-item playlist? (The user may have picked
            // something else while the listing loaded.)
            if (player.mediaItemCount != 1 || player.getMediaItemAt(0) !== anchor) return@launch
            if (plan.before.isNotEmpty()) {
                player.addMediaItems(0, plan.before.map { QueueItems.placeholder(it, parentId) })
            }
            if (plan.after.isNotEmpty()) {
                player.addMediaItems(plan.after.map { QueueItems.placeholder(it, parentId) })
            } else {
                // Started on the album's last track: line up the next album
                // now, so even this first boundary is gapless.
                maybeExtendQueue(player)
            }
        }
    }

    /**
     * The current track is the last one queued: append what follows — the
     * rest of its album if the queue lacks it, else the artist's next album —
     * so the hand-off is gapless and playback runs on like the old
     * track-by-track chaining did. If playback already ENDED waiting for it,
     * resume into the appended tracks.
     */
    private fun maybeExtendQueue(player: Player) {
        if (player.hasNextMediaItem()) return
        val item = player.currentMediaItem ?: return
        val extras = item.mediaMetadata.extras ?: return
        val type = extras.getString(EXTRA_TYPE)
        if (!MusicQueue.expandsQueue(type)) return
        val parentId = extras.getString(EXTRA_PARENT_ID) ?: return
        if (extendJob?.isActive == true) return
        val queued = (0 until player.mediaItemCount).map { player.getMediaItemAt(it).mediaId }.toSet()
        extendJob = scope.launch {
            val next = try {
                val album = itemRepo.getChildren(parentId)
                MusicQueue.continuation(item.mediaId, type!!, parentId, album, queued) {
                    val nextAlbum = NextSiblingResolver(itemRepo).nextContainer(parentId, type)
                        ?: return@continuation null
                    nextAlbum.id to itemRepo.getChildren(nextAlbum.id)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                null
            } ?: return@launch
            if (player.currentMediaItem?.mediaId != item.mediaId || player.hasNextMediaItem()) return@launch
            player.addMediaItems(next.tracks.map { QueueItems.placeholder(it, next.parentId) })
            if (player.playbackState == Player.STATE_ENDED && player.hasNextMediaItem()) {
                player.seekToNextMediaItem()
            }
        }
    }

    /** The queue ran out. Music: queue what follows (see [maybeExtendQueue]).
     *  Audiobooks keep the old book → next-book chaining, and a multi-file
     *  book's chapters chain to each other. Unless the sleep timer asked to
     *  stop at the end of this item: then it stays ended. */
    private fun advanceAfterEnd(player: Player) {
        val item = player.currentMediaItem ?: return
        if (StopAfterItem.consume(item.mediaId)) return
        val type = item.mediaMetadata.extras?.getString(EXTRA_TYPE)
        if (MusicQueue.expandsQueue(type)) {
            maybeExtendQueue(player)
        } else {
            maybeAutoAdvance(player)
        }
    }

    // ── Admin stop ────────────────────────────────────────────────────────

    /**
     * Listen for the admin `playback.stop` SSE event while the player is
     * actually playing (one shared socket app-wide — see
     * NotificationsRepository; [wantsAdminStopEvents] for why not while
     * paused / ended / failed). Re-evaluated on every item, play/pause and
     * state change. Targeting per
     * [tv.onscreen.mobile.data.model.PlaybackStopEvent.targets]: this player is
     * a direct-play stream named [STREAM_CLIENT_NAME] with no server session.
     * A missed event is still caught by the heartbeat / media-request 403.
     */
    private fun updateStopSubscription(player: Player) {
        val active = wantsAdminStopEvents(
            playWhenReady = player.playWhenReady,
            playbackState = player.playbackState,
            hasItem = player.currentMediaItem != null,
        )
        if (!active) {
            stopEventsJob?.cancel()
            stopEventsJob = null
            return
        }
        if (stopEventsJob?.isActive == true) return
        stopEventsJob = scope.launch {
            try {
                notifications.subscribePlaybackStops().collect { ev ->
                    val current = player.currentMediaItem ?: return@collect
                    if (ev.targets(current.mediaId, sessionId = null, clientName = STREAM_CLIENT_NAME)) {
                        haltForAdminStop(player, current.mediaId, ev.displayText)
                    }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                android.util.Log.w(TAG, "playback.stop subscription ended", e)
            }
        }
    }

    // ── Progress ──────────────────────────────────────────────────────────

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
                knownDurations[id] = dur
                // Publish locally BEFORE the write: the server fans this
                // back out over SSE, and the now-playing screen must
                // recognise it as our own echo rather than a remote device
                // telling it to seek.
                LocalProgressTracker.record(id, pos)
                // Best-effort EXCEPT a refusal: any 403 on this heartbeat means
                // the server no longer lets this profile play the item (watch
                // cap / allowed hours, library access revoked, rating ceiling
                // lowered, an admin stop). Swallowing it kept background music
                // streaming on its already-issued token. Same decision as
                // PlayerViewModel.reportProgress — see HeartbeatRefusal.
                when (
                    val refusal = HeartbeatRefusal.heartbeat {
                        itemRepo.updateProgress(id, pos, dur, "playing")
                    }
                ) {
                    null -> Unit
                    is HeartbeatRefusal.PlaybackStopped -> haltForAdminStop(player, id, refusal.message)
                    else -> haltForRefusal(player, id, refusal)
                }
            }
        }
    }

    /** Publish the terminal 'stopped' for the current item — the
     *  server's scrobble trigger. Deduped per item id. */
    private fun reportStopped(player: Player?) {
        val item = player?.currentMediaItem ?: return
        reportStoppedAt(item.mediaId, player.currentPosition, player.duration)
    }

    private fun reportStoppedAt(id: String, positionMs: Long, durationMs: Long) {
        if (signedOut) return
        if (id.isEmpty() || id == reportedStoppedFor) return
        if (durationMs <= 0 || durationMs == C.TIME_UNSET) return
        val pos = positionMs.coerceIn(0, durationMs)
        reportedStoppedFor = id
        LocalProgressTracker.record(id, pos)
        // Detached, NOT scope.launch: the caller that matters is onDestroy,
        // which cancels `scope` on the very next line — the PUT would die at
        // its first suspension point and the terminal position + scrobble
        // would be lost on every swipe-away / service kill.
        itemRepo.reportProgressDetached(id, pos, durationMs, "stopped")
    }

    // ── Audiobook chaining ────────────────────────────────────────────────

    /** On end of an audiobook (or any non-queued audio), resolve the next
     *  sibling and chain to it. Music uses the queue instead. */
    private fun maybeAutoAdvance(player: Player) {
        val item = player.currentMediaItem ?: return
        val md = item.mediaMetadata
        val type = md.extras?.getString(EXTRA_TYPE)
        if (type == AudiobookSpeed.CHAPTER) {
            chainToNextChapter(player, item)
            return
        }
        if (type != "track" && type != "audiobook") return
        val itemId = item.mediaId.ifEmpty { return }
        val parentId = md.extras?.getString(EXTRA_PARENT_ID)
        val index = md.extras?.getInt(EXTRA_INDEX, -1)?.takeIf { it >= 0 }
        scope.launch {
            val next = NextSiblingResolver(itemRepo).resolve(itemId, type, parentId, index) ?: return@launch
            chainTo(next.id)
        }
    }

    /** A chapter of a multi-file book ended: play the book's next chapter
     *  file ([AudiobookChapters] — by the listing, since chapters usually
     *  carry no index). The last chapter just ends. */
    private fun chainToNextChapter(player: Player, item: MediaItem) {
        val itemId = item.mediaId.ifEmpty { return }
        val bookId = item.mediaMetadata.extras?.getString(EXTRA_PARENT_ID) ?: return
        scope.launch {
            val next = try {
                AudiobookChapters.nextAfter(itemRepo.getChildren(bookId), itemId)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                null
            } ?: return@launch
            // Still sitting at the end of that chapter? The listener may have
            // picked something else while the listing loaded.
            if (player.currentMediaItem?.mediaId != itemId || player.playbackState != Player.STATE_ENDED) {
                return@launch
            }
            chainTo(next.id)
        }
    }

    // ── Listening speed ───────────────────────────────────────────────────

    /**
     * Put the player at the right speed for [item]: an audiobook (or a
     * chapter of one) at its book's saved speed, everything else at 1× —
     * this one player plays music too, and a book's 1.5× must not leak into
     * an album. Runs on every item change, whoever caused it: the screen
     * handing over a book, the queue moving on, a chapter or book chaining
     * here with no screen open.
     *
     * Another chapter of the book already playing changes nothing: ExoPlayer
     * keeps its PlaybackParameters across items, so the speed carries over —
     * including one the listener changed mid-book, which the screen applies
     * straight to this player through its MediaController. A new book fetches
     * its speed (AudiobookRepository prefers a pick whose PUT hasn't landed).
     */
    private fun applyListeningSpeed(player: Player, item: MediaItem?) {
        if (item == null) return
        val extras = item.mediaMetadata.extras
        val bookId = AudiobookSpeed.bookIdOf(
            extras?.getString(EXTRA_TYPE),
            item.mediaId,
            extras?.getString(EXTRA_PARENT_ID),
        )
        if (bookId == null) {
            speedJob?.cancel()
            speedBookId = null
            if (!AudiobookSpeed.same(player.playbackParameters.speed, AudiobookSpeed.NORMAL)) {
                player.setPlaybackSpeed(AudiobookSpeed.NORMAL)
            }
            return
        }
        if (bookId == speedBookId) return
        speedBookId = bookId
        speedJob?.cancel()
        val itemId = item.mediaId
        speedJob = scope.launch {
            val rate = audiobooks.listeningSpeed(itemId, bookId).rate ?: return@launch
            if (speedBookId == bookId) player.setPlaybackSpeed(rate)
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
            ReplayGainExtras.write(this, ReplayGain.fromFile(file))
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
     *  rejected in a query string by the asset-route middleware).
     *
     *  Returns the url WITHOUT the credential and stashes the credential in
     *  [StreamTokenVault], which the player's data source re-attaches at
     *  request time. The token must not ride in the MediaItem uri — this is a
     *  MediaSession player, and media3 republishes that uri into the platform
     *  session's METADATA_KEY_MEDIA_URI where any notification-listener app
     *  can read it. */
    private suspend fun buildDirectPlayUrl(streamPath: String, streamToken: String?): String? {
        val server = prefs.getServerUrl()?.trimEnd('/') ?: return null
        val token = if (!streamToken.isNullOrEmpty()) streamToken else prefs.getAssetToken()
        return StreamTokenVault.register("$server$streamPath", token)
    }
}
