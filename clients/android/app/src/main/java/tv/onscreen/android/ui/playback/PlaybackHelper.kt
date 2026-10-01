package tv.onscreen.android.ui.playback

import android.net.Uri
import androidx.leanback.media.PlayerAdapter
import androidx.media3.common.Player
import tv.onscreen.android.data.model.ItemFile

/**
 * Decides the playback strategy for a given file on Android TV.
 *
 * ExoPlayer handles far more codecs natively than a browser:
 * - Video: H.264, H.265 (hardware on most devices), VP9, AV1 (where the
 *   device decodes it in hardware)
 * - Audio: AAC, MP3, Opus, FLAC, Vorbis, AC3, EAC3 (passthrough), DTS and
 *   TrueHD (where the device decodes them or the output takes them)
 * - Containers: MP4, MKV, WebM, MOV, TS
 *
 * So direct play covers the vast majority of content.
 */
sealed class PlaybackMode {
    /** Play the raw file via HTTP range requests. */
    data object DirectPlay : PlaybackMode()

    /** Remux: copy video, transcode audio only → HLS. */
    data object Remux : PlaybackMode()

    /** Full transcode at the given resolution → HLS. */
    data class Transcode(val height: Int) : PlaybackMode()
}

object PlaybackHelper {

    /** Plus "av1" on a device with a hardware AV1 decoder ([supportsAv1]). */
    private val directPlayVideoCodecs = setOf(
        "h264", "hevc", "h265", "vp9",
    )

    private val directPlayContainers = setOf(
        "mp4", "mkv", "matroska", "webm", "mov",
    )

    /** Video codecs ExoPlayer can play but that may need container remux.
     *  Plus "av1", as for [directPlayVideoCodecs]. */
    private val remuxVideoCodecs = setOf(
        "h264", "hevc", "h265", "vp9",
    )

    /** The play mode for the server's decision [verdict] ("directPlay",
     *  "directStream", "transcode"), falling back to the local [decide] when
     *  the server gave none. Null for "unsupported" (Dolby Vision), which
     *  plays nowhere. Shared by PlaybackViewModel.prepare and the background
     *  service's chain to the next track. */
    fun modeFor(verdict: String?, file: ItemFile): PlaybackMode? = when (verdict) {
        "directPlay" -> PlaybackMode.DirectPlay
        "directStream" -> PlaybackMode.Remux
        "transcode" -> PlaybackMode.Transcode(if ((file.resolution_h ?: 1080) >= 2160) 2160 else 1080)
        "unsupported" -> null
        else -> decide(file)
    }

    /** The local decision, for when the server gives none. What this device
     *  plays is read the way the capabilities header reads it, so the two
     *  agree: AV1 only with a hardware decoder, DTS and TrueHD only as
     *  [deviceAudioDecoders] claims them. */
    fun decide(file: ItemFile): PlaybackMode = decide(file, supportsAv1(), deviceAudioDecoders(output))

    /**
     * [decide] for a device that decodes AV1 in hardware or not ([av1]) and
     * plays [audioCodecs]. Both used to be assumed: AV1 direct played (or was
     * remuxed) on boxes with only Android's software AV1 decoder, where 4K
     * stutters, and DTS direct played on boxes with neither a DTS decoder nor
     * an output that takes it, which play the film silently.
     */
    internal fun decide(file: ItemFile, av1: Boolean, audioCodecs: Collection<String>): PlaybackMode {
        val video = file.video_codec?.lowercase()
        val audio = file.audio_codec?.lowercase()
        val container = file.container?.lowercase()

        // Audio-only files — always direct play.
        if (video.isNullOrEmpty()) return PlaybackMode.DirectPlay

        val av1Ok = av1 && video == "av1"
        val videoOk = video in directPlayVideoCodecs || av1Ok
        val audioOk = audio.isNullOrEmpty() || audio in audioCodecs
        val containerOk = container in directPlayContainers

        if (videoOk && audioOk && containerOk) {
            return PlaybackMode.DirectPlay
        }

        // Video codec is compatible but container or audio isn't — remux.
        if (video in remuxVideoCodecs || av1Ok) {
            return PlaybackMode.Remux
        }

        // Everything else needs full transcode. Cap at the PANEL height —
        // asking a 1080p stick's server for a 2160 rung wastes encoder work
        // on rows the display throws away.
        val sourceH = file.resolution_h ?: 1080
        val defaultHeight = (if (sourceH >= 2160) 2160 else 1080)
            .coerceAtMost(displayHeightCap())
        return PlaybackMode.Transcode(defaultHeight)
    }

    // Device decoder inventory, probed once. The capabilities header is built from
    // this so the server only picks a transcode output (HEVC, AV1, 10-bit) the
    // device can actually decode — the old hardcoded `true`s meant a cheap/older
    // box that can't decode the server's chosen HEVC/AV1/10-bit HLS output
    // dead-ended on a hard error dialog (the HLS path has no further fallback).
    private val decoderInfos: List<android.media.MediaCodecInfo> by lazy {
        try {
            android.media.MediaCodecList(android.media.MediaCodecList.REGULAR_CODECS)
                .codecInfos.filter { !it.isEncoder }
        } catch (e: Exception) {
            emptyList()
        }
    }

    private fun hasDecoderFor(mime: String): Boolean =
        decoderInfos.any { info -> info.supportedTypes.any { it.equals(mime, ignoreCase = true) } }

    /** [hasDecoderFor], counting only hardware decoders ([isHardwareCodec]). */
    private fun hasHardwareDecoderFor(mime: String): Boolean =
        decoderInfos.any { info ->
            isHardware(info) && info.supportedTypes.any { it.equals(mime, ignoreCase = true) }
        }

    private fun isHardware(info: android.media.MediaCodecInfo): Boolean = isHardwareCodec(
        info.name,
        if (android.os.Build.VERSION.SDK_INT >= 29) info.isHardwareAccelerated else null,
    )

    /**
     * Whether the decoder [name] runs on dedicated hardware. Android 10+
     * says so itself ([hardwareAccelerated]); before that, the software
     * decoders are known by name: Android's own (OMX.google., c2.android.,
     * c2.google.), FFmpeg's, Samsung's ".sw." ones, and anything outside the
     * OMX. / c2. namespaces (Media3's rule). Secure-only decoders don't count
     * either: they only play DRM streams.
     */
    internal fun isHardwareCodec(name: String, hardwareAccelerated: Boolean?): Boolean {
        val n = name.lowercase()
        if (n.endsWith(".secure")) return false
        if (hardwareAccelerated != null) return hardwareAccelerated
        return !(
            n.startsWith("omx.google.") ||
                n.startsWith("c2.android.") ||
                n.startsWith("c2.google.") ||
                n.startsWith("omx.ffmpeg.") ||
                (n.startsWith("omx.sec.") && n.contains(".sw.")) ||
                (!n.startsWith("omx.") && !n.startsWith("c2."))
            )
    }

    /** Whether any video decoder reports a 10-bit (Main10 / HDR10) profile. Gates
     *  the 10-bit + HDR claims so we don't request output the decoder/panel can't
     *  render (garbled or green frames, or a hard decoder error). */
    private fun supports10Bit(): Boolean {
        val tenBitProfiles = mapOf(
            "video/hevc" to setOf(
                android.media.MediaCodecInfo.CodecProfileLevel.HEVCProfileMain10,
                android.media.MediaCodecInfo.CodecProfileLevel.HEVCProfileMain10HDR10,
                android.media.MediaCodecInfo.CodecProfileLevel.HEVCProfileMain10HDR10Plus,
            ),
            "video/av01" to setOf(
                android.media.MediaCodecInfo.CodecProfileLevel.AV1ProfileMain10,
                android.media.MediaCodecInfo.CodecProfileLevel.AV1ProfileMain10HDR10,
                android.media.MediaCodecInfo.CodecProfileLevel.AV1ProfileMain10HDR10Plus,
            ),
        )
        return decoderInfos.any { info ->
            info.supportedTypes.any { type ->
                val want = tenBitProfiles[type.lowercase()] ?: return@any false
                // AV1 counts only in hardware, as for the av1 claim itself:
                // Android 10+ ships a software AV1 decoder with Main10
                // profiles on every box, which made even an 8-bit-only one
                // claim 10-bit and HDR.
                if (type.equals("video/av01", ignoreCase = true) && !isHardware(info)) return@any false
                try {
                    info.getCapabilitiesForType(type).profileLevels.any { it.profile in want }
                } catch (e: Exception) {
                    false
                }
            }
        }
    }

    /** Whether a HARDWARE VP9 decoder reports Profile 2 (10-bit), the
     *  vp9MaxBitDepth claim. Profile 2 is a decoder profile of its own: an
     *  NVIDIA SHIELD decodes 8-bit VP9 and 10-bit HEVC in hardware, but not
     *  10-bit VP9. Software doesn't count, as for AV1 ([supportsAv1]):
     *  Android's own VP9 decoder reports Profile 2 there too. */
    private fun supportsVp9Profile2(): Boolean =
        decoderInfos.any { info ->
            isHardware(info) && info.supportedTypes.any { type ->
                type.equals("video/x-vnd.on2.vp9", ignoreCase = true) && try {
                    decodesVp9Profile2(info.getCapabilitiesForType(type).profileLevels.map { it.profile })
                } catch (e: Exception) {
                    false
                }
            }
        }

    /** Whether a VP9 decoder's [profiles] (MediaCodecInfo.CodecProfileLevel)
     *  include Profile 2, plain or HDR. */
    internal fun decodesVp9Profile2(profiles: Collection<Int>): Boolean = profiles.any {
        it == android.media.MediaCodecInfo.CodecProfileLevel.VP9Profile2 ||
            it == android.media.MediaCodecInfo.CodecProfileLevel.VP9Profile2HDR ||
            it == android.media.MediaCodecInfo.CodecProfileLevel.VP9Profile2HDR10Plus
    }

    /** Whether the device can decode HEVC (probed from the platform codec list). */
    fun supportsHevc(): Boolean = hasDecoderFor("video/hevc")

    /** Whether the device has an AV1 HARDWARE decoder. v2.1.
     *
     * When true and the source file is AV1, the server prefers the
     * AV1 fMP4 remux path (av01 tag, .m4s segments, #EXT-X-MAP) over
     * an H.264 NVENC/QSV/AMF re-encode — same bytes off disk to the
     * client, no GPU encode work on the server — and may pick AV1 as a
     * transcode output.
     *
     * AV1 hardware decode landed broadly on Android TV devices from
     * 2022 onward (Fire TV Stick 4K Max, Chromecast with Google TV 4K,
     * any TV with MediaTek MT9602 / Realtek RTD2843 / Amlogic S905X4
     * or newer SoC). Older boxes (the Tegra X1 Nvidia Shields among
     * them) have none, and Android 10+ ships a software AV1 decoder on
     * every device: counting it made those boxes claim AV1 and decode
     * it on the CPU, where 4K stutters. Only hardware counts, so they get
     * an H.264 / HEVC transcode instead. */
    fun supportsAv1(): Boolean = hasHardwareDecoderFor("video/av01")

    /**
     * Total duration of the ITEM in content time — the only time base a
     * progress report or completion ratio may be computed in.
     *
     * [playerDurationMs] is NOT that for a resumed HLS session: the session
     * playlist starts at [hlsOffsetMs], so the player reports only the
     * REMAINING duration. Pairing it with a position that has the offset
     * added (which both the progress heartbeat and the Watch Next publisher
     * do) puts the two halves in different time bases — a movie resumed an
     * hour in reports position ≈ duration on its first heartbeat and the
     * server marks it watched.
     *
     * Nor is it while [playerWindowDynamic] (Player.isCurrentMediaItemDynamic):
     * a server remux / transcode is an HLS EVENT playlist that grows as the
     * server writes it, and until its ENDLIST the player's duration is only
     * what exists so far — a few minutes into a film on a fresh session. The
     * phone client, trusting it, marked a movie watched minutes into a resume.
     *
     * The position reported is time in the FILE, so the file's length is
     * the one to divide it by. In order: the player's own duration when it
     * is the file's (direct play, not an [hlsSession], window settled); the
     * length the server lists ([listedDurationMs]: the file's probed one,
     * else the item's runtime); a server session's, re-absolutised, once its
     * playlist has ended. A runtime (TMDB's, in whole minutes) shorter than
     * the file marks an item watched, and puts Up Next (which reads this
     * length too) up, minutes early; a longer one can keep an item from ever
     * reaching watched. Returns 0 when nothing is known, which callers treat
     * as "don't report".
     */
    fun contentDurationMs(
        listedDurationMs: Long?,
        playerDurationMs: Long,
        hlsOffsetMs: Long,
        hlsSession: Boolean,
        playerWindowDynamic: Boolean,
    ): Long {
        // C.TIME_UNSET is negative, so `> 0` also rules out "not known yet";
        // MAX_VALUE guards the unbounded/live shape.
        val settled = !playerWindowDynamic && playerDurationMs > 0L && playerDurationMs != Long.MAX_VALUE
        if (settled && !hlsSession) return playerDurationMs
        if (listedDurationMs != null && listedDurationMs > 0L) return listedDurationMs
        if (settled) return playerDurationMs + hlsOffsetMs
        return 0L
    }

    /**
     * The length the server lists for what is played, for
     * [contentDurationMs]: the file's probed duration, else the item's
     * runtime (the API omits the item's when it has none; chapters and books
     * carry theirs on the file). A zero is no length either. Null when
     * neither is known.
     */
    fun listedDurationMs(fileDurationMs: Long?, itemDurationMs: Long?): Long? =
        fileDurationMs?.takeIf { it > 0L } ?: itemDurationMs?.takeIf { it > 0L }

    /**
     * Whether the Up Next card is due: [contentPositionMs] within [leadMs]
     * of the end ([contentDurationMs], both content time). Never with no
     * known length (0): a server remux's player duration is only the part
     * written so far, which put the card up mid-film on a resumed session.
     */
    fun upNextDue(contentPositionMs: Long, contentDurationMs: Long, leadMs: Long): Boolean {
        if (contentDurationMs <= 0L) return false
        return contentDurationMs - contentPositionMs in 0..leadMs
    }

    /**
     * Whether the Up Next countdown counts this second down: while the player
     * plays, and at the end of the stream too ([Player.STATE_ENDED], where
     * isPlaying is false) unless paused there. It counted only while playing,
     * so the card the end of an episode puts up sat at its first second:
     * nothing moved on to the next episode, and the screensaver came on over
     * the frozen card. A pause holds it: whoever catches the card and pauses
     * has asked to stay.
     */
    fun upNextCountdownTicks(isPlaying: Boolean, playbackState: Int, playWhenReady: Boolean): Boolean =
        isPlaying || (playbackState == Player.STATE_ENDED && playWhenReady)

    /**
     * The remote's Play, and Play/Pause on a player that isn't playing. One an
     * error stopped (IDLE, its dialog dismissed with Back) plays the way the
     * transport bar's Play plays it, through [transport] (the glue's
     * adapter): prepared again, a server session loaded again where it
     * stopped (ContentTimeForwardingPlayer.prepare). The raw player's play()
     * does neither, so the key did nothing there. Any other IDLE player keeps
     * play(): one still waiting for its first source would end at once,
     * prepared with nothing in it (and close the screen). So does one with no
     * [transport]: the fragment passes none once the server refused playback.
     */
    fun playFromKey(player: Player, transport: PlayerAdapter?) {
        if (transport != null && player.playbackState == Player.STATE_IDLE && player.playerError != null) {
            transport.play()
        } else {
            player.play()
        }
    }

    /**
     * Where in a server session's stream (player time) playback starts, so it
     * begins at [requestedMs] (content time) in a stream that opens at
     * [offsetMs]: the web client's desiredStartSec. A remux copies the video,
     * so it opens on the keyframe at or before the request, up to several
     * seconds early, and started at its head it replayed them; a stream that
     * covers the whole file (a pre-encoded ladder, offset 0) started a resume
     * from 0:00. Further in when seg 0's head is silent ([seg0GapMs]: an audio
     * re-encode after a mid-stream start), so the first frame shown comes
     * with sound.
     */
    fun hlsStartMs(requestedMs: Long, offsetMs: Long, seg0GapMs: Long): Long =
        maxOf(requestedMs - offsetMs, seg0GapMs, 0L)

    /**
     * Strips the `token` query parameter from a stream/asset URL before it
     * goes into a user-facing error dialog. The URL is shown so the user (or
     * a tunnel log) can identify which request died — but stream URLs carry
     * the per-session ?token=, and a credential on a TV screen ends up in
     * support photos. Every other query param is kept so the URL stays
     * debuggable. Non-hierarchical URIs (no query to parse) pass through.
     */
    fun sanitizeUriForDisplay(uri: Uri): String {
        if (!uri.isHierarchical || "token" !in uri.queryParameterNames) {
            return uri.toString()
        }
        val cleaned = uri.buildUpon().clearQuery()
        for (name in uri.queryParameterNames) {
            if (name == "token") continue
            uri.getQueryParameters(name).forEach { cleaned.appendQueryParameter(name, it) }
        }
        return cleaned.build().toString()
    }

    /**
     * True when [error] is a load answered 403 or 404 — how a stream the
     * server stopped (admin stop from Now Playing) dies: the terminated
     * session's playlist/segments 404, and a stopped direct play / remux is
     * refused 403 for the stop window. The player then probes the progress
     * route once to tell a stop apart from an ordinary broken stream.
     */
    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    fun isStoppedStreamStatus(error: Throwable): Boolean {
        var cause: Throwable? = error
        while (cause != null) {
            if (cause is androidx.media3.datasource.HttpDataSource.InvalidResponseCodeException) {
                return cause.responseCode == 403 || cause.responseCode == 404
            }
            cause = cause.cause
        }
        return false
    }

    /**
     * Builds the X-Client-Capabilities header value from this device's decode
     * support — the declarative profile the server uses for transcode target
     * selection and (once adopted) the POST /items/{id}/playback-decision
     * endpoint. Built from the same supportsHevc()/supportsAv1() + codec sets
     * that decide() and the transcode request use, so it stays consistent with
     * what this client claims. ExoPlayer decodes up to 7.1, so maxAudioChannels
     * is 8 (the AAC transcode fallback still caps at 5.1 server-side). See
     * docs/capability-profiles.md for the grammar.
     */
    /**
     * What the screen and audio output take, as far as the header goes. All of
     * it can change while the app runs (a receiver switched off, the stick
     * moved to another TV), unlike the decoder inventory.
     *
     * The defaults are the claims from before any of this was read: the old
     * 4K claim for the unlikely path where [initDisplayCaps] never ran (a
     * 1080p Fire TV stick used to claim maxHeight=2160 unconditionally, the
     * server then direct-played 4K files the panel couldn't show and the
     * stick couldn't smoothly decode), HDR as the decoder allows, no DTS. No
     * TrueHD and no HLG either, until the output or the screen says so.
     */
    private data class Output(
        /** The audio output takes 8-channel DTS as a bitstream ([onAudioOutputChanged]). */
        val dts: Boolean = false,
        /** The audio output takes 8-channel TrueHD as a bitstream ([onAudioOutputChanged]). */
        val truehd: Boolean = false,
        val width: Int = 3840,
        val height: Int = 2160,
        /** The screen shows HDR10 ([displayShowsHdr10]). */
        val hdr: Boolean = true,
        /** The screen shows HLG ([displayShowsHlg]). */
        val hlg: Boolean = false,
    )

    @Volatile private var output = Output()
    private val outputLock = Any()

    private fun updateOutput(change: (Output) -> Output) {
        synchronized(outputLock) { output = change(output) }
    }

    /** The capabilities header, cached.
     *
     *  AuthInterceptor attaches this to EVERY authenticated request.
     *  Rebuilding it per request meant several list allocations plus a
     *  codec-profile scan on every API call. The decoder inventory behind it
     *  can't change while the process is alive; the [Output] can, so the
     *  cache is per output state. */
    private class CachedHeader(val output: Output, val value: String)

    @Volatile private var capabilitiesHeader: CachedHeader? = null
    private val headerLock = Any()

    fun clientCapabilitiesHeader(): String {
        // One read of the output state: a change landing mid-build then gets
        // a header of its own on the next request instead of being
        // overwritten by one built for the old state.
        val out = output
        capabilitiesHeader?.let { if (it.output == out) return it.value }
        // One build at a time: the app's first screen fires several requests
        // at once, and each built its own (five at every start).
        synchronized(headerLock) {
            capabilitiesHeader?.let { if (it.output == out) return it.value }
            val header = buildClientCapabilitiesHeader(out)
            capabilitiesHeader = CachedHeader(out, header)
            // What this device tells the server it plays: the first thing to
            // check when the server's decision surprises.
            if (tv.onscreen.android.BuildConfig.DEBUG) android.util.Log.i("PlaybackHelper", "capabilities: $header")
            return header
        }
    }

    /**
     * The audio output changed (a receiver switched on or off, the TV's
     * surround setting, a different HDMI sink): what it takes as a bitstream
     * decides the DTS and TrueHD claims, so the header is rebuilt when that
     * changes. Registered at app start (OnScreenApp) through Media3's
     * AudioCapabilitiesReceiver, which reads the HDMI sink the same way the
     * player's audio sink does.
     */
    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    fun onAudioOutputChanged(audio: androidx.media3.exoplayer.audio.AudioCapabilities) {
        // The player's own passthrough check, not just "DTS is in the
        // output's list": it also checks the channel count, and a sink that
        // takes DTS but fewer channels than a film has refuses it. With no
        // DTS decoder to fall back on, that film played silently (no audio
        // track selected, no error). 8 channels: what the header claims.
        fun passthrough(mime: String) = audio.isPassthroughPlaybackSupported(
            androidx.media3.common.Format.Builder()
                .setSampleMimeType(mime)
                .setChannelCount(8)
                .setSampleRate(48_000)
                .build(),
            androidx.media3.common.AudioAttributes.DEFAULT,
        )
        val dts = passthrough(androidx.media3.common.MimeTypes.AUDIO_DTS)
        val truehd = passthrough(androidx.media3.common.MimeTypes.AUDIO_TRUEHD)
        if (dts == output.dts && truehd == output.truehd) return
        android.util.Log.i(
            "PlaybackHelper",
            "audio output takes as a bitstream: 8-channel DTS ${if (dts) "yes" else "no"}, 8-channel TrueHD ${if (truehd) "yes" else "no"}",
        )
        updateOutput { it.copy(dts = dts, truehd = truehd) }
    }

    /**
     * The audio codecs to claim. DTS plays one of two ways: decoded here (a
     * DTS decoder in the codec list), or passed through as a bitstream to an
     * output that decodes it, which Media3 prefers whenever the output takes
     * it. Most Android TV boxes (Fire TV sticks, Nvidia Shields) have no DTS
     * decoder at all, so claiming DTS only for a decoder made the server
     * convert DTS for them even with a DTS receiver attached.
     *
     * TrueHD plays only the second way (Android ships no TrueHD decoder), so
     * it is claimed only for an output that takes it: a Shield on an Atmos
     * receiver then direct-plays a Blu-ray remux's TrueHD track instead of
     * getting it converted to AAC.
     */
    internal fun audioDecoders(dtsDecoder: Boolean, dtsOutput: Boolean, truehdOutput: Boolean): List<String> {
        val audio = mutableListOf("aac", "mp3", "opus", "flac", "vorbis", "ac3", "eac3")
        if (dtsDecoder || dtsOutput) audio.add("dts")
        if (truehdOutput) audio.add("truehd")
        return audio
    }

    /** [audioDecoders] for this device and the output state [out]. */
    private fun deviceAudioDecoders(out: Output): List<String> =
        audioDecoders(hasDecoderFor("audio/vnd.dts"), out.dts, out.truehd)

    /**
     * Read the screen now (at app start, before any request builds the
     * header) and again whenever it changes: an HDMI box moved to another TV,
     * the TV's HDR setting, a resolution change. Main thread.
     */
    fun initDisplayCaps(context: android.content.Context) {
        val app = context.applicationContext
        readDisplay(app)
        val dm = app.getSystemService(android.hardware.display.DisplayManager::class.java) ?: return
        dm.registerDisplayListener(
            object : android.hardware.display.DisplayManager.DisplayListener {
                override fun onDisplayChanged(displayId: Int) {
                    if (displayId == android.view.Display.DEFAULT_DISPLAY) readDisplay(app)
                }
                override fun onDisplayAdded(displayId: Int) = Unit
                override fun onDisplayRemoved(displayId: Int) = Unit
            },
            android.os.Handler(android.os.Looper.getMainLooper()),
        )
    }

    /**
     * The panel size, from the larger of the display mode and Media3's view of
     * the video output. Many TVs draw their menus smaller than the panel (a
     * 1080p Hisense Google TV runs its UI at 1280x720) and report that as the
     * display mode, while video goes out at the panel's size: its real size
     * is in vendor.display-size / sys.display-size, which Media3's
     * getCurrentDisplayModeSize reads on TVs. Claiming the UI size made the
     * server transcode every 1080p file down to 720p there.
     *
     * And which HDR the screen shows ([displayShowsHdr10], [displayShowsHlg]).
     */
    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    private fun readDisplay(context: android.content.Context) {
        try {
            val wm = context.getSystemService(android.content.Context.WINDOW_SERVICE)
                as? android.view.WindowManager ?: return
            @Suppress("DEPRECATION")
            val display = wm.defaultDisplay ?: return
            val mode = display.mode
            val videoOut = try {
                androidx.media3.common.util.Util.getCurrentDisplayModeSize(context)
            } catch (_: Exception) {
                null
            }
            val (w, h) = if (videoOut != null && videoOut.x.toLong() * videoOut.y > mode.physicalWidth.toLong() * mode.physicalHeight) {
                videoOut.x to videoOut.y
            } else {
                mode.physicalWidth to mode.physicalHeight
            }
            @Suppress("DEPRECATION")
            val hdrTypes = screenHdrTypes(
                reported = try {
                    display.hdrCapabilities?.supportedHdrTypes
                } catch (_: Exception) {
                    null
                },
                sdk = android.os.Build.VERSION.SDK_INT,
            )
            val hdr = displayShowsHdr10(hdrTypes)
            val hlg = displayShowsHlg(hdrTypes, android.os.Build.MANUFACTURER)
            val before = output
            updateOutput {
                if (w > 0 && h > 0) it.copy(width = w, height = h, hdr = hdr, hlg = hlg) else it.copy(hdr = hdr, hlg = hlg)
            }
            if (output != before) {
                android.util.Log.i(
                    "PlaybackHelper",
                    "screen: ${output.width}x${output.height}, HDR types ${hdrTypes?.joinToString() ?: "unknown"}: " +
                        "HDR10 ${if (hdr) "yes" else "no"}, HLG ${if (hlg) "yes" else "no"} " +
                        "(the server tone-maps HDR sources the screen doesn't show)",
                )
            }
        } catch (_: Exception) {
            // Keep what we had — over-claiming is the old behaviour.
        }
    }

    /**
     * The screen's HDR types from what Display.getHdrCapabilities() [reported].
     * No capabilities at all means "can't say" before Android 16, and keeps
     * the old claim (null). From Android 16 (API 36) it means the user forced
     * SDR (DisplayInfo.isForceSdr): no HDR types.
     */
    internal fun screenHdrTypes(reported: IntArray?, sdk: Int): IntArray? =
        reported ?: if (sdk >= 36) IntArray(0) else null

    /**
     * Whether a screen reporting [hdrTypes] ([screenHdrTypes]) shows HDR10,
     * the server's hdr claim (HDR10 and HDR10+ sources; HLG has its own,
     * [displayShowsHlg]). An empty list is a screen that shows none: HDR
     * sent to it came out washed out, grey and dim, where the server would
     * have tone-mapped it to SDR. Null, a platform that can't say, keeps the
     * claim the decoder allows, as before.
     */
    internal fun displayShowsHdr10(hdrTypes: IntArray?): Boolean =
        hdrTypes == null || hdrTypes.any { it == HDR_TYPE_HDR10 || it == HDR_TYPE_HDR10_PLUS }

    /**
     * Whether a screen reporting [hdrTypes] shows HLG, the server's hlg
     * claim: only when it lists HLG, so a platform that can't say gets HLG
     * tone-mapped to SDR. Never through an NVIDIA SHIELD ([manufacturer]
     * NVIDIA), whatever the TV reports: it outputs HDR10, but sends HLG
     * flagged as SDR, and the TV shows it as such.
     */
    internal fun displayShowsHlg(hdrTypes: IntArray?, manufacturer: String?): Boolean =
        !manufacturer.equals("NVIDIA", ignoreCase = true) && hdrTypes?.contains(HDR_TYPE_HLG) == true

    /** Display.HdrCapabilities.HDR_TYPE_HDR10 / HDR_TYPE_HLG /
     *  HDR_TYPE_HDR10_PLUS (API 29 for the last; the values are what matter
     *  on older ones). */
    private const val HDR_TYPE_HDR10 = 2
    private const val HDR_TYPE_HLG = 3
    private const val HDR_TYPE_HDR10_PLUS = 4

    /** Height ceiling for transcode requests: never ask for more rows than
     *  the panel has. */
    fun displayHeightCap(): Int = output.height

    private fun buildClientCapabilitiesHeader(out: Output): String {
        val video = mutableListOf("h264", "vp9")
        if (supportsHevc()) video.add("h265")
        if (supportsAv1()) video.add("av1")
        // DTS is probed too — claiming it unconditionally made the server pick a
        // DTS passthrough/output a box that can neither decode nor pass it on
        // couldn't play. See audioDecoders.
        val audio = deviceAudioDecoders(out)
        val keys = listOf(
            "videoDecoder=" + video.joinToString(":"),
            "audioDecoder=" + audio.joinToString(":"),
            // Raw-audio containers must be listed too, or the server can't
            // DirectPlay a music file (e.g. a .flac track): audioDecoder=flac
            // says we decode the codec, but the play decision also checks the
            // CONTAINER, and an audio-only source in a flac/mp3/ogg/wav/aac
            // container would otherwise fall to a (broken) audio-only transcode.
            // ExoPlayer plays all of these natively, so claim them for passthrough.
            "protocols=mp4:mkv:webm:mov:ts:flac:mp3:ogg:wav:aac:aiff:m4a",
            "maxWidth=${out.width}",
            "maxHeight=${out.height}",
            "maxAudioChannels=8",
        )
        return (keys + depthAndHdrKeys(supports10Bit(), supportsVp9Profile2(), out.hdr, out.hlg)).joinToString(",")
    }

    /**
     * The header's bit-depth and HDR keys (docs/capability-profiles.md §5.1),
     * from what the decoders report ([tenBit]: a Main10 profile,
     * [vp9Profile2]: a hardware VP9 Profile 2 one) and what the screen shows
     * ([hdr10], [hlg]).
     */
    internal fun depthAndHdrKeys(tenBit: Boolean, vp9Profile2: Boolean, hdr10: Boolean, hlg: Boolean): List<String> = listOf(
        // 10-bit only when a decoder actually reports a Main10/HDR profile;
        // HDR only when the screen shows it too. A 10-bit SDR file still
        // plays on an SDR screen, but HDR sent to one came out washed
        // out: the server tone-maps it instead when this says 0.
        "maxbitdepth=" + if (tenBit) "10" else "8",
        // VP9's own ceiling (the server reads maxbitdepth for HEVC only):
        // without it the server takes 10-bit VP9 for playable wherever VP9 is.
        "vp9MaxBitDepth=" + if (vp9Profile2) "10" else "8",
        "hdr=" + if (tenBit && hdr10) "1" else "0",
        // HLG answered on its own, always: without it the server lets hdr
        // decide HLG, and an HDR10 screen isn't necessarily an HLG one (nor
        // is any screen behind a SHIELD, see displayShowsHlg).
        "hlg=" + if (tenBit && hlg) "1" else "0",
    )
}
