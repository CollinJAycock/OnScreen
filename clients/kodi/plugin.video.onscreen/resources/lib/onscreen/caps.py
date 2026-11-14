"""The X-Client-Capabilities header Kodi sends (docs/capability-profiles.md).

Kodi's player is ffmpeg: it demuxes every container the server indexes and
decodes every audio codec in software, downmixing to the speakers itself. So
the header is wide, and the server direct-plays almost everything. What
can't be claimed blindly is what the device's video hardware and display
take: resolution, HEVC / AV1, HDR, Dolby Vision. Those come from the add-on
settings and Kodi's own reports (resources/lib/kodi/device.py).

maxAudioChannels stays 8 whatever the speakers are: Kodi downmixes a 7.1
track itself, and a lower number would make the server remux a file Kodi
plays directly. audioPassthrough lists only AC-3 / E-AC-3: they ride the
MPEG-TS segments the add-on asks for, and where the server would otherwise
encode a TrueHD or DTS track to AAC in a transcode it encodes E-AC-3 for the
receiver instead. TrueHD / DTS passthrough would make the server package
those sessions as fMP4, which not every Kodi HLS path plays.
"""

# ffprobe's codec names (the server compares lower-cased names; h265 and
# hevc are the same codec to it).
_BASE_VIDEO = ("h264",)
_EXTRA_VIDEO = (
    "vp8",
    "mpeg2video",
    "mpeg1video",
    "mpeg4",
    "msmpeg4v3",
    "vc1",
    "wmv3",
    "theora",
)

_AUDIO = (
    "aac",
    "mp3",
    "ac3",
    "eac3",
    "dts",
    "truehd",
    "flac",
    "alac",
    "opus",
    "vorbis",
    "wmav2",
    "wmapro",
    "pcm_s16le",
    "pcm_s24le",
    "pcm_s32le",
    "pcm_s16be",
    "pcm_bluray",
    "pcm_dvd",
)

# The first name ffprobe gives the container, as the scanner stores it
# (mov covers mp4 / m4v; mpeg is a program stream: .mpg / .vob).
_CONTAINERS = ("mkv", "mp4", "ts", "avi", "asf", "mpeg", "flv", "ogg", "webm")

RESOLUTIONS = {
    "2160": (3840, 2160),
    "1080": (1920, 1080),
    "720": (1280, 720),
}

DOVI_PROFILES = ("5", "8.1", "8.4")


class DeviceCaps:
    """What this Kodi plays. Defaults are a 4K HDR-less device with HEVC,
    AV1 and VP9 decoding (any Kodi 20+ decodes them, in software if need
    be)."""

    def __init__(
        self,
        max_width=3840,
        max_height=2160,
        hevc=True,
        av1=True,
        vp9=True,
        max_bit_depth=10,
        hdr10=False,
        hlg=False,
        dovi=(),
        passthrough=(),
    ):
        self.max_width = max_width
        self.max_height = max_height
        self.hevc = hevc
        self.av1 = av1
        self.vp9 = vp9
        self.max_bit_depth = max_bit_depth
        self.hdr10 = hdr10
        self.hlg = hlg
        self.dovi = tuple(dovi)
        self.passthrough = tuple(passthrough)


def header(caps):
    """The header value for caps."""
    video = list(_BASE_VIDEO)
    if caps.hevc:
        video.append("h265")
    if caps.av1:
        video.append("av1")
    if caps.vp9:
        video.append("vp9")
    video.extend(_EXTRA_VIDEO)
    parts = [
        "videoDecoder=" + ":".join(video),
        "audioDecoder=" + ":".join(_AUDIO),
        "protocols=" + ":".join(_CONTAINERS),
        "maxWidth=%d" % caps.max_width,
        "maxHeight=%d" % caps.max_height,
        "maxAudioChannels=8",
        "maxbitdepth=%d" % (caps.max_bit_depth if caps.hevc else 8),
    ]
    if caps.vp9:
        parts.append("vp9MaxBitDepth=%d" % caps.max_bit_depth)
    if caps.hdr10:
        parts.append("hdr=1")
        parts.append("hlg=%d" % (1 if caps.hlg else 0))
    else:
        parts.append("hdr=0")
    dovi = [p for p in DOVI_PROFILES if p in caps.dovi]
    if dovi:
        parts.append("dovi=" + ":".join(dovi))
    passthrough = [c for c in ("ac3", "eac3") if c in caps.passthrough]
    if passthrough:
        parts.append("audioPassthrough=" + ":".join(passthrough))
    parts.append("optimized=1")
    parts.append("vodRemux=1")
    return ",".join(parts)


def parse_hdr_types(label):
    """Kodi's System.SupportedHDRTypes info label ("HDR10, HLG, Dolby
    Vision" on a capable display, "" otherwise) as (hdr10, hlg, dovi)."""
    s = (label or "").lower().replace(" ", "")
    return ("hdr10" in s, "hlg" in s, "dolbyvision" in s)
