package tv.onscreen.mobile.playback

import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor.AudioFormat
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.abs
import kotlin.math.pow
import kotlin.math.roundToInt

class ReplayGainAudioProcessorTest {

    private val stereo16 = AudioFormat(44_100, 2, C.ENCODING_PCM_16BIT)
    private fun db(x: Double) = 10.0.pow(x / 20)

    private fun processor(
        format: AudioFormat = stereo16,
        dither: Boolean = false,
        settings: ReplayGainAudioProcessor.Settings = ReplayGainAudioProcessor.Settings(ReplayGainMode.TRACK, 0.0),
        tags: ReplayGainInfo? = null,
    ) = ReplayGainAudioProcessor(dither = dither).apply {
        configure(format)
        flush()
        setSettings(settings)
        setStreamTags(tags)
    }

    private fun pcm16(samples: List<Int>): ByteBuffer =
        ByteBuffer.allocateDirect(samples.size * 2).order(ByteOrder.nativeOrder()).apply {
            samples.forEach { putShort(it.toShort()) }
            flip()
        }

    private fun ReplayGainAudioProcessor.run16(samples: List<Int>): List<Int> {
        queueInput(pcm16(samples))
        val out = output
        return List(out.remaining() / 2) { out.getShort().toInt() }
    }

    @Test
    fun `mode off is a bit-exact passthrough`() {
        val p = processor(
            settings = ReplayGainAudioProcessor.Settings.OFF,
            tags = ReplayGainInfo(trackGain = -9.0, trackPeak = 0.4),
        )
        val input = listOf(0, 1, -1, 12345, -32768, 32767)
        assertThat(p.run16(input)).isEqualTo(input)
    }

    @Test
    fun `untagged file is a bit-exact passthrough even with a preamp`() {
        val p = processor(
            settings = ReplayGainAudioProcessor.Settings(ReplayGainMode.ALBUM, 6.0),
            tags = ReplayGainInfo.NONE,
        )
        val input = listOf(100, -100, 30000, -30000)
        assertThat(p.run16(input)).isEqualTo(input)
    }

    @Test
    fun `applies the track gain from the first sample of the stream`() {
        val p = processor(tags = ReplayGainInfo(trackGain = -6.0, trackPeak = 0.9))
        val g = db(-6.0)
        val input = listOf(10000, -10000, 20000, -20000)
        assertThat(p.run16(input)).isEqualTo(input.map { (it * g).roundToInt() })
    }

    @Test
    fun `a new stream's tags snap at the boundary instead of ramping`() {
        val p = processor(tags = ReplayGainInfo(trackGain = -6.0, trackPeak = 0.9))
        p.run16(List(8) { 10000 })
        // Next track: +0 dB. Its very first frame must already be at unity.
        p.setStreamTags(ReplayGainInfo(trackGain = 0.0, trackPeak = 0.5))
        assertThat(p.run16(listOf(10000, 10000, -7, 7))).isEqualTo(listOf(10000, 10000, -7, 7))
    }

    @Test
    fun `album mode uses the album gain and falls back per stream`() {
        val p = processor(
            settings = ReplayGainAudioProcessor.Settings(ReplayGainMode.ALBUM, 0.0),
            tags = ReplayGainInfo(trackGain = -2.0, trackPeak = 0.5, albumGain = -8.0, albumPeak = 0.6),
        )
        assertThat(p.run16(listOf(16000, 16000))).isEqualTo(List(2) { (16000 * db(-8.0)).roundToInt() })
        // A single without album tags: track gain.
        p.setStreamTags(ReplayGainInfo(trackGain = -2.0, trackPeak = 0.5))
        assertThat(p.run16(listOf(16000, 16000))).isEqualTo(List(2) { (16000 * db(-2.0)).roundToInt() })
    }

    @Test
    fun `a settings change mid-track ramps to the new gain`() {
        val tags = ReplayGainInfo(trackGain = -6.0, trackPeak = 0.9)
        val p = processor(tags = tags)
        p.run16(List(4) { 20000 }) // at -6 dB
        p.setSettings(ReplayGainAudioProcessor.Settings(ReplayGainMode.OFF, 0.0)) // → unity

        val rampFrames = (44_100 * ReplayGainAudioProcessor.RAMP_SECONDS).roundToInt()
        val frames = rampFrames + 10
        val out = p.run16(List(frames * 2) { 20000 })
        val left = out.filterIndexed { i, _ -> i % 2 == 0 }

        // Starts just above the old level, rises monotonically, lands on unity.
        assertThat(left.first()).isGreaterThan((20000 * db(-6.0)).roundToInt())
        assertThat(left.first()).isLessThan(20000)
        assertThat(left.zipWithNext().all { (a, b) -> b >= a }).isTrue()
        assertThat(left.takeLast(10)).containsExactlyElementsIn(List(10) { 20000 })
        // Both channels of a frame get the same gain.
        assertThat(out.chunked(2).all { it[0] == it[1] }).isTrue()
    }

    @Test
    fun `clamps instead of wrapping when samples exceed the tagged peak`() {
        // Tags claim a 0.5 peak (so +6 dB is allowed), but the audio is louder.
        val p = processor(tags = ReplayGainInfo(trackGain = 6.0, trackPeak = 0.5))
        assertThat(p.run16(listOf(30000, -30000))).isEqualTo(listOf(32767, -32768))
    }

    @Test
    fun `tpdf dither stays within one LSB of the exact product`() {
        val p = processor(dither = true, tags = ReplayGainInfo(trackGain = -7.3, trackPeak = 0.9))
        val g = db(-7.3)
        val input = List(2000) { (it * 31) % 20000 - 10000 }
        val out = p.run16(input)
        out.zip(input).forEach { (o, i) -> assertThat(abs(o - i * g)).isAtMost(1.5) }
        // …and it actually dithers (not a plain rounding).
        assertThat(out).isNotEqualTo(input.map { (it * g).roundToInt() })
    }

    @Test
    fun `float pcm is scaled without requantising`() {
        val format = AudioFormat(48_000, 1, C.ENCODING_PCM_FLOAT)
        val p = processor(format = format, tags = ReplayGainInfo(trackGain = -6.0, trackPeak = 0.9))
        val input = floatArrayOf(0.5f, -0.25f, 1.0f)
        val buf = ByteBuffer.allocateDirect(input.size * 4).order(ByteOrder.nativeOrder()).apply {
            input.forEach { putFloat(it) }
            flip()
        }
        p.queueInput(buf)
        val out = p.output
        val g = db(-6.0).toFloat()
        input.forEach { assertThat(out.getFloat()).isWithin(1e-6f).of(it * g) }
    }

    @Test
    fun `encodings it does not scale leave the stage inactive`() {
        val p = ReplayGainAudioProcessor()
        p.configure(AudioFormat(44_100, 2, C.ENCODING_PCM_24BIT))
        p.flush()
        assertThat(p.isActive).isFalse()
        val q = ReplayGainAudioProcessor()
        q.configure(stereo16)
        q.flush()
        assertThat(q.isActive).isTrue()
    }

    @Test
    fun `a flush keeps the stream's tags`() {
        val p = processor(tags = ReplayGainInfo(trackGain = -6.0, trackPeak = 0.9))
        p.run16(listOf(1000, 1000))
        p.flush() // e.g. a seek within the track
        assertThat(p.run16(listOf(10000, 10000))).isEqualTo(List(2) { (10000 * db(-6.0)).roundToInt() })
    }

    @Test
    fun `reports the applied level per track and settings change`() {
        val seen = mutableListOf<Double?>()
        val p = ReplayGainAudioProcessor(dither = false, onAppliedChanged = { seen += it }).apply {
            configure(stereo16)
            flush()
            setSettings(ReplayGainAudioProcessor.Settings(ReplayGainMode.TRACK, 0.0))
            setStreamTags(ReplayGainInfo(trackGain = -6.2, trackPeak = 0.9))
        }
        p.run16(listOf(1, 1))
        p.run16(listOf(1, 1)) // unchanged → no duplicate report
        p.setStreamTags(ReplayGainInfo.NONE) // untagged track
        p.run16(listOf(1, 1))
        p.setStreamTags(ReplayGainInfo(trackGain = -3.0, trackPeak = 0.5))
        p.setSettings(ReplayGainAudioProcessor.Settings(ReplayGainMode.TRACK, 1.5))
        p.run16(listOf(1, 1))
        p.setSettings(ReplayGainAudioProcessor.Settings.OFF)
        p.run16(listOf(1, 1))

        assertThat(seen).hasSize(4)
        assertThat(seen[0]!!).isWithin(1e-9).of(-6.2)
        assertThat(seen[1]).isNull()
        assertThat(seen[2]!!).isWithin(1e-9).of(-1.5)
        assertThat(seen[3]).isNull()
    }

    @Test
    fun `unknown item plays at unity`() {
        val p = processor(tags = null)
        assertThat(p.run16(listOf(1234, -1234))).isEqualTo(listOf(1234, -1234))
        assertThat(p.targetGain()).isEqualTo(1.0)
    }
}
