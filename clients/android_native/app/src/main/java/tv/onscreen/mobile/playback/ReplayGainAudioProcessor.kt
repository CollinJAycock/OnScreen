package tv.onscreen.mobile.playback

import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor.AudioFormat
import androidx.media3.common.audio.AudioProcessor.StreamMetadata
import androidx.media3.common.audio.BaseAudioProcessor
import java.nio.ByteBuffer
import kotlin.math.roundToInt

/**
 * The ReplayGain volume stage of the music player's audio pipeline.
 *
 * Why an AudioProcessor and not `player.volume` set from
 * `onMediaItemTransition`: in a gapless queue the next track's samples are
 * decoded and handed to the AudioTrack a few hundred milliseconds before the
 * transition event reaches the app thread, and the volume call then travels
 * back to the playback thread — so the first part of every track would play
 * at the PREVIOUS track's gain (a loud-to-quiet album jump is audible). This
 * stage instead sits in the sink's processing chain, and
 * [ReplayGainAudioRenderer] swaps its tags on the playback thread at the exact
 * buffer where the decoder's output moves to the next stream — so every sample
 * of a track gets that track's gain. (`player.volume` also can't boost: it
 * clamps to [0, 1], and a peak-safe positive gain is part of ReplayGain.)
 *
 * Behaviour:
 *  - Unity gain (mode Off, untagged file, or tags that net to 0 dB) is a
 *    straight byte copy — bit-transparent, so turning the feature on costs
 *    nothing for files it doesn't touch.
 *  - A new track's gain applies from its first sample ([setStreamTags] snaps).
 *  - A settings change mid-track ramps over ~20 ms instead of stepping, so
 *    moving the preamp slider doesn't click.
 *  - 16-bit output is requantised with TPDF dither (±1 LSB triangular noise)
 *    whenever the gain isn't unity, the textbook way to scale integer PCM
 *    without truncation distortion. Float PCM needs none.
 *
 * DefaultAudioSink only runs custom processors on its integer-PCM path (float
 * output is off by default), after its own trimming stage — so this sees
 * 16-bit PCM with encoder delay/padding already removed. Float is handled too
 * for completeness; any other encoding leaves the stage inactive.
 *
 * Thread-safety: [setStreamTags] / [setSettings] are called from the playback
 * thread and the main thread respectively; the audio thread reads the
 * @Volatile fields once per buffer.
 */
class ReplayGainAudioProcessor(
    /** Tests switch dither off to assert exact sample values. */
    private val dither: Boolean = true,
    /** Called on the audio thread whenever the gain being applied changes
     *  (new track, new settings): the applied level in dB, or null when
     *  ReplayGain isn't acting on this track (mode off, or no usable tag).
     *  Drives the now-playing screen's "ReplayGain" readout. */
    private val onAppliedChanged: ((Double?) -> Unit)? = null,
) : BaseAudioProcessor() {

    /** Mode + preamp, replaced atomically as one immutable pair. */
    data class Settings(val mode: ReplayGainMode, val preampDb: Double) {
        companion object {
            val OFF = Settings(ReplayGainMode.OFF, 0.0)
        }
    }

    @Volatile private var settings: Settings = Settings.OFF
    @Volatile private var tags: ReplayGainInfo? = null
    /** Set with new stream tags: the next buffer jumps straight to the new
     *  gain (a track boundary) rather than ramping to it. */
    @Volatile private var snapPending = true

    // Audio-thread state.
    private var currentGain = 1.0
    private var rampTarget = 1.0
    private var rampStep = 0.0
    private var rampFramesLeft = 0
    private var cachedFor: Pair<Settings, ReplayGainInfo?>? = null
    private var cachedTarget = 1.0
    private var rng = 0x2545F491L

    /** The stream that just reached the output — called by the renderer on
     *  the playback thread at the buffer boundary. Null = unknown item,
     *  treated like an untagged file (unity). */
    fun setStreamTags(tags: ReplayGainInfo?) {
        this.tags = tags
        snapPending = true
    }

    /** New user settings; takes effect on the next buffer, ramped. */
    fun setSettings(settings: Settings) {
        this.settings = settings
    }

    /** The gain the current settings + tags resolve to (for tests / logs). */
    fun targetGain(): Double = ReplayGain.linear(tags, settings.mode, settings.preampDb)

    override fun onConfigure(inputAudioFormat: AudioFormat): AudioFormat =
        when (inputAudioFormat.encoding) {
            C.ENCODING_PCM_16BIT, C.ENCODING_PCM_FLOAT -> inputAudioFormat
            // Not an error: an encoding we don't scale just bypasses the stage.
            else -> AudioFormat.NOT_SET
        }

    override fun queueInput(inputBuffer: ByteBuffer) {
        val size = inputBuffer.remaining()
        if (size == 0) return
        val out = replaceOutputBuffer(size)
        updateTarget()
        if (rampFramesLeft == 0 && currentGain == 1.0) {
            // Unity and not ramping: untouched bytes.
            out.put(inputBuffer)
            out.flip()
            return
        }
        val channels = inputAudioFormat.channelCount.coerceAtLeast(1)
        when (inputAudioFormat.encoding) {
            C.ENCODING_PCM_16BIT -> {
                while (inputBuffer.remaining() >= 2 * channels) {
                    val g = nextFrameGain()
                    repeat(channels) {
                        val s = inputBuffer.getShort().toDouble()
                        out.putShort(requantize16(s * g, g != 1.0))
                    }
                }
            }
            C.ENCODING_PCM_FLOAT -> {
                while (inputBuffer.remaining() >= 4 * channels) {
                    val g = nextFrameGain().toFloat()
                    repeat(channels) { out.putFloat(inputBuffer.getFloat() * g) }
                }
            }
            // onConfigure leaves the stage inactive for anything else, so the
            // sink never queues other encodings here.
            else -> Unit
        }
        // A partial trailing frame can't happen with well-formed PCM; copy it
        // through rather than drop bytes if it ever does.
        while (inputBuffer.hasRemaining()) out.put(inputBuffer.get())
        out.flip()
    }

    /** Recompute the target when settings or tags changed; snap on a stream
     *  change, otherwise start a short ramp. */
    private fun updateTarget() {
        val s = settings
        val t = tags
        val key = s to t
        if (key != cachedFor) {
            cachedFor = key
            cachedTarget = ReplayGain.linear(t, s.mode, s.preampDb)
            onAppliedChanged?.invoke(appliedDb(t, s, cachedTarget))
        }
        val target = cachedTarget
        if (snapPending) {
            snapPending = false
            currentGain = target
            rampTarget = target
            rampFramesLeft = 0
            return
        }
        if (target != rampTarget) {
            rampTarget = target
            val frames = (inputAudioFormat.sampleRate * RAMP_SECONDS).roundToInt().coerceAtLeast(1)
            rampStep = (target - currentGain) / frames
            rampFramesLeft = frames
        }
    }

    private fun appliedDb(t: ReplayGainInfo?, s: Settings, linear: Double): Double? =
        if (ReplayGain.select(t, s.mode) == null) null else 20 * kotlin.math.log10(linear)

    private fun nextFrameGain(): Double {
        if (rampFramesLeft > 0) {
            rampFramesLeft--
            currentGain = if (rampFramesLeft == 0) rampTarget else currentGain + rampStep
        }
        return currentGain
    }

    private fun requantize16(value: Double, scaled: Boolean): Short {
        val v = if (dither && scaled) value + tpdf() else value
        val r = Math.round(v)
        return r.coerceIn(Short.MIN_VALUE.toLong(), Short.MAX_VALUE.toLong()).toInt().toShort()
    }

    /** Triangular noise in (-1, 1) LSB: the sum of two uniforms. xorshift64
     *  — this runs per sample on the audio thread, so no locks or allocation. */
    private fun tpdf(): Double = uniform() - uniform()

    private fun uniform(): Double {
        var x = rng
        x = x xor (x shl 13)
        x = x xor (x ushr 7)
        x = x xor (x shl 17)
        rng = x
        return (x ushr 11).toDouble() / (1L shl 53).toDouble()
    }

    override fun onFlush(streamMetadata: StreamMetadata) {
        // A flush (seek / reconfigure) keeps the stream's tags; any ramp in
        // flight just lands.
        currentGain = rampTarget
        rampFramesLeft = 0
    }

    override fun onReset() {
        // Settings are the user's and outlive a sink reset (the service pushes
        // them once); the tags come back with the next stream.
        tags = null
        snapPending = true
        currentGain = 1.0
        rampTarget = 1.0
        rampFramesLeft = 0
        cachedFor = null
        cachedTarget = 1.0
    }

    companion object {
        /** Length of the settings-change ramp. */
        const val RAMP_SECONDS = 0.02
    }
}
