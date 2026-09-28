package tv.onscreen.mobile.playback

import android.net.Uri
import android.os.Bundle
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import tv.onscreen.mobile.data.model.ChildItem

/**
 * ReplayGain tags riding a queue item's metadata extras — how the UI hands
 * the service the tags of the track it already fetched, so the audio stage
 * knows them from the first sample without a second /items/{id} fetch.
 * Lazily-resolved queue entries don't carry them; the service's resolver
 * records theirs when it fetches the file.
 */
object ReplayGainExtras {
    private const val KEY_PRESENT = "onscreen.rg"
    private const val KEY_TRACK_GAIN = "onscreen.rg.tg"
    private const val KEY_TRACK_PEAK = "onscreen.rg.tp"
    private const val KEY_ALBUM_GAIN = "onscreen.rg.ag"
    private const val KEY_ALBUM_PEAK = "onscreen.rg.ap"

    /** Record [info] (possibly all-null = "file has no tags") on [extras]. */
    fun write(extras: Bundle, info: ReplayGainInfo) {
        extras.putBoolean(KEY_PRESENT, true)
        info.trackGain?.let { extras.putDouble(KEY_TRACK_GAIN, it) }
        info.trackPeak?.let { extras.putDouble(KEY_TRACK_PEAK, it) }
        info.albumGain?.let { extras.putDouble(KEY_ALBUM_GAIN, it) }
        info.albumPeak?.let { extras.putDouble(KEY_ALBUM_PEAK, it) }
    }

    /** The tags on [extras], or null when none were recorded (unknown —
     *  distinct from "recorded, file has none"). */
    fun read(extras: Bundle?): ReplayGainInfo? {
        if (extras == null || !extras.getBoolean(KEY_PRESENT, false)) return null
        fun d(key: String): Double? =
            if (extras.containsKey(key)) extras.getDouble(key).takeIf { it.isFinite() } else null
        return ReplayGainInfo(
            trackGain = d(KEY_TRACK_GAIN),
            trackPeak = d(KEY_TRACK_PEAK),
            albumGain = d(KEY_ALBUM_GAIN),
            albumPeak = d(KEY_ALBUM_PEAK),
        )
    }
}

/** Builds the service's lazily-resolved queue entries (see [MusicQueue]). */
object QueueItems {
    fun placeholder(child: ChildItem, parentId: String): MediaItem {
        val extras = Bundle().apply {
            putString(PlaybackService.EXTRA_TYPE, child.type)
            putString(PlaybackService.EXTRA_PARENT_ID, parentId)
            child.index?.let { putInt(PlaybackService.EXTRA_INDEX, it) }
        }
        return MediaItem.Builder()
            .setUri(Uri.parse(MusicQueue.placeholderUri(child.id)))
            .setMediaId(child.id)
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(child.title)
                    .apply { child.index?.let { setTrackNumber(it) } }
                    .setExtras(extras)
                    .build(),
            )
            .build()
    }
}

/**
 * One-way signals from the background player to whatever UI is showing.
 * Process-wide because the service and the screens share a process but no
 * object graph (the UI reaches the service only through a MediaController).
 */
object BackgroundAudioEvents {

    /** Background audio was stopped by an admin (Now Playing → Stop) for
     *  [itemId]; [message] is the sentence to show. */
    data class AdminStop(val itemId: String, val message: String)

    private val _adminStops = MutableSharedFlow<AdminStop>(extraBufferCapacity = 4)
    val adminStops: SharedFlow<AdminStop> = _adminStops.asSharedFlow()

    fun emitAdminStop(stop: AdminStop) {
        _adminStops.tryEmit(stop)
    }

    private val _replayGainDb = MutableStateFlow<Double?>(null)

    /** The ReplayGain level the background player is applying right now, in
     *  dB — null when it isn't (setting off, untagged track, nothing
     *  playing). Published by the audio stage at each track / settings
     *  change; the now-playing screen shows it. */
    val replayGainDb: StateFlow<Double?> = _replayGainDb.asStateFlow()

    fun publishReplayGainDb(db: Double?) {
        _replayGainDb.value = db
    }
}
