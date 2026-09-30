package tv.onscreen.mobile.playback

import android.content.Context
import android.os.Handler
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MediaItem
import androidx.media3.common.Timeline
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.Renderer
import androidx.media3.exoplayer.audio.AudioRendererEventListener
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.DefaultAudioSink
import androidx.media3.exoplayer.audio.MediaCodecAudioRenderer
import androidx.media3.exoplayer.mediacodec.MediaCodecAdapter
import androidx.media3.exoplayer.mediacodec.MediaCodecSelector
import androidx.media3.exoplayer.source.MediaSource

/**
 * The stock audio renderer, plus one job: tell [ReplayGainAudioProcessor]
 * which queue item the samples it's about to scale belong to, at the exact
 * buffer where that changes.
 *
 * MediaCodecRenderer already tracks that boundary for gapless playback:
 * [onStreamChanged] fires when the renderer starts READING the next item
 * (while the previous one is still being decoded), and
 * [onOutputStreamOffsetUsChanged] fires once the decoder's OUTPUT crosses into
 * it — after the previous item's last buffer went to the sink, before the
 * next item's first. Streams are keyed by their renderer offset, so the item
 * resolved at read time is applied at output time.
 *
 * The item comes from the renderer's own copy of the playlist timeline (the
 * player hands every renderer the timeline on each playlist change), looked
 * up through the MediaPeriodId the stream was enabled with.
 */
class ReplayGainAudioRenderer(
    context: Context,
    codecAdapterFactory: MediaCodecAdapter.Factory,
    mediaCodecSelector: MediaCodecSelector,
    enableDecoderFallback: Boolean,
    eventHandler: Handler?,
    eventListener: AudioRendererEventListener?,
    audioSink: AudioSink,
    private val processor: ReplayGainAudioProcessor,
    private val tagsFor: (MediaItem?) -> ReplayGainInfo?,
) : MediaCodecAudioRenderer(
    context,
    codecAdapterFactory,
    mediaCodecSelector,
    enableDecoderFallback,
    eventHandler,
    eventListener,
    audioSink,
) {
    // Playback-thread only. Bounded: at most the current + one pending
    // stream are live; stale entries (a pending stream a seek discarded) age
    // out.
    private val itemsByOffset = object : LinkedHashMap<Long, MediaItem?>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Long, MediaItem?>?): Boolean =
            size > MAX_TRACKED_STREAMS
    }
    private val period = Timeline.Period()
    private val window = Timeline.Window()

    override fun onStreamChanged(
        formats: Array<out Format>,
        startPositionUs: Long,
        offsetUs: Long,
        mediaPeriodId: MediaSource.MediaPeriodId,
    ) {
        // Record BEFORE super: for the first stream super applies it straight
        // away (onOutputStreamOffsetUsChanged runs inside this call).
        itemsByOffset[offsetUs] = mediaItemFor(mediaPeriodId)
        super.onStreamChanged(formats, startPositionUs, offsetUs, mediaPeriodId)
    }

    override fun onOutputStreamOffsetUsChanged(outputStreamOffsetUs: Long) {
        super.onOutputStreamOffsetUsChanged(outputStreamOffsetUs)
        val item = itemsByOffset[outputStreamOffsetUs]
        val tags = tagsFor(item)
        processor.setStreamTags(tags)
        // Once per track: lets a device test confirm the gain moved at the
        // hand-off (`adb logcat -s ReplayGain`).
        android.util.Log.i(TAG, "output → ${item?.mediaId ?: "?"}: tags=$tags, gain=${processor.targetGain()}")
    }

    private fun mediaItemFor(id: MediaSource.MediaPeriodId?): MediaItem? {
        if (id == null) return null
        return runCatching {
            val tl = timeline
            if (tl.isEmpty) return@runCatching null
            val periodIndex = tl.getIndexOfPeriod(id.periodUid)
            if (periodIndex == C.INDEX_UNSET) return@runCatching null
            val windowIndex = tl.getPeriod(periodIndex, period).windowIndex
            tl.getWindow(windowIndex, window).mediaItem
        }.getOrNull()
    }

    private companion object {
        const val TAG = "ReplayGain"
        const val MAX_TRACKED_STREAMS = 8
    }
}

/**
 * DefaultRenderersFactory with the ReplayGain stage wired in: the audio sink
 * gets [processor] in its processing chain, and the audio renderer is
 * [ReplayGainAudioRenderer]. Everything else (video, text, metadata, the codec
 * adapter) is the stock factory's.
 */
class ReplayGainRenderersFactory(
    context: Context,
    private val processor: ReplayGainAudioProcessor,
    private val tagsFor: (MediaItem?) -> ReplayGainInfo?,
) : DefaultRenderersFactory(context) {

    override fun buildAudioSink(
        context: Context,
        enableFloatOutput: Boolean,
        enableAudioOutputPlaybackParameters: Boolean,
    ): AudioSink =
        DefaultAudioSink.Builder(context)
            // Float output would route around custom processors entirely
            // (DefaultAudioSink only runs them on its integer-PCM path).
            .setEnableFloatOutput(false)
            .setEnableAudioOutputPlaybackParameters(enableAudioOutputPlaybackParameters)
            .setAudioProcessors(arrayOf(processor))
            .build()

    override fun buildAudioRenderers(
        context: Context,
        extensionRendererMode: Int,
        mediaCodecSelector: MediaCodecSelector,
        enableDecoderFallback: Boolean,
        audioSink: AudioSink,
        eventHandler: Handler,
        eventListener: AudioRendererEventListener,
        out: ArrayList<Renderer>,
    ) {
        // Replaces super's single MediaCodecAudioRenderer (no decoder
        // extensions are bundled and extension mode stays OFF, so that's all
        // super would add). Not built-then-swapped: a MediaCodecAudioRenderer
        // registers itself as the sink's listener in its constructor, so a
        // discarded one must never be constructed against this sink.
        out.add(
            ReplayGainAudioRenderer(
                context, codecAdapterFactory, mediaCodecSelector, enableDecoderFallback,
                eventHandler, eventListener, audioSink, processor, tagsFor,
            ),
        )
    }
}
