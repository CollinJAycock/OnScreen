// DSD (DSF / DSDIFF) for the native audio engine.
//
// DSD is a 1-bit stream at 64, 128 or 256 times a 44.1 kHz (or 48 kHz)
// base rate: DSD64 is 2.8224 MHz. Everything here is pure — it touches
// no device, no thread and no network beyond the reader it is handed —
// so all of it is unit-tested at the bottom of this file:
//
//   * parsing DSF and uncompressed DSDIFF (DFF) headers, then reading
//     exactly the sound data the header declares: never DSF's zero
//     padding in the last block, never the ID3 chunk after the data;
//   * seek offsets (whole DSF blocks), so a seek is one HTTP Range
//     request instead of reading everything before the target;
//   * the DSD -> PCM converter. This is the default path: DSD becomes
//     ordinary 24-bit PCM at twice the base rate (88.2 or 96 kHz), which
//     every output (WASAPI shared or exclusive, cpal) plays, and volume
//     and ReplayGain apply to it as to any other file;
//   * DoP (DSD over PCM) framing and the rule for when DoP may be used
//     at all: opt-in, WASAPI exclusive, unity volume, no gain;
//   * reading ReplayGain's TXXX frames from the file's ID3v2 tag.
//
// Level convention: a DSD bit maps to +/-1.0 and the converter has unity
// gain, so DSD's 0 dB reference (50 % modulation) comes out at -6 dBFS.
// That is the usual DSD -> PCM convention: SACD masters may peak up to
// +3 dB above that reference, and the 6 dB of headroom keeps those
// peaks from clipping.

use std::io::{self, Read, Seek, SeekFrom};
use std::sync::OnceLock;

/// DSD "silence": four ones and four zeros, no DC and no energy below an
/// eighth of the DSD rate. A DoP DAC plays it as silence, and the
/// converter turns it into (near) zero — see `idle_pattern_converts_to_silence`.
pub const DSD_IDLE_BYTE: u8 = 0x69;

/// DSF's block size per channel; the spec fixes it at 4096 bytes.
const DSF_BLOCK_BYTES: u32 = 4096;

/// Most channels a DSD file may have here. DSF allows 6; DSDIFF more,
/// but the engine's outputs stop at 8 (7.1).
const MAX_DFF_CHANNELS: u16 = 8;
const MAX_DSF_CHANNELS: u32 = 6;

/// Header bytes a parser reads or skips before the sound data, at most.
/// Real files keep the header area to a few hundred bytes; a crafted one
/// is refused instead of being read without bound.
const MAX_HEADER_BYTES: u64 = 1 << 20;

/// The DSD bit rates the engine plays: 64, 128 and 256 times either base.
const BASE_RATES: [u32; 2] = [44_100, 48_000];
const MULTIPLES: [u32; 3] = [64, 128, 256];

fn classify_rate(rate_hz: u32) -> Option<(u32, u32)> {
    for base in BASE_RATES {
        for multiple in MULTIPLES {
            if rate_hz == base * multiple {
                return Some((base, multiple));
            }
        }
    }
    None
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Container {
    Dsf,
    Dff,
}

/// How a container lays out its DSD bits.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Layout {
    /// DSF: `block_bytes` of channel 0, then of channel 1, and so on,
    /// repeating. `lsb_first`: the oldest bit of each byte is bit 0 (the
    /// spec's "bits per sample = 1"); otherwise bit 7 ("= 8").
    Blocks { block_bytes: u32, lsb_first: bool },
    /// DSDIFF: one byte per channel per frame, interleaved, MSB (oldest
    /// bit) first.
    Interleaved,
}

/// What a DSD file's header says about its sound data.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct DsdStream {
    pub container: Container,
    /// DSD bits per second per channel, e.g. 2_822_400 for DSD64.
    pub rate_hz: u32,
    /// 44_100 or 48_000.
    pub base_hz: u32,
    /// 64, 128 or 256.
    pub multiple: u32,
    pub channels: u16,
    pub layout: Layout,
    /// Absolute byte offset of the first sound-data byte.
    pub data_offset: u64,
    /// DSD samples (bits) per channel that are audio. Never more than the
    /// data chunk holds; for DSF also never more than its sample count,
    /// so the zero padding of the last block is not audio.
    pub samples: u64,
    /// Where to look for an ID3v2 tag: DSF's metadata chunk, or the first
    /// DSDIFF chunk after the sound data (an "ID3 " chunk is one of them
    /// when a tagger wrote one).
    pub tag_offset: Option<u64>,
}

impl DsdStream {
    /// The converter's output rate: twice the base rate (88.2 / 96 kHz).
    pub fn pcm_rate_hz(&self) -> u32 {
        2 * self.base_hz
    }

    /// DoP carries 16 DSD bits per PCM frame: DSD64 -> 176.4 kHz.
    pub fn dop_rate_hz(&self) -> u32 {
        self.rate_hz / 16
    }
}

/// Byte-counting header reader: every read and skip is bounded by
/// MAX_HEADER_BYTES, and `pos` is the absolute file offset.
struct Header<'a, R> {
    r: &'a mut R,
    pos: u64,
}

impl<R: Read> Header<'_, R> {
    fn bytes<const N: usize>(&mut self, what: &str) -> Result<[u8; N], String> {
        if self.pos + N as u64 > MAX_HEADER_BYTES {
            return Err(format!("audio: DSD header area too large (reading {what})"));
        }
        let mut b = [0u8; N];
        self.r
            .read_exact(&mut b)
            .map_err(|e| format!("audio: read {what}: {e}"))?;
        self.pos += N as u64;
        Ok(b)
    }

    fn u16_be(&mut self, what: &str) -> Result<u16, String> {
        Ok(u16::from_be_bytes(self.bytes(what)?))
    }
    fn u32_le(&mut self, what: &str) -> Result<u32, String> {
        Ok(u32::from_le_bytes(self.bytes(what)?))
    }
    fn u32_be(&mut self, what: &str) -> Result<u32, String> {
        Ok(u32::from_be_bytes(self.bytes(what)?))
    }
    fn u64_le(&mut self, what: &str) -> Result<u64, String> {
        Ok(u64::from_le_bytes(self.bytes(what)?))
    }
    fn u64_be(&mut self, what: &str) -> Result<u64, String> {
        Ok(u64::from_be_bytes(self.bytes(what)?))
    }

    fn skip(&mut self, n: u64, what: &str) -> Result<(), String> {
        if self.pos.saturating_add(n) > MAX_HEADER_BYTES {
            return Err(format!("audio: DSD header area too large (skipping {what})"));
        }
        let copied = io::copy(&mut (&mut *self.r).take(n), &mut io::sink())
            .map_err(|e| format!("audio: read {what}: {e}"))?;
        if copied != n {
            return Err(format!("audio: file ends inside {what}"));
        }
        self.pos += n;
        Ok(())
    }
}

/// Parses a DSF or DSDIFF header from the start of the file. On success
/// the reader stands at the first sound-data byte (`data_offset`).
pub fn parse<R: Read>(r: &mut R) -> Result<DsdStream, String> {
    let mut h = Header { r, pos: 0 };
    match &h.bytes::<4>("DSD header")? {
        b"DSD " => parse_dsf(&mut h),
        b"FRM8" => parse_dff(&mut h),
        other => Err(format!("audio: not a DSD file (magic {other:?})")),
    }
}

fn parse_dsf<R: Read>(h: &mut Header<'_, R>) -> Result<DsdStream, String> {
    // "DSD " chunk: size (28), total file size, metadata chunk offset.
    let dsd_size = h.u64_le("DSF header")?;
    let _file_size = h.u64_le("DSF header")?;
    let metadata_offset = h.u64_le("DSF header")?;
    if dsd_size < 28 {
        return Err(format!("audio: bad DSF header size {dsd_size}"));
    }
    h.skip(dsd_size - 28, "DSF header")?;

    if &h.bytes::<4>("DSF fmt chunk")? != b"fmt " {
        return Err("audio: DSF file has no fmt chunk".into());
    }
    let fmt_size = h.u64_le("DSF fmt chunk")?;
    if fmt_size < 52 {
        return Err(format!("audio: bad DSF fmt chunk size {fmt_size}"));
    }
    let _version = h.u32_le("DSF fmt chunk")?;
    let format_id = h.u32_le("DSF fmt chunk")?;
    let _channel_type = h.u32_le("DSF fmt chunk")?;
    let channels = h.u32_le("DSF fmt chunk")?;
    let rate_hz = h.u32_le("DSF fmt chunk")?;
    let bits_per_sample = h.u32_le("DSF fmt chunk")?;
    let sample_count = h.u64_le("DSF fmt chunk")?;
    let block_bytes = h.u32_le("DSF fmt chunk")?;
    let _reserved = h.u32_le("DSF fmt chunk")?;
    h.skip(fmt_size - 52, "DSF fmt chunk")?;

    if format_id != 0 {
        return Err(format!("audio: DSF format {format_id} isn't plain DSD"));
    }
    if !(1..=MAX_DSF_CHANNELS).contains(&channels) {
        return Err(format!("audio: unsupported DSF channel count {channels}"));
    }
    // DSF's "bits per sample" is really the bit order: 1 = LSB first.
    let lsb_first = match bits_per_sample {
        1 => true,
        8 => false,
        other => return Err(format!("audio: unsupported DSF bits per sample {other}")),
    };
    if block_bytes != DSF_BLOCK_BYTES {
        return Err(format!(
            "audio: unsupported DSF block size {block_bytes} (the spec's is {DSF_BLOCK_BYTES})"
        ));
    }
    let (base_hz, multiple) = classify_rate(rate_hz)
        .ok_or_else(|| format!("audio: unsupported DSD rate {rate_hz} Hz"))?;

    // The data chunk follows fmt; skip anything a writer put in between.
    let data_size = loop {
        let id = h.bytes::<4>("DSF chunk")?;
        let size = h.u64_le("DSF chunk")?;
        if &id == b"data" {
            break size;
        }
        if size < 12 {
            return Err(format!("audio: bad DSF chunk size {size}"));
        }
        h.skip(size - 12, "DSF chunk")?;
    };
    if data_size < 12 {
        return Err(format!("audio: bad DSF data chunk size {data_size}"));
    }
    let data_len = data_size - 12;
    let data_offset = h.pos;

    // Only whole block groups are audio, and of those only the first
    // `sample_count` bits per channel: the rest of the last block is
    // zero padding (which would play as a full-scale DC step).
    let group_bytes = u64::from(block_bytes) * u64::from(channels);
    let capacity = (data_len / group_bytes) * u64::from(block_bytes) * 8;
    let samples = if sample_count == 0 {
        capacity
    } else {
        sample_count.min(capacity)
    };
    let data_end = data_offset.saturating_add(data_len);
    Ok(DsdStream {
        container: Container::Dsf,
        rate_hz,
        base_hz,
        multiple,
        channels: channels as u16,
        layout: Layout::Blocks {
            block_bytes,
            lsb_first,
        },
        data_offset,
        samples,
        tag_offset: (metadata_offset >= data_end).then_some(metadata_offset),
    })
}

/// DSDIFF 1.5 (big-endian IFF): FRM8 / "DSD " form with FVER, PROP/SND
/// (FS, CHNL, CMPR, ...) and the sound data chunk. Only uncompressed
/// "DSD " sound data plays; DST-compressed files are refused.
fn parse_dff<R: Read>(h: &mut Header<'_, R>) -> Result<DsdStream, String> {
    let form_size = h.u64_be("DSDIFF header")?;
    if &h.bytes::<4>("DSDIFF header")? != b"DSD " {
        return Err("audio: DSDIFF file isn't a DSD form".into());
    }
    let form_end = 12u64.saturating_add(form_size);

    let mut rate_hz = None;
    let mut channels = None;
    let mut compression: Option<[u8; 4]> = None;
    let data_len = loop {
        let id = h.bytes::<4>("DSDIFF chunk")?;
        let size = h.u64_be("DSDIFF chunk")?;
        // IFF chunks are padded to an even length; the pad byte isn't
        // counted in the size.
        let padded = size.saturating_add(size & 1);
        match &id {
            b"PROP" => {
                if size < 4 {
                    return Err("audio: bad DSDIFF PROP chunk".into());
                }
                if &h.bytes::<4>("DSDIFF PROP chunk")? != b"SND " {
                    h.skip(padded - 4, "DSDIFF PROP chunk")?;
                    continue;
                }
                let mut left = padded - 4;
                while left > 0 {
                    if left < 12 {
                        return Err("audio: bad DSDIFF PROP chunk".into());
                    }
                    let sub = h.bytes::<4>("DSDIFF property")?;
                    let sub_size = h.u64_be("DSDIFF property")?;
                    let sub_padded = sub_size.saturating_add(sub_size & 1);
                    if sub_padded > left - 12 {
                        return Err("audio: DSDIFF property overruns its PROP chunk".into());
                    }
                    let read = match &sub {
                        b"FS  " if sub_size >= 4 => {
                            rate_hz = Some(h.u32_be("DSDIFF FS")?);
                            4
                        }
                        b"CHNL" if sub_size >= 2 => {
                            channels = Some(h.u16_be("DSDIFF CHNL")?);
                            2
                        }
                        b"CMPR" if sub_size >= 4 => {
                            compression = Some(h.bytes::<4>("DSDIFF CMPR")?);
                            4
                        }
                        _ => 0,
                    };
                    h.skip(sub_padded - read, "DSDIFF property")?;
                    left -= 12 + sub_padded;
                }
            }
            b"DSD " => break size,
            b"DST " => return Err(dst_refused()),
            _ => h.skip(padded, "DSDIFF chunk")?,
        }
    };

    match &compression {
        Some(c) if c == b"DST " => return Err(dst_refused()),
        Some(c) if c != b"DSD " => {
            return Err(format!("audio: unsupported DSDIFF compression {c:?}"));
        }
        _ => {}
    }
    let rate_hz = rate_hz.ok_or("audio: DSDIFF file has no sample rate (FS)")?;
    let channels = channels.ok_or("audio: DSDIFF file has no channel count (CHNL)")?;
    if !(1..=MAX_DFF_CHANNELS).contains(&channels) {
        return Err(format!("audio: unsupported DSDIFF channel count {channels}"));
    }
    let (base_hz, multiple) = classify_rate(rate_hz)
        .ok_or_else(|| format!("audio: unsupported DSD rate {rate_hz} Hz"))?;
    let data_offset = h.pos;
    let samples = (data_len / u64::from(channels)) * 8;
    let data_end = data_offset
        .saturating_add(data_len)
        .saturating_add(data_len & 1);
    Ok(DsdStream {
        container: Container::Dff,
        rate_hz,
        base_hz,
        multiple,
        channels,
        layout: Layout::Interleaved,
        data_offset,
        samples,
        tag_offset: (data_end < form_end).then_some(data_end),
    })
}

fn dst_refused() -> String {
    "audio: DST-compressed DSDIFF isn't supported (convert the file to plain DSD)".into()
}

// ── Seeking ────────────────────────────────────────────────────────────────

/// Where a seek starts reading. A DSF seek lands on a block group
/// boundary (channels stay aligned); a DSDIFF seek on a 16-byte boundary,
/// which keeps the converter's decimation phases the same as when playing
/// from the start. `skip_samples` (per channel) are then dropped by the
/// converter or the DoP packer to land on the requested time.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct SeekPlan {
    pub byte_offset: u64,
    /// DSD sample (per channel) at `byte_offset`.
    pub first_sample: u64,
    pub skip_samples: u64,
}

/// Byte offset alignment for DSDIFF seeks: 16 bytes = 128 bits, a whole
/// number of output samples at every rate (decimation 32, 64 or 128).
const DFF_SEEK_ALIGN_BYTES: u64 = 16;

pub fn seek_plan(s: &DsdStream, position_ms: u64) -> SeekPlan {
    let target = (u128::from(position_ms) * u128::from(s.rate_hz) / 1000)
        .min(u128::from(s.samples)) as u64;
    let channels = u64::from(s.channels);
    let (byte_offset, first_sample) = match s.layout {
        Layout::Blocks { block_bytes, .. } => {
            let block_bits = u64::from(block_bytes) * 8;
            let block = target / block_bits;
            (
                s.data_offset + block * u64::from(block_bytes) * channels,
                block * block_bits,
            )
        }
        Layout::Interleaved => {
            let byte = target / 8 / DFF_SEEK_ALIGN_BYTES * DFF_SEEK_ALIGN_BYTES;
            (s.data_offset + byte * channels, byte * 8)
        }
    };
    SeekPlan {
        byte_offset,
        first_sample,
        skip_samples: target - first_sample,
    }
}

// ── Reading the sound data ─────────────────────────────────────────────────

/// Reads the sound data from `first_sample` on and hands it out per
/// channel, MSB (oldest bit) first, whatever the container's layout. It
/// stops at the last audio sample the header declares, so it never reads
/// DSF's block padding or anything after the data chunk.
pub struct DsdReader<R> {
    inner: R,
    channels: usize,
    layout: Layout,
    /// Audio samples per channel still to deliver.
    remaining: u64,
    raw: Vec<u8>,
}

/// Frames per DSDIFF read: 4096 bytes per channel, like a DSF block.
const DFF_CHUNK_FRAMES: usize = 4096;

impl<R: Read> DsdReader<R> {
    /// `inner` must stand at `plan.byte_offset`.
    pub fn new(inner: R, s: &DsdStream, plan: &SeekPlan) -> Self {
        DsdReader {
            inner,
            channels: usize::from(s.channels),
            layout: s.layout,
            remaining: s.samples.saturating_sub(plan.first_sample),
            raw: Vec::new(),
        }
    }

    /// Fills `out[ch]` with the next run of channel `ch`'s bytes and
    /// returns how many bytes each got; 0 = the audio has ended. A file
    /// cut short ends the audio at the last whole block (or frame). A
    /// final byte only partly audio (sample counts needn't be multiples
    /// of 8) is left out: at most 7 DSD samples, 2.5 us at DSD64.
    pub fn read_chunk(&mut self, out: &mut [Vec<u8>]) -> io::Result<usize> {
        debug_assert_eq!(out.len(), self.channels);
        if self.remaining < 8 {
            self.remaining = 0;
            return Ok(0);
        }
        match self.layout {
            Layout::Blocks {
                block_bytes,
                lsb_first,
            } => {
                let block = block_bytes as usize;
                let group = block * self.channels;
                self.raw.resize(group, 0);
                if read_full(&mut self.inner, &mut self.raw)? < group {
                    self.remaining = 0;
                    return Ok(0);
                }
                let bits = self.remaining.min(block as u64 * 8);
                self.remaining -= bits;
                let n = (bits / 8) as usize;
                for (ch, dst) in out.iter_mut().enumerate() {
                    let src = &self.raw[ch * block..ch * block + n];
                    dst.clear();
                    if lsb_first {
                        dst.extend(src.iter().map(|b| b.reverse_bits()));
                    } else {
                        dst.extend_from_slice(src);
                    }
                }
                Ok(n)
            }
            Layout::Interleaved => {
                let mut frames = ((self.remaining / 8) as usize).min(DFF_CHUNK_FRAMES);
                self.raw.resize(frames * self.channels, 0);
                let got = read_full(&mut self.inner, &mut self.raw)?;
                if got < self.raw.len() {
                    frames = got / self.channels;
                    self.remaining = 0;
                } else {
                    self.remaining -= frames as u64 * 8;
                }
                for (ch, dst) in out.iter_mut().enumerate() {
                    dst.clear();
                    dst.extend(
                        self.raw[..frames * self.channels]
                            .iter()
                            .skip(ch)
                            .step_by(self.channels),
                    );
                }
                Ok(frames)
            }
        }
    }
}

/// Reads until `buf` is full or the stream ends; returns the bytes read.
fn read_full<R: Read>(r: &mut R, buf: &mut [u8]) -> io::Result<usize> {
    let mut got = 0;
    while got < buf.len() {
        match r.read(&mut buf[got..]) {
            Ok(0) => break,
            Ok(n) => got += n,
            Err(e) if e.kind() == io::ErrorKind::Interrupted => {}
            Err(e) => return Err(e),
        }
    }
    Ok(got)
}

// ── DSD -> PCM ─────────────────────────────────────────────────────────────
//
// A chain of linear-phase FIR low-pass filters, each followed by
// decimation, from the DSD rate down to twice the base rate:
//
//   DSD64  2.8224 MHz -> [stage 1, /8] -> 352.8 k -> [half-band, /2]
//          -> 176.4 k -> [final, /2] -> 88.2 kHz
//   DSD128 adds one half-band stage, DSD256 two (the 48 kHz family is
//   the same chain, every frequency scaled by 48/44.1).
//
// Why 88.2 kHz (2x base) and not 176.4: DSD's noise shaping moves its
// quantisation noise above ~25 kHz, where it climbs to tens of dB above
// the in-band floor. Converting to 88.2 kHz lets the final filter remove
// everything above 44.1 kHz (what the SACD spec asks a player to filter
// at ~50 kHz), instead of handing that noise to the DAC and the
// amplifier, and it is a rate every DAC and every OS mixer accepts. The
// audio band is untouched: the passband is flat to 30 kHz.
//
// Design rule, each stage: the band that can still reach the output,
// 0..44.1 kHz (x48/44.1), must stay free of aliases, i.e. everything
// that would fold into it on decimation is attenuated by >= 140 dB
// (`STOPBAND_DB`); the passband is flat (ripple ~1e-6 dB) to at least
// 44.1 kHz in every stage but the last, which passes 0..30 kHz and stops
// from 44.1 kHz, so the output has no aliasing anywhere in its band. The
// tests below measure every stage's response against these numbers.
//
// The coefficients are computed here (Kaiser-windowed sinc), not copied:
// the Kaiser window's beta for an attenuation A > 50 dB is
// 0.1102 (A - 8.7), and the length for a transition width df (in cycles
// per sample) is about (A - 7.95) / (14.36 df) + 1 (Kaiser 1974, as in
// Oppenheim & Schafer). Each stage's length below is that estimate
// rounded up, then checked by the response tests.
//
// Stage 1 runs on the bits themselves. Its 112 taps span 14 input bytes;
// for each of those byte positions a 256-entry table holds the filter's
// sum for every byte value (bit 1 = +1, bit 0 = -1), so one output is 14
// table lookups instead of 112 multiplies (the classic "byte lookup"
// DSD decimator). Everything runs in f64; the result is rounded to 24
// bits at the end. No dither: DSD's own in-band noise (~-120 dB) is far
// above the 24-bit step (-144 dB) and already dithers it.

/// Attenuation every stage is designed for.
const STOPBAND_DB: f64 = 140.0;

/// Stage 1: 112 taps (14 bytes) at the DSD rate, decimating by 8.
/// Must pass 0..44.1 kHz (0.015625 of DSD64's rate) and stop from
/// 352.8 - 44.1 = 308.7 kHz (0.109375). Kaiser's estimate for that
/// transition is 99 taps; 112 (whole bytes, with margin) measures
/// -140.6 dB. Cutoff: the midpoint, 1/16 of the input rate. The same
/// filter serves DSD128/256, whose stage 1 needs less (their next
/// alias band starts relatively higher).
const STAGE1_TAPS: usize = 112;
const STAGE1_CUTOFF: f64 = 1.0 / 16.0;

/// Half-band stages, each decimating by 2. The strictest is the last
/// one (352.8 -> 176.4 kHz): pass 0..44.1 kHz (0.125), stop from
/// 132.3 kHz (0.375). Estimate 38 taps; half-band lengths are 4k+3, and
/// 39 measures -135 dB, so 43 (-137 dB). Earlier half-bands (DSD128/256)
/// need less, but sharing one design keeps it simple and costs little
/// (half its taps are zero and skipped).
const HALFBAND_TAPS: usize = 43;

/// Final stage, 4x -> 2x base rate (176.4 -> 88.2 kHz): pass 0..30 kHz
/// (0.170068), stop from 44.1 kHz (0.25). Estimate 116 taps; 121
/// measures -140.1 dB.
const FINAL_TAPS: usize = 121;
const FINAL_PASS: f64 = 30_000.0 / 176_400.0;
const FINAL_STOP: f64 = 0.25;

fn kaiser_beta(attenuation_db: f64) -> f64 {
    0.1102 * (attenuation_db - 8.7)
}

/// Modified Bessel function of the first kind, order 0 (power series).
fn bessel_i0(x: f64) -> f64 {
    let q = x * x / 4.0;
    let mut term = 1.0;
    let mut sum = 1.0;
    let mut k = 1.0;
    while term > sum * 1e-17 {
        term *= q / (k * k);
        sum += term;
        k += 1.0;
    }
    sum
}

/// Kaiser-windowed sinc low-pass with `cutoff` in cycles per sample,
/// normalised to unity gain at DC.
fn kaiser_lowpass(taps: usize, cutoff: f64, beta: f64) -> Vec<f64> {
    let m = (taps - 1) as f64 / 2.0;
    let i0_beta = bessel_i0(beta);
    let mut h: Vec<f64> = (0..taps)
        .map(|n| {
            let t = n as f64 - m;
            let sinc = if t == 0.0 {
                2.0 * cutoff
            } else {
                (2.0 * std::f64::consts::PI * cutoff * t).sin() / (std::f64::consts::PI * t)
            };
            let r = t / m;
            sinc * bessel_i0(beta * (1.0 - r * r).max(0.0).sqrt()) / i0_beta
        })
        .collect();
    let sum: f64 = h.iter().sum();
    for v in &mut h {
        *v /= sum;
    }
    h
}

/// Half-band low-pass (cutoff at a quarter of the input rate): the even
/// offsets from the centre are exactly zero and the centre is exactly
/// 1/2, the odd taps scaled to sum to 1/2 (unity DC gain).
fn halfband(taps: usize, beta: f64) -> Vec<f64> {
    debug_assert_eq!(taps % 4, 3);
    let mut h = kaiser_lowpass(taps, 0.25, beta);
    let c = (taps - 1) / 2;
    let mut odd_sum = 0.0;
    for (n, v) in h.iter_mut().enumerate() {
        let off = n.abs_diff(c);
        if off == 0 {
            *v = 0.5;
        } else if off % 2 == 0 {
            *v = 0.0;
        } else {
            odd_sum += *v;
        }
    }
    for (n, v) in h.iter_mut().enumerate() {
        if n.abs_diff(c) % 2 == 1 {
            *v *= 0.5 / odd_sum;
        }
    }
    h
}

/// Stage 1's byte tables. For byte position k (0 = the newest byte) and
/// byte value b, table[k][b] = sum over its bits t (t = 0 the oldest, at
/// bit 7) of h[8k + 7 - t] * (+1 or -1). With x[i] the bit stream, an
/// output after byte m is sum_j h[j] x[8m + 7 - j], and bit t of byte
/// m - k is x[8(m - k) + t], i.e. j = 8k + 7 - t.
fn byte_tables(h: &[f64]) -> Vec<[f64; 256]> {
    debug_assert_eq!(h.len() % 8, 0);
    (0..h.len() / 8)
        .map(|k| {
            let mut table = [0.0; 256];
            for (b, slot) in table.iter_mut().enumerate() {
                *slot = (0..8)
                    .map(|t| {
                        let bit = (b >> (7 - t)) & 1;
                        let x = if bit == 1 { 1.0 } else { -1.0 };
                        h[8 * k + 7 - t] * x
                    })
                    .sum();
            }
            table
        })
        .collect()
}

struct Filters {
    stage1: Vec<f64>,
    stage1_tables: Vec<[f64; 256]>,
    halfband: Vec<f64>,
    halfband_nonzero: Vec<(usize, f64)>,
    last: Vec<f64>,
    last_nonzero: Vec<(usize, f64)>,
}

fn nonzero(h: &[f64]) -> Vec<(usize, f64)> {
    h.iter()
        .enumerate()
        .filter(|(_, v)| **v != 0.0)
        .map(|(i, v)| (i, *v))
        .collect()
}

fn filters() -> &'static Filters {
    static FILTERS: OnceLock<Filters> = OnceLock::new();
    FILTERS.get_or_init(|| {
        let beta = kaiser_beta(STOPBAND_DB);
        let stage1 = kaiser_lowpass(STAGE1_TAPS, STAGE1_CUTOFF, beta);
        let halfband = halfband(HALFBAND_TAPS, beta);
        let last = kaiser_lowpass(FINAL_TAPS, (FINAL_PASS + FINAL_STOP) / 2.0, beta);
        Filters {
            stage1_tables: byte_tables(&stage1),
            halfband_nonzero: nonzero(&halfband),
            last_nonzero: nonzero(&last),
            stage1,
            halfband,
            last,
        }
    })
}

/// Stage 1: one output per input byte (decimation by 8).
struct ByteFir {
    tables: &'static [[f64; 256]],
    // The last K bytes, stored twice so the window is one slice.
    history: Vec<u8>,
    pos: usize,
}

impl ByteFir {
    fn new(tables: &'static [[f64; 256]]) -> Self {
        // Start from DSD silence rather than zeros (which are -1s).
        ByteFir {
            tables,
            history: vec![DSD_IDLE_BYTE; 2 * tables.len()],
            pos: 0,
        }
    }

    fn push(&mut self, byte: u8) -> f64 {
        let k = self.tables.len();
        self.history[self.pos] = byte;
        self.history[self.pos + k] = byte;
        self.pos = (self.pos + 1) % k;
        // Oldest .. newest; the newest is table 0.
        let window = &self.history[self.pos..self.pos + k];
        window
            .iter()
            .rev()
            .zip(self.tables)
            .map(|(b, table)| table[usize::from(*b)])
            .sum()
    }
}

/// A linear-phase FIR followed by decimation by 2 (one output per two
/// inputs). Zero taps (half of a half-band's) are skipped.
struct Decimator2 {
    taps: &'static [(usize, f64)],
    len: usize,
    history: Vec<f64>,
    pos: usize,
    odd: bool,
}

impl Decimator2 {
    fn new(taps: &'static [(usize, f64)], len: usize) -> Self {
        Decimator2 {
            taps,
            len,
            history: vec![0.0; 2 * len],
            pos: 0,
            odd: false,
        }
    }

    fn push(&mut self, x: f64) -> Option<f64> {
        self.history[self.pos] = x;
        self.history[self.pos + self.len] = x;
        self.pos = (self.pos + 1) % self.len;
        self.odd = !self.odd;
        if self.odd {
            return None;
        }
        // Symmetric taps: the window's direction doesn't matter.
        let window = &self.history[self.pos..self.pos + self.len];
        Some(self.taps.iter().map(|(i, h)| h * window[*i]).sum())
    }
}

/// One channel's chain: stage 1, the half-bands, the final stage.
struct Chain {
    stage1: ByteFir,
    halfbands: Vec<Decimator2>,
    last: Decimator2,
}

impl Chain {
    fn new(halfbands: usize) -> Self {
        let f = filters();
        Chain {
            stage1: ByteFir::new(&f.stage1_tables),
            halfbands: (0..halfbands)
                .map(|_| Decimator2::new(&f.halfband_nonzero, f.halfband.len()))
                .collect(),
            last: Decimator2::new(&f.last_nonzero, f.last.len()),
        }
    }

    fn push(&mut self, byte: u8) -> Option<f64> {
        let mut v = self.stage1.push(byte);
        for hb in &mut self.halfbands {
            v = hb.push(v)?;
        }
        self.last.push(v)
    }
}

/// How many raw chain outputs to drop so that output n is the signal at
/// time n (in output samples). Every stage is linear phase and delays by
/// half its length at its own input rate; summed in DSD bits: stage 1
/// at 1 bit per sample, half-band i at 8 * 2^i, the final stage at
/// decimation / 2. Raw output r is computed once bit
/// decimation * (r + 1) - 1 is in, so it shows the signal at bit
/// decimation * r + (decimation - 1) - delay.
fn chain_delay(decimation: u64, halfbands: usize) -> f64 {
    let f = filters();
    let half = |taps: usize| (taps - 1) as f64 / 2.0;
    let mut bits = half(f.stage1.len());
    for i in 0..halfbands {
        bits += half(f.halfband.len()) * 8.0 * f64::from(1u32 << i);
    }
    bits += half(f.last.len()) * (decimation / 2) as f64;
    (bits - (decimation - 1) as f64) / decimation as f64
}

/// DSD -> PCM for one stream: per-channel MSB-first bytes in, interleaved
/// f64 samples (DSD's +/-1 scale) out at `DsdStream::pcm_rate_hz`.
///
/// Time alignment: the chain's group delay (well under a millisecond) is
/// dropped from the start and flushed from the end with DSD silence, and
/// a seek's leftover samples inside its block are dropped too, so output
/// sample n is the time `first_sample + skip` + n / rate and a track
/// gives exactly its length in samples. Gapless: like any per-track
/// filter, the samples within the filter length of a track boundary are
/// computed against silence on the far side.
pub struct PcmConverter {
    chains: Vec<Chain>,
    // Output frames still to drop / still to emit.
    skip: u64,
    left: u64,
    // How many frames `finish` may still produce from silence.
    tail: u64,
    scratch: Vec<Vec<f64>>,
}

impl PcmConverter {
    pub fn new(s: &DsdStream, plan: &SeekPlan) -> Self {
        let decimation = u64::from(s.rate_hz / s.pcm_rate_hz());
        // decimation = 8 (stage 1) * 2^halfbands * 2 (final stage).
        let halfbands = (decimation / 16).trailing_zeros() as usize;
        let delay = chain_delay(decimation, halfbands).round() as u64;
        let lead = plan.skip_samples / decimation;
        let total = s.samples.saturating_sub(plan.first_sample) / decimation;
        let channels = usize::from(s.channels);
        PcmConverter {
            chains: (0..channels).map(|_| Chain::new(halfbands)).collect(),
            skip: delay + lead,
            left: total.saturating_sub(lead),
            tail: delay + 2,
            scratch: vec![Vec::new(); channels],
        }
    }

    /// Converts one run of per-channel bytes (all the same length),
    /// appending interleaved samples to `out`.
    pub fn convert(&mut self, bytes: &[Vec<u8>], out: &mut Vec<f64>) {
        for ((chain, input), scratch) in self.chains.iter_mut().zip(bytes).zip(&mut self.scratch) {
            scratch.clear();
            scratch.extend(input.iter().filter_map(|b| chain.push(*b)));
        }
        let frames = self.scratch.first().map_or(0, Vec::len);
        for i in 0..frames {
            if self.skip > 0 {
                self.skip -= 1;
                continue;
            }
            if self.left == 0 {
                break;
            }
            self.left -= 1;
            out.extend(self.scratch.iter().map(|ch| ch[i]));
        }
    }

    /// After the last byte: pushes the samples still inside the filters
    /// out, by feeding DSD silence. Emits at most the chain's delay (so a
    /// stream cut short doesn't turn into a long stretch of silence).
    pub fn finish(&mut self, out: &mut Vec<f64>) {
        let silence = vec![vec![DSD_IDLE_BYTE; 16]; self.chains.len()];
        let channels = self.chains.len().max(1);
        // Bounded: each round feeds 16 bytes per channel.
        for _ in 0..4096 {
            if self.left == 0 || self.tail == 0 {
                return;
            }
            let before = out.len();
            self.convert(&silence, out);
            let emitted = ((out.len() - before) / channels) as u64;
            if emitted > self.tail {
                out.truncate(before + self.tail as usize * channels);
                self.tail = 0;
            } else {
                self.tail -= emitted;
            }
        }
    }
}

// ── DoP ────────────────────────────────────────────────────────────────────
//
// DoP 1.1 carries DSD through a 24-bit PCM channel: each PCM frame holds
// 16 DSD bits per channel (oldest bit at the top of the 16), with a
// marker byte above them that alternates 0x05 / 0xFA from frame to frame
// (the same marker on every channel of a frame). A DoP DAC sees the
// markers and plays DSD; anything else plays it as loud noise, which is
// why it is opt-in and only ever sent to WASAPI exclusive at unity gain.
//
// The decoder produces payloads (marker byte left at zero); the output
// adds the markers, so the alternation never breaks — not across a
// pause, an underrun or the gap before the first sample, which all go
// out as the DoP idle pattern (0x69 payload bytes with markers), never
// zeros (which a DAC reads as PCM and switches mode with a click).

#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
pub const DOP_MARKER_05: u8 = 0x05;
#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
pub const DOP_MARKER_FA: u8 = 0xFA;

/// A 24-in-32 DoP payload: `older` in bits 23..16, `newer` in 15..8,
/// both MSB (oldest bit) first. The marker byte (31..24) is added by
/// `dop_word`.
pub fn dop_payload(older: u8, newer: u8) -> i32 {
    (i32::from(older) << 16) | (i32::from(newer) << 8)
}

#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
pub const DOP_IDLE_PAYLOAD: i32 = (DSD_IDLE_BYTE as i32) << 16 | (DSD_IDLE_BYTE as i32) << 8;

/// The word the DAC gets: marker in the top byte, payload below.
#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
pub fn dop_word(payload: i32, marker: u8) -> i32 {
    ((u32::from(marker) << 24) | (payload as u32 & 0x00FF_FF00)) as i32
}

/// The marker sequence, one per frame.
#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
#[derive(Clone, Copy)]
pub struct DopMarker {
    next_is_05: bool,
}

#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
impl DopMarker {
    pub fn new() -> Self {
        DopMarker { next_is_05: true }
    }

    pub fn next(&mut self) -> u8 {
        let m = if self.next_is_05 {
            DOP_MARKER_05
        } else {
            DOP_MARKER_FA
        };
        self.next_is_05 = !self.next_is_05;
        m
    }
}

/// One output period of DoP, `out` interleaved with `channels` per frame.
/// For each frame `fill` writes the frame's payloads into its slice and
/// returns true, or returns false when it has no audio for the frame
/// (paused, underrun, not unity volume): that frame goes out as idle.
/// Every frame gets the next marker either way.
#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
pub fn dop_period(
    out: &mut [i32],
    channels: usize,
    marker: &mut DopMarker,
    mut fill: impl FnMut(&mut [i32]) -> bool,
) {
    for frame in out.chunks_exact_mut(channels.max(1)) {
        let m = marker.next();
        let audio = fill(frame);
        for w in frame {
            *w = dop_word(if audio { *w } else { DOP_IDLE_PAYLOAD }, m);
        }
    }
}

/// Turns per-channel bytes into DoP payloads, interleaved by frame.
pub struct DopPacker {
    // Bytes per channel still to drop (a seek's leftover, whole frames).
    skip: usize,
    // A byte per channel waiting for its partner.
    carry: Option<Vec<u8>>,
}

impl DopPacker {
    pub fn new(plan: &SeekPlan) -> Self {
        DopPacker {
            skip: ((plan.skip_samples / 16) * 2) as usize,
            carry: None,
        }
    }

    pub fn pack(&mut self, bytes: &[Vec<u8>], out: &mut Vec<i32>) {
        let n = bytes.first().map_or(0, Vec::len);
        let mut i = self.skip.min(n);
        self.skip -= i;
        if i < n {
            if let Some(carry) = self.carry.take() {
                out.extend(carry.iter().zip(bytes).map(|(c, b)| dop_payload(*c, b[i])));
                i += 1;
            }
        }
        while i + 1 < n {
            out.extend(bytes.iter().map(|b| dop_payload(b[i], b[i + 1])));
            i += 2;
        }
        if i < n {
            self.carry = Some(bytes.iter().map(|b| b[i]).collect());
        }
    }

    /// An odd byte left at the end goes out paired with DSD silence.
    pub fn finish(&mut self, out: &mut Vec<i32>) {
        if let Some(carry) = self.carry.take() {
            out.extend(carry.iter().map(|c| dop_payload(*c, DSD_IDLE_BYTE)));
        }
    }
}

/// What the DoP decision looks at. DoP words reach a DAC intact only
/// over WASAPI exclusive at unity gain; through shared mode / the OS
/// mixer, or with any volume or gain applied, they turn into noise.
pub struct DopRequest {
    /// The user's "DSD over DoP" setting (default off).
    pub enabled: bool,
    /// Exclusive output is on (the setting the WASAPI exclusive path
    /// keys on). Whether the device then accepts the DoP rate is only
    /// known at open; a refusal falls back to PCM conversion.
    pub exclusive: bool,
    /// Software volume, 0..=1.
    pub volume: f32,
    /// The ReplayGain factor this track would get.
    pub gain: f32,
}

/// The DoP rate to try (DSD64 -> 176.4 kHz, DSD128 -> 352.8 kHz), or
/// None: convert to PCM. Linux and macOS never use DoP (cpal goes
/// through the mixer); DSD256 doesn't (705.6 kHz DoP is rare in DACs).
pub fn dop_rate(s: &DsdStream, req: &DopRequest) -> Option<u32> {
    dop_rate_for(cfg!(target_os = "windows"), s, req)
}

fn dop_rate_for(platform_has_dop: bool, s: &DsdStream, req: &DopRequest) -> Option<u32> {
    let allowed = platform_has_dop
        && req.enabled
        && req.exclusive
        && req.volume == 1.0
        && req.gain == 1.0
        && matches!(s.multiple, 64 | 128);
    allowed.then(|| s.dop_rate_hz())
}

// ── ReplayGain tags ────────────────────────────────────────────────────────

/// Largest ID3 frame read whole; bigger ones (cover art) are skipped.
const MAX_ID3_FRAME: u64 = 64 * 1024;
/// Skips up to this size read through; longer ones seek (a Range request).
const SKIP_BY_READING: u64 = 64 * 1024;

fn skip_forward<R: Read + Seek>(r: &mut R, n: u64) -> io::Result<()> {
    if n <= SKIP_BY_READING {
        let copied = io::copy(&mut r.by_ref().take(n), &mut io::sink())?;
        if copied < n {
            return Err(io::ErrorKind::UnexpectedEof.into());
        }
    } else {
        r.seek(SeekFrom::Current(n as i64))?;
    }
    Ok(())
}

/// The (description, value) pairs of the TXXX frames in the file's ID3v2
/// tag — where taggers put REPLAYGAIN_* for DSF and DSDIFF. Frames other
/// than TXXX (cover art above all) are skipped, not read. Ok(empty) when
/// the file has no tag.
pub fn read_txxx<R: Read + Seek>(r: &mut R, s: &DsdStream) -> io::Result<Vec<(String, String)>> {
    let Some(offset) = s.tag_offset else {
        return Ok(Vec::new());
    };
    r.seek(SeekFrom::Start(offset))?;
    match s.container {
        Container::Dsf => read_id3v2_txxx(r),
        Container::Dff => {
            // Chunks after the sound data; a tagger's "ID3 " is one of them.
            for _ in 0..32 {
                let mut head = [0u8; 12];
                if read_full(r, &mut head)? < head.len() {
                    break;
                }
                let size = u64::from_be_bytes(head[4..12].try_into().unwrap());
                if &head[0..4] == b"ID3 " {
                    // The tag's own header bounds what is read.
                    return read_id3v2_txxx(r);
                }
                skip_forward(r, size.saturating_add(size & 1))?;
            }
            Ok(Vec::new())
        }
    }
}

fn synchsafe(b: &[u8]) -> u64 {
    b.iter().fold(0u64, |acc, v| (acc << 7) | u64::from(v & 0x7F))
}

fn read_id3v2_txxx<R: Read + Seek>(r: &mut R) -> io::Result<Vec<(String, String)>> {
    let mut header = [0u8; 10];
    if read_full(r, &mut header)? < header.len() || &header[0..3] != b"ID3" {
        return Ok(Vec::new());
    }
    let major = header[3];
    let flags = header[5];
    // v2.2's frames have another shape, and a tag-wide unsynchronised
    // v2.3 tag can't be walked frame by frame; both are rare enough to
    // go without ReplayGain.
    if !(3..=4).contains(&major) || flags & 0x80 != 0 {
        return Ok(Vec::new());
    }
    let mut left = synchsafe(&header[6..10]);
    if flags & 0x40 != 0 {
        // Extended header: v2.4's size counts itself, v2.3's doesn't.
        let mut size = [0u8; 4];
        r.read_exact(&mut size)?;
        let total = if major == 4 {
            synchsafe(&size)
        } else {
            u64::from(u32::from_be_bytes(size)) + 4
        };
        if total < 4 || total > left {
            return Ok(Vec::new());
        }
        skip_forward(r, total - 4)?;
        left -= total;
    }
    let mut pairs = Vec::new();
    while left >= 10 {
        let mut frame = [0u8; 10];
        r.read_exact(&mut frame)?;
        left -= 10;
        if frame[0] == 0 {
            break; // padding
        }
        let size = if major == 4 {
            synchsafe(&frame[4..8])
        } else {
            u64::from(u32::from_be_bytes(frame[4..8].try_into().unwrap()))
        };
        if size > left {
            break;
        }
        left -= size;
        let format = frame[9];
        // Compressed, encrypted, grouped or unsynchronised frames are
        // skipped; v2.4's data-length indicator is allowed.
        let plain = if major == 4 {
            format & 0x4E == 0
        } else {
            format & 0xE0 == 0
        };
        if &frame[0..4] == b"TXXX" && plain && size <= MAX_ID3_FRAME {
            let mut body = vec![0u8; size as usize];
            r.read_exact(&mut body)?;
            let body = if major == 4 && format & 0x01 != 0 {
                body.get(4..).unwrap_or(&[])
            } else {
                &body[..]
            };
            if let Some(pair) = parse_txxx(body) {
                pairs.push(pair);
            }
        } else {
            skip_forward(r, size)?;
        }
    }
    Ok(pairs)
}

/// TXXX body: encoding byte, description, NUL, value.
fn parse_txxx(body: &[u8]) -> Option<(String, String)> {
    let (&encoding, text) = body.split_first()?;
    let (desc, value) = match encoding {
        // ISO-8859-1 / UTF-8: NUL-terminated byte strings.
        0 | 3 => {
            let nul = text.iter().position(|b| *b == 0)?;
            let decode = |b: &[u8]| -> String {
                if encoding == 0 {
                    b.iter().map(|c| char::from(*c)).collect()
                } else {
                    String::from_utf8_lossy(b).into_owned()
                }
            };
            (decode(&text[..nul]), decode(&text[nul + 1..]))
        }
        // UTF-16 with a BOM / UTF-16BE: terminated by an aligned 00 00.
        1 | 2 => {
            let nul = (0..text.len() / 2).find(|i| text[2 * i] == 0 && text[2 * i + 1] == 0)? * 2;
            let decode = |b: &[u8]| -> String {
                let (big_endian, b) = match b {
                    [0xFF, 0xFE, rest @ ..] => (false, rest),
                    [0xFE, 0xFF, rest @ ..] => (true, rest),
                    _ => (encoding == 2, b),
                };
                let units = b.chunks_exact(2).map(|p| {
                    if big_endian {
                        u16::from_be_bytes([p[0], p[1]])
                    } else {
                        u16::from_le_bytes([p[0], p[1]])
                    }
                });
                char::decode_utf16(units)
                    .map(|c| c.unwrap_or(char::REPLACEMENT_CHARACTER))
                    .collect::<String>()
            };
            (decode(&text[..nul]), decode(&text[nul + 2..]))
        }
        _ => return None,
    };
    Some((
        desc.trim_end_matches('\0').trim().to_string(),
        value.trim_end_matches('\0').trim().to_string(),
    ))
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use std::f64::consts::PI;
    use std::io::Cursor;

    // ── Fixtures ───────────────────────────────────────────────────

    /// A DSF file: `bits[ch]` are each channel's MSB-first bytes (the
    /// fixture writes them LSB-first unless `msb_first`), the last block
    /// padded with zeros as the spec says, then `id3` as the metadata
    /// chunk. `sample_count` defaults to all the bytes given.
    pub(crate) fn build_dsf(
        rate: u32,
        bits: &[Vec<u8>],
        msb_first: bool,
        sample_count: Option<u64>,
        id3: Option<&[u8]>,
    ) -> Vec<u8> {
        let channels = bits.len();
        let bytes = bits[0].len();
        let blocks = bytes.div_ceil(4096);
        let mut data = Vec::new();
        for blk in 0..blocks {
            for ch in bits {
                for i in 0..4096 {
                    let b = ch.get(blk * 4096 + i).copied().unwrap_or(0x00);
                    data.push(if msb_first { b } else { b.reverse_bits() });
                }
            }
        }
        let data_offset = 28 + 52 + 12;
        let meta_offset = id3.map_or(0, |_| (data_offset + data.len()) as u64);
        let total = data_offset + data.len() + id3.map_or(0, <[u8]>::len);
        let mut f = Vec::new();
        f.extend_from_slice(b"DSD ");
        f.extend_from_slice(&28u64.to_le_bytes());
        f.extend_from_slice(&(total as u64).to_le_bytes());
        f.extend_from_slice(&meta_offset.to_le_bytes());
        f.extend_from_slice(b"fmt ");
        f.extend_from_slice(&52u64.to_le_bytes());
        f.extend_from_slice(&1u32.to_le_bytes()); // version
        f.extend_from_slice(&0u32.to_le_bytes()); // DSD raw
        f.extend_from_slice(&2u32.to_le_bytes()); // channel type
        f.extend_from_slice(&(channels as u32).to_le_bytes());
        f.extend_from_slice(&rate.to_le_bytes());
        f.extend_from_slice(&(if msb_first { 8u32 } else { 1 }).to_le_bytes());
        f.extend_from_slice(&sample_count.unwrap_or(bytes as u64 * 8).to_le_bytes());
        f.extend_from_slice(&4096u32.to_le_bytes());
        f.extend_from_slice(&0u32.to_le_bytes());
        f.extend_from_slice(b"data");
        f.extend_from_slice(&((12 + data.len()) as u64).to_le_bytes());
        f.extend_from_slice(&data);
        if let Some(tag) = id3 {
            f.extend_from_slice(tag);
        }
        f
    }

    fn dff_chunk(id: &[u8; 4], body: &[u8]) -> Vec<u8> {
        let mut c = Vec::new();
        c.extend_from_slice(id);
        c.extend_from_slice(&(body.len() as u64).to_be_bytes());
        c.extend_from_slice(body);
        if body.len() % 2 == 1 {
            c.push(0);
        }
        c
    }

    /// A DSDIFF file with the frames interleaved, CMPR `compression`, an
    /// odd-sized COMT chunk before the data (exercises the pad byte) and
    /// `trailer` chunks after it.
    fn build_dff(rate: u32, bits: &[Vec<u8>], compression: &[u8; 4], trailer: &[u8]) -> Vec<u8> {
        let channels = bits.len();
        let mut prop = b"SND ".to_vec();
        prop.extend(dff_chunk(b"FS  ", &rate.to_be_bytes()));
        let mut chnl = (channels as u16).to_be_bytes().to_vec();
        for i in 0..channels {
            chnl.extend_from_slice(if i == 0 { b"SLFT" } else { b"C000" });
        }
        prop.extend(dff_chunk(b"CHNL", &chnl));
        let mut cmpr = compression.to_vec();
        cmpr.extend_from_slice(b"\x0eNot compressed\x00");
        prop.extend(dff_chunk(b"CMPR", &cmpr));
        let mut data = Vec::new();
        for i in 0..bits[0].len() {
            for ch in bits {
                data.push(ch[i]);
            }
        }
        let mut body = b"DSD ".to_vec();
        body.extend(dff_chunk(b"FVER", &0x0105_0000u32.to_be_bytes()));
        body.extend(dff_chunk(b"PROP", &prop));
        body.extend(dff_chunk(b"COMT", b"odd"));
        body.extend(dff_chunk(b"DSD ", &data));
        body.extend_from_slice(trailer);
        let mut f = b"FRM8".to_vec();
        f.extend_from_slice(&(body.len() as u64).to_be_bytes());
        f.extend(body);
        f
    }

    /// An ID3v2.4 tag with the given TXXX frames (UTF-8) and a big APIC
    /// frame in front, which the reader has to skip.
    fn id3_tag(txxx: &[(&str, &str)]) -> Vec<u8> {
        fn synchsafe_bytes(n: usize) -> [u8; 4] {
            [(n >> 21) as u8 & 0x7F, (n >> 14) as u8 & 0x7F, (n >> 7) as u8 & 0x7F, n as u8 & 0x7F]
        }
        let mut frames = Vec::new();
        let apic = vec![0xFFu8; 100_000];
        frames.extend_from_slice(b"APIC");
        frames.extend_from_slice(&synchsafe_bytes(apic.len()));
        frames.extend_from_slice(&[0, 0]);
        frames.extend_from_slice(&apic);
        for (desc, value) in txxx {
            let mut body = vec![3u8];
            body.extend_from_slice(desc.as_bytes());
            body.push(0);
            body.extend_from_slice(value.as_bytes());
            frames.extend_from_slice(b"TXXX");
            frames.extend_from_slice(&synchsafe_bytes(body.len()));
            frames.extend_from_slice(&[0, 0]);
            frames.extend(body);
        }
        frames.extend(vec![0u8; 64]); // padding
        let mut tag = b"ID3\x04\x00\x00".to_vec();
        tag.extend_from_slice(&synchsafe_bytes(frames.len()));
        tag.extend(frames);
        tag
    }

    /// Reads everything the reader hands out, per channel.
    fn read_all(file: &[u8], s: &DsdStream, plan: &SeekPlan) -> Vec<Vec<u8>> {
        let mut cur = Cursor::new(file);
        cur.seek(SeekFrom::Start(plan.byte_offset)).unwrap();
        let mut rd = DsdReader::new(cur, s, plan);
        let channels = usize::from(s.channels);
        let mut all = vec![Vec::new(); channels];
        let mut chunk = vec![Vec::new(); channels];
        while rd.read_chunk(&mut chunk).unwrap() > 0 {
            for (a, c) in all.iter_mut().zip(&chunk) {
                a.extend_from_slice(c);
            }
        }
        all
    }

    fn start() -> SeekPlan {
        SeekPlan { byte_offset: 0, first_sample: 0, skip_samples: 0 }
    }

    fn from_start(s: &DsdStream) -> SeekPlan {
        SeekPlan { byte_offset: s.data_offset, ..start() }
    }

    /// Counting bytes, a recognisable pattern per channel.
    fn ramp(channels: usize, bytes: usize) -> Vec<Vec<u8>> {
        (0..channels)
            .map(|ch| (0..bytes).map(|i| (i * 7 + ch * 31) as u8).collect())
            .collect()
    }

    // ── Container parsing ──────────────────────────────────────────

    #[test]
    fn dsf_header_and_data_bounds() {
        let bits = ramp(2, 5000);
        let file = build_dsf(2_822_400, &bits, false, Some(5000 * 8 - 3), Some(&id3_tag(&[])));
        let s = parse(&mut Cursor::new(&file)).unwrap();
        assert_eq!(s.container, Container::Dsf);
        assert_eq!((s.rate_hz, s.base_hz, s.multiple, s.channels), (2_822_400, 44_100, 64, 2));
        assert_eq!(s.layout, Layout::Blocks { block_bytes: 4096, lsb_first: true });
        assert_eq!(s.data_offset, 92);
        assert_eq!(s.samples, 5000 * 8 - 3);
        assert_eq!(s.tag_offset, Some(92 + 2 * 2 * 4096));
        assert_eq!((s.pcm_rate_hz(), s.dop_rate_hz()), (88_200, 176_400));

        // The reader undoes DSF's LSB-first order and hands out exactly
        // the audio: not the last partial byte, not the zero padding of
        // the second block, not the ID3 chunk after the data.
        let got = read_all(&file, &s, &from_start(&s));
        for (g, b) in got.iter().zip(&bits) {
            assert_eq!(g[..], b[..4999]);
        }
    }

    #[test]
    fn dsf_msb_first_and_sample_count_zero() {
        // "Bits per sample = 8" is MSB-first; a sample count of 0 (some
        // writers) means "all the blocks".
        let bits = ramp(1, 4096);
        let file = build_dsf(5_644_800, &bits, true, Some(0), None);
        let s = parse(&mut Cursor::new(&file)).unwrap();
        assert_eq!(s.layout, Layout::Blocks { block_bytes: 4096, lsb_first: false });
        assert_eq!((s.multiple, s.samples, s.tag_offset), (128, 4096 * 8, None));
        assert_eq!(read_all(&file, &s, &from_start(&s)), bits);
    }

    #[test]
    fn dsf_with_odd_channel_counts_keeps_channels_apart() {
        for channels in [1, 3, 5] {
            let bits = ramp(channels, 6000);
            let file = build_dsf(11_289_600, &bits, false, None, None);
            let s = parse(&mut Cursor::new(&file)).unwrap();
            assert_eq!((s.channels as usize, s.multiple), (channels, 256));
            assert_eq!(read_all(&file, &s, &from_start(&s)), bits, "{channels} channels");
        }
    }

    #[test]
    fn dsf_headers_that_dont_play_are_refused() {
        let good = build_dsf(2_822_400, &ramp(2, 16), false, None, None);
        let patch = |at: usize, value: u32| {
            let mut f = good.clone();
            f[at..at + 4].copy_from_slice(&value.to_le_bytes());
            parse(&mut Cursor::new(&f)).unwrap_err()
        };
        // fmt chunk fields: format id @44, channels @52, rate @56,
        // bits per sample @60, block size @72.
        assert!(patch(44, 1).contains("plain DSD"));
        assert!(patch(52, 0).contains("channel count"));
        assert!(patch(52, 7).contains("channel count"));
        assert!(patch(56, 44_100).contains("rate"));
        assert!(patch(56, 22_579_200).contains("rate")); // DSD512
        assert!(patch(60, 2).contains("bits per sample"));
        assert!(patch(72, 8192).contains("block size"));
        assert!(parse(&mut Cursor::new(b"DSD \x05\0\0\0")).is_err());
        assert!(parse(&mut Cursor::new(b"NOPE....")).unwrap_err().contains("not a DSD file"));
    }

    #[test]
    fn dff_header_and_data_bounds() {
        let bits = ramp(2, 3001);
        let trailer = [dff_chunk(b"DIIN", &[1, 2, 3]), dff_chunk(b"ID3 ", &[0xAA; 50])].concat();
        let file = build_dff(2_822_400, &bits, b"DSD ", &trailer);
        let s = parse(&mut Cursor::new(&file)).unwrap();
        assert_eq!(s.container, Container::Dff);
        assert_eq!((s.rate_hz, s.channels, s.layout), (2_822_400, 2, Layout::Interleaved));
        assert_eq!(s.samples, 3001 * 8);
        // The sound data ends where the trailer starts (plus its pad).
        assert_eq!(s.tag_offset, Some(s.data_offset + 2 * 3001));
        // De-interleaved, and nothing of the chunks after the data.
        assert_eq!(read_all(&file, &s, &from_start(&s)), bits);
    }

    #[test]
    fn dff_with_odd_channel_counts_and_48k_family() {
        for channels in [1, 3, 5] {
            let bits = ramp(channels, 5000);
            let file = build_dff(6_144_000, &bits, b"DSD ", &[]);
            let s = parse(&mut Cursor::new(&file)).unwrap();
            assert_eq!((s.base_hz, s.multiple, s.pcm_rate_hz()), (48_000, 128, 96_000));
            assert_eq!(s.tag_offset, None);
            assert_eq!(read_all(&file, &s, &from_start(&s)), bits, "{channels} channels");
        }
    }

    #[test]
    fn dst_compressed_dff_is_refused_clearly() {
        let file = build_dff(2_822_400, &ramp(2, 10), b"DST ", &[]);
        let err = parse(&mut Cursor::new(&file)).unwrap_err();
        assert!(err.contains("DST-compressed"), "{err}");

        // A DST sound-data chunk without a CMPR saying so.
        let mut body = b"DSD ".to_vec();
        let mut prop = b"SND ".to_vec();
        prop.extend(dff_chunk(b"FS  ", &2_822_400u32.to_be_bytes()));
        prop.extend(dff_chunk(b"CHNL", &[0, 2, b'S', b'L', b'F', b'T', b'S', b'R', b'G', b'T']));
        body.extend(dff_chunk(b"PROP", &prop));
        body.extend(dff_chunk(b"DST ", &[0; 8]));
        let mut f = b"FRM8".to_vec();
        f.extend_from_slice(&(body.len() as u64).to_be_bytes());
        f.extend(body);
        assert!(parse(&mut Cursor::new(&f)).unwrap_err().contains("DST-compressed"));
    }

    #[test]
    fn dff_without_rate_or_channels_is_refused() {
        let mut body = b"DSD ".to_vec();
        body.extend(dff_chunk(b"PROP", b"SND "));
        body.extend(dff_chunk(b"DSD ", &[0; 8]));
        let mut f = b"FRM8".to_vec();
        f.extend_from_slice(&(body.len() as u64).to_be_bytes());
        f.extend(body);
        assert!(parse(&mut Cursor::new(&f)).unwrap_err().contains("sample rate"));
    }

    // ── Seeking ────────────────────────────────────────────────────

    #[test]
    fn seek_offsets_are_computed_not_read() {
        let dsf = DsdStream {
            container: Container::Dsf,
            rate_hz: 2_822_400,
            base_hz: 44_100,
            multiple: 64,
            channels: 2,
            layout: Layout::Blocks { block_bytes: 4096, lsb_first: true },
            data_offset: 92,
            samples: 2_822_400 * 300,
            tag_offset: None,
        };
        // 1 s = 2_822_400 samples = block 86 (86 * 32768 = 2_818_048).
        assert_eq!(
            seek_plan(&dsf, 1000),
            SeekPlan {
                byte_offset: 92 + 86 * 4096 * 2,
                first_sample: 2_818_048,
                skip_samples: 4352,
            }
        );
        assert_eq!(seek_plan(&dsf, 0), SeekPlan { byte_offset: 92, first_sample: 0, skip_samples: 0 });
        // Past the end: clamps to the end of the audio.
        let end = seek_plan(&dsf, 10_000_000);
        assert_eq!(end.first_sample + end.skip_samples, dsf.samples);
        // Hours in at DSD256 don't overflow.
        let long = DsdStream { rate_hz: 11_289_600, multiple: 256, samples: u64::MAX / 2, ..dsf };
        let p = seek_plan(&long, 10 * 3600 * 1000);
        assert_eq!(p.first_sample + p.skip_samples, 11_289_600 * 36_000);
        assert_eq!((p.byte_offset - 92) % (4096 * 2), 0);

        let dff = DsdStream {
            container: Container::Dff,
            layout: Layout::Interleaved,
            channels: 3,
            data_offset: 200,
            ..dsf
        };
        // 1 s = byte 352_800 per channel; aligned down to 16 bytes.
        let p = seek_plan(&dff, 1000);
        assert_eq!(p.byte_offset, 200 + 352_800 * 3);
        assert_eq!((p.first_sample, p.skip_samples), (352_800 * 8, 0));
        let p = seek_plan(&dff, 1);
        assert_eq!((p.byte_offset, p.first_sample, p.skip_samples), (200 + 352 * 3, 352 * 8, 6));
    }

    // A seek lands on the same samples playing from the start produced:
    // block-aligned start, the leftover dropped exactly.
    #[test]
    fn a_seek_continues_the_same_output() {
        let rate = 2_822_400;
        let bytes = 4096 * 40;
        let bits = vec![sdm_bytes(rate, bytes * 8, 0.5, 997.0, 0.3)];
        let file = build_dsf(rate, &bits, false, None, None);
        let s = parse(&mut Cursor::new(&file)).unwrap();

        let convert = |plan: &SeekPlan| {
            let mut conv = PcmConverter::new(&s, plan);
            let mut out = Vec::new();
            conv.convert(&read_all(&file, &s, plan), &mut out);
            conv.finish(&mut out);
            out
        };
        let whole = convert(&from_start(&s));
        assert_eq!(whole.len() as u64, s.samples / 32, "a track gives exactly its length");
        let at = 150; // ms: block 12, inside it
        let plan = seek_plan(&s, at);
        assert!(plan.skip_samples > 0 && plan.first_sample > 0);
        let seeked = convert(&plan);
        let first = ((plan.first_sample + plan.skip_samples) / 32) as usize;
        assert_eq!(seeked.len(), whole.len() - first);
        // The seek started from silence instead of the real history, so
        // the first filter length differs; after that, identical.
        for (i, (a, b)) in seeked.iter().zip(&whole[first..]).enumerate().skip(200) {
            assert!((a - b).abs() < 1e-12, "sample {i}: {a} vs {b}");
        }
    }

    // ── Converter design ───────────────────────────────────────────

    /// |H(f)| in dB, f in cycles per sample at the filter's input rate.
    fn response_db(h: &[f64], f: f64) -> f64 {
        let (mut re, mut im) = (0.0, 0.0);
        for (n, v) in h.iter().enumerate() {
            let w = 2.0 * PI * f * n as f64;
            re += v * w.cos();
            im -= v * w.sin();
        }
        20.0 * (re * re + im * im).sqrt().log10()
    }

    /// Worst passband deviation (dB) and worst stopband level (dB).
    fn measure(h: &[f64], pass: f64, stop: f64) -> (f64, f64) {
        let steps = 4000;
        let ripple = (0..=steps)
            .map(|i| response_db(h, pass * i as f64 / steps as f64).abs())
            .fold(0.0, f64::max);
        let stopband = (0..=steps)
            .map(|i| response_db(h, stop + (0.5 - stop) * i as f64 / steps as f64))
            .fold(f64::MIN, f64::max);
        (ripple, stopband)
    }

    #[test]
    fn every_stage_meets_its_specification() {
        let f = filters();
        // Stage 1 at DSD64: pass 44.1 kHz, stop from 308.7 kHz.
        let (ripple, stop) = measure(&f.stage1, 44_100.0 / 2_822_400.0, 308_700.0 / 2_822_400.0);
        assert!(ripple < 1e-4 && stop < -135.0, "stage 1: ripple {ripple} dB, stopband {stop} dB");
        // Half-band at 352.8 kHz: pass 44.1 kHz, stop from 132.3 kHz.
        let (ripple, stop) = measure(&f.halfband, 0.125, 0.375);
        assert!(ripple < 1e-4 && stop < -135.0, "half-band: ripple {ripple} dB, stopband {stop} dB");
        // Final at 176.4 kHz: flat to 30 kHz, stop from 44.1 kHz.
        let (ripple, stop) = measure(&f.last, FINAL_PASS, FINAL_STOP);
        assert!(ripple < 1e-4 && stop < -135.0, "final: ripple {ripple} dB, stopband {stop} dB");
        // Linear phase, unity gain.
        for h in [&f.stage1, &f.halfband, &f.last] {
            assert!((h.iter().sum::<f64>() - 1.0).abs() < 1e-12);
            for (a, b) in h.iter().zip(h.iter().rev()) {
                assert!((a - b).abs() < 1e-15);
            }
        }
        // Half-band shape: centre 1/2, every second tap zero.
        let c = f.halfband.len() / 2;
        assert_eq!(f.halfband[c], 0.5);
        assert!(f.halfband.iter().enumerate().all(|(n, v)| n == c || n.abs_diff(c) % 2 == 1 || *v == 0.0));
    }

    // The byte tables compute exactly the 112-tap FIR over the +/-1 bits
    // (oldest bit = MSB): a wrong bit order or table index fails here.
    #[test]
    fn byte_tables_equal_the_direct_filter() {
        let f = filters();
        let mut fir = ByteFir::new(&f.stage1_tables);
        let mut state = 0x1234_5678u32;
        let mut bits: Vec<f64> = Vec::new();
        // The history starts as idle bytes.
        for _ in 0..f.stage1_tables.len() {
            bits.extend((0..8).map(|t| if (DSD_IDLE_BYTE >> (7 - t)) & 1 == 1 { 1.0 } else { -1.0 }));
        }
        for _ in 0..500 {
            state = state.wrapping_mul(1_664_525).wrapping_add(1_013_904_223);
            let byte = (state >> 24) as u8;
            bits.extend((0..8).map(|t| if (byte >> (7 - t)) & 1 == 1 { 1.0 } else { -1.0 }));
            let got = fir.push(byte);
            let newest = bits.len() - 1;
            let want: f64 = f.stage1.iter().enumerate().map(|(j, h)| h * bits[newest - j]).sum();
            assert!((got - want).abs() < 1e-12, "{got} vs {want}");
        }
        // An all-ones stream is +1.0 (DC gain 1), all-zeros -1.0.
        let mut fir = ByteFir::new(&f.stage1_tables);
        let last = (0..20).map(|_| fir.push(0xFF)).last().unwrap();
        assert!((last - 1.0).abs() < 1e-12);
    }

    #[test]
    fn idle_pattern_converts_to_silence() {
        for rate in [2_822_400, 5_644_800, 11_289_600, 3_072_000] {
            let s = DsdStream {
                container: Container::Dff,
                rate_hz: rate,
                base_hz: classify_rate(rate).unwrap().0,
                multiple: classify_rate(rate).unwrap().1,
                channels: 2,
                layout: Layout::Interleaved,
                data_offset: 0,
                samples: 400_000 * 8,
                tag_offset: None,
            };
            let mut conv = PcmConverter::new(&s, &start());
            let mut out = Vec::new();
            conv.convert(&vec![vec![DSD_IDLE_BYTE; 400_000]; 2], &mut out);
            assert!(!out.is_empty());
            let peak = out.iter().fold(0.0f64, |m, v| m.max(v.abs()));
            // -120 dB: the pattern's lines at multiples of rate/8 are in
            // stage 1's stopband (they alias to DC when it decimates).
            assert!(peak < 1e-6, "DSD{}: idle came out at {peak}", rate / s.base_hz);
        }
    }

    // ── Converter quality ──────────────────────────────────────────

    /// 5th-order error-feedback sigma-delta modulator for the tests: NTF
    /// with one zero at DC and two pairs at the Legendre-optimal points
    /// of the 20 kHz band (+/-0.538, +/-0.906 of the band edge), and
    /// Butterworth high-pass poles placed for a peak NTF gain of 1.5
    /// (the usual stability rule for a 1-bit quantiser). Returns
    /// MSB-first bytes of a sine `amp * sin(2 pi freq t + phase)`.
    pub(crate) fn sdm_bytes(rate: u32, bits: usize, amp: f64, freq: f64, phase: f64) -> Vec<u8> {
        let (num, den) = ntf(f64::from(rate), 20_000.0, 1.5);
        // Feedback filter (NTF - 1) applied to the quantisation error.
        let mut e = [0.0f64; 6];
        let mut fb = [0.0f64; 6];
        let mut out = vec![0u8; bits / 8];
        let w = 2.0 * PI * freq / f64::from(rate);
        for n in 0..bits {
            let mut f = 0.0;
            for k in 1..=5 {
                f += (num[k] - den[k]) * e[k] - den[k] * fb[k];
            }
            let u = amp * (w * n as f64 + phase).sin() + f;
            assert!(u.abs() < 50.0, "test modulator went unstable");
            let y = if u >= 0.0 { 1.0 } else { -1.0 };
            for k in (2..=5).rev() {
                e[k] = e[k - 1];
                fb[k] = fb[k - 1];
            }
            e[1] = y - u;
            fb[1] = f;
            if y > 0.0 {
                out[n / 8] |= 0x80 >> (n % 8);
            }
        }
        out
    }

    type C = (f64, f64);
    fn cmul(a: C, b: C) -> C {
        (a.0 * b.0 - a.1 * b.1, a.0 * b.1 + a.1 * b.0)
    }
    fn cdiv(a: C, b: C) -> C {
        let d = b.0 * b.0 + b.1 * b.1;
        ((a.0 * b.0 + a.1 * b.1) / d, (a.1 * b.0 - a.0 * b.1) / d)
    }
    /// Coefficients (z^0..z^-5) of prod (1 - r_k z^-1).
    fn poly(roots: &[C]) -> [f64; 6] {
        let mut p: Vec<C> = vec![(1.0, 0.0)];
        for r in roots {
            let mut next = vec![(0.0, 0.0); p.len() + 1];
            for (i, c) in p.iter().enumerate() {
                next[i].0 += c.0;
                next[i].1 += c.1;
                let m = cmul(*c, *r);
                next[i + 1].0 -= m.0;
                next[i + 1].1 -= m.1;
            }
            p = next;
        }
        let mut out = [0.0; 6];
        for (o, c) in out.iter_mut().zip(&p) {
            *o = c.0;
        }
        out
    }
    fn ntf(fs: f64, band: f64, h_inf: f64) -> ([f64; 6], [f64; 6]) {
        let wb = 2.0 * PI * band / fs;
        let zeros: Vec<C> = [0.0, 0.538_469, -0.538_469, 0.906_180, -0.906_180]
            .iter()
            .map(|x: &f64| ((x * wb).cos(), (x * wb).sin()))
            .collect();
        let num = poly(&zeros);
        let poles = |wc: f64| -> Vec<C> {
            let omega = 2.0 * (wc / 2.0).tan();
            (0..5)
                .map(|k| {
                    let a = PI * f64::from(2 * k + 6) / 10.0;
                    let s = cdiv((omega, 0.0), (a.cos(), a.sin()));
                    cdiv((2.0 + s.0, s.1), (2.0 - s.0, -s.1))
                })
                .collect()
        };
        let gain_at_nyquist = |den: &[f64; 6]| {
            let at = |p: &[f64; 6]| p.iter().enumerate().map(|(k, c)| c * if k % 2 == 0 { 1.0 } else { -1.0 }).sum::<f64>();
            (at(&num) / at(den)).abs()
        };
        let (mut lo, mut hi) = (1e-6, PI / 2.0);
        for _ in 0..100 {
            let mid = (lo + hi) / 2.0;
            if gain_at_nyquist(&poly(&poles(mid))) < h_inf {
                lo = mid;
            } else {
                hi = mid;
            }
        }
        (num, poly(&poles(lo)))
    }

    fn fft(re: &mut [f64], im: &mut [f64]) {
        let n = re.len();
        let mut j = 0;
        for i in 1..n {
            let mut bit = n >> 1;
            while j & bit != 0 {
                j ^= bit;
                bit >>= 1;
            }
            j |= bit;
            if i < j {
                re.swap(i, j);
                im.swap(i, j);
            }
        }
        let mut len = 2;
        while len <= n {
            let ang = -2.0 * PI / len as f64;
            for start in (0..n).step_by(len) {
                for k in 0..len / 2 {
                    let (wr, wi) = ((ang * k as f64).cos(), (ang * k as f64).sin());
                    let (a, b) = (start + k, start + k + len / 2);
                    let tr = re[b] * wr - im[b] * wi;
                    let ti = re[b] * wi + im[b] * wr;
                    re[b] = re[a] - tr;
                    im[b] = im[a] - ti;
                    re[a] += tr;
                    im[a] += ti;
                }
            }
            len <<= 1;
        }
    }

    /// Converts a 1 kHz-ish sine at 50 % modulation (DSD's 0 dB) and
    /// measures the output: (peak bin, level dBFS, in-band THD+N dB,
    /// phase error in output samples).
    fn measure_tone(rate: u32) -> (usize, usize, f64, f64, f64) {
        let (base, multiple) = classify_rate(rate).unwrap();
        let out_rate = 2.0 * f64::from(base);
        let n = 1 << 15;
        // Coherent: a whole number of cycles in the FFT window.
        let bin = (1000.0 * n as f64 / out_rate).round() as usize;
        let freq = bin as f64 * out_rate / n as f64;
        let warm = 2048;
        let decimation = (rate / (2 * base)) as usize;
        let bits = (n + warm + 256) * decimation;
        let phase = 0.7;
        let data = sdm_bytes(rate, bits, 0.5, freq, phase);
        let s = DsdStream {
            container: Container::Dff,
            rate_hz: rate,
            base_hz: base,
            multiple,
            channels: 1,
            layout: Layout::Interleaved,
            data_offset: 0,
            samples: bits as u64,
            tag_offset: None,
        };
        let mut conv = PcmConverter::new(&s, &start());
        let mut out = Vec::new();
        for chunk in data.chunks(4096) {
            conv.convert(&[chunk.to_vec()], &mut out);
        }
        let mut re: Vec<f64> = out[warm..warm + n].to_vec();
        let mut im = vec![0.0; n];
        // Phase of output sample `warm` vs. the input sine at that time.
        let fit = |k: usize| out[k] - 0.5 * (2.0 * PI * freq * k as f64 / out_rate + phase).sin();
        let residual = (warm..warm + 2000).map(|k| fit(k).powi(2)).sum::<f64>() / 2000.0;
        fft(&mut re, &mut im);
        let power: Vec<f64> = (0..n / 2).map(|k| re[k] * re[k] + im[k] * im[k]).collect();
        let peak = (1..n / 2).max_by(|a, b| power[*a].total_cmp(&power[*b])).unwrap();
        let level = 20.0 * (2.0 * power[bin].sqrt() / n as f64).log10();
        let lo = (20.0 * n as f64 / out_rate).ceil() as usize;
        let hi = (20_000.0 * n as f64 / out_rate).floor() as usize;
        let noise: f64 = (lo..=hi).filter(|k| *k != bin).map(|k| power[k]).sum();
        let thd_n = 10.0 * (noise / power[bin]).log10();
        // Residual vs. the ideal, time-aligned sine (all bands): a
        // timing error of d samples leaves ~ (2 pi f d / rate) * amp.
        let timing = residual.sqrt() / (0.5 * 2.0 * PI * freq / out_rate);
        (peak, bin, level, thd_n, timing)
    }

    // DSD64 from a 5th-order modulator: the output is the tone at the
    // right frequency, at -6.02 dBFS (DSD's 0 dB = 50 % modulation ->
    // -6 dB headroom), time-aligned, with in-band (20 Hz - 20 kHz) THD+N
    // at the test modulator's own noise floor: measured -120 dB at
    // DSD64, the threshold 6 dB above it (the modulator is chaotic, so a
    // different libm can move its floor a little). Any converter defect
    // lands far above that: the shaped noise above 100 kHz is around
    // -10 dBFS, so a stopband 50 dB short of spec, or a stage aliasing,
    // shows up at -90 dB or worse; a bit-order or table bug destroys
    // the tone altogether.
    #[test]
    fn dsd64_converts_a_tone_cleanly() {
        let (peak, bin, level, thd_n, timing) = measure_tone(2_822_400);
        assert_eq!(peak, bin, "the tone is at the bin it was made for");
        assert!((level + 6.0206).abs() < 0.02, "level {level} dBFS");
        assert!(thd_n < -114.0, "THD+N {thd_n} dB");
        assert!(timing < 0.6, "output is {timing} samples off the input's time");
    }

    // Higher rates have less in-band modulator noise: measured -152 dB
    // (DSD128), -160 dB (DSD256), -124 dB (DSD64 at 48 kHz x 64).
    #[test]
    fn dsd128_and_dsd256_and_48k_family_convert_cleanly() {
        for (rate, max_thd_n) in [(5_644_800, -140.0), (11_289_600, -140.0), (3_072_000, -114.0)] {
            let (peak, bin, level, thd_n, timing) = measure_tone(rate);
            assert_eq!(peak, bin, "{rate}");
            assert!((level + 6.0206).abs() < 0.02, "{rate}: level {level} dBFS");
            assert!(thd_n < max_thd_n, "{rate}: THD+N {thd_n} dB");
            assert!(timing < 0.6, "{rate}: {timing} samples off");
        }
    }

    // Channels stay where they are through de-blocking and interleaving.
    #[test]
    fn channels_convert_independently() {
        let rate = 2_822_400;
        let bytes = 4096 * 4;
        let bits = vec![
            sdm_bytes(rate, bytes * 8, 0.5, 1000.0, 0.0),
            vec![DSD_IDLE_BYTE; bytes],
            sdm_bytes(rate, bytes * 8, 0.25, 3000.0, 0.0),
        ];
        let file = build_dsf(rate, &bits, false, None, Some(&id3_tag(&[])));
        let s = parse(&mut Cursor::new(&file)).unwrap();
        let plan = from_start(&s);
        let mut conv = PcmConverter::new(&s, &plan);
        let mut out = Vec::new();
        conv.convert(&read_all(&file, &s, &plan), &mut out);
        conv.finish(&mut out);
        let rms = |ch: usize| {
            let v: Vec<f64> = out.iter().skip(ch).step_by(3).skip(500).copied().collect();
            (v.iter().map(|x| x * x).sum::<f64>() / v.len() as f64).sqrt()
        };
        assert!((rms(0) - 0.5 / 2f64.sqrt()).abs() < 0.01, "ch0 {}", rms(0));
        assert!(rms(1) < 1e-6, "ch1 {}", rms(1));
        assert!((rms(2) - 0.25 / 2f64.sqrt()).abs() < 0.01, "ch2 {}", rms(2));
    }

    // The padding of the last block (zeros = a full-scale -1 step) and
    // the ID3 chunk after the data must never reach the output.
    #[test]
    fn padding_and_the_trailing_tag_are_not_played() {
        let bits = vec![vec![DSD_IDLE_BYTE; 5000]; 2];
        let garbage: Vec<u8> = (0..20_000).map(|i| (i * 37 % 256) as u8).collect();
        let mut tag = b"ID3\x04\x00\x00\x00\x01\x1c\x20".to_vec();
        tag.extend(garbage);
        let file = build_dsf(2_822_400, &bits, false, None, Some(&tag));
        let s = parse(&mut Cursor::new(&file)).unwrap();
        let plan = from_start(&s);
        let mut conv = PcmConverter::new(&s, &plan);
        let mut out = Vec::new();
        conv.convert(&read_all(&file, &s, &plan), &mut out);
        conv.finish(&mut out);
        assert_eq!(out.len() as u64, 2 * (5000 * 8 / 32));
        let peak = out.iter().fold(0.0f64, |m, v| m.max(v.abs()));
        assert!(peak < 1e-6, "peak {peak}");

        // DoP likewise: exactly the audio's frames, all idle payloads.
        let mut packer = DopPacker::new(&plan);
        let mut words = Vec::new();
        packer.pack(&read_all(&file, &s, &plan), &mut words);
        packer.finish(&mut words);
        assert_eq!(words.len(), 2 * 2500);
        assert!(words.iter().all(|w| *w == DOP_IDLE_PAYLOAD));
    }

    // ── DoP ────────────────────────────────────────────────────────

    #[test]
    fn dop_payload_takes_dsf_bits_oldest_first() {
        // DSF byte 0b0000_0001 is LSB-first: its oldest bit is the 1.
        // DoP wants the oldest bit at the top of its 16.
        let file = build_dsf(2_822_400, &[vec![0x80, 0x00, 0xC0, 0x01]], false, None, None);
        assert_eq!(file[92], 0x01, "the fixture stores the 1 bit first in time at bit 0");
        let s = parse(&mut Cursor::new(&file)).unwrap();
        let plan = from_start(&s);
        let mut packer = DopPacker::new(&plan);
        let mut words = Vec::new();
        packer.pack(&read_all(&file, &s, &plan), &mut words);
        assert_eq!(words, vec![0x0080_0000, 0x00C0_0100]);
        assert_eq!(dop_payload(0xFF, 0x00), 0x00FF_0000);
        assert_eq!(dop_word(dop_payload(0x80, 0x01), DOP_MARKER_05) as u32, 0x0580_0100);
        assert_eq!(dop_word(dop_payload(0x80, 0x01), DOP_MARKER_FA) as u32, 0xFA80_0100);
    }

    #[test]
    fn dop_markers_alternate_per_frame_through_idle_and_periods() {
        let mut marker = DopMarker::new();
        let mut words = vec![0i32; 6 * 2];
        let mut n = 0;
        // Frames 0, 2, 4 have audio; 1, 3, 5 are an underrun (idle).
        dop_period(&mut words, 2, &mut marker, |frame| {
            n += 1;
            frame.fill(dop_payload(0x12, 0x34));
            n % 2 == 1
        });
        let mut next = vec![0i32; 2 * 2];
        dop_period(&mut next, 2, &mut marker, |_| false); // paused
        words.extend(next);
        for (f, frame) in words.chunks(2).enumerate() {
            let want = if f % 2 == 0 { 0x05 } else { 0xFA };
            for w in frame {
                assert_eq!((*w as u32) >> 24, want, "frame {f}: markers alternate per frame");
            }
            let payload = frame[0] & 0x00FF_FF00;
            if f < 6 && f % 2 == 0 {
                assert_eq!(payload, 0x0012_3400);
            } else {
                assert_eq!(payload, DOP_IDLE_PAYLOAD, "frame {f} is the idle pattern, not zeros");
            }
            assert_eq!(frame[0], frame[1]);
        }
        assert_eq!(DOP_IDLE_PAYLOAD, 0x0069_6900);
    }

    #[test]
    fn dop_packer_pairs_bytes_across_chunks_and_seeks_whole_frames() {
        let mut packer = DopPacker::new(&start());
        let mut words = Vec::new();
        packer.pack(&[vec![1, 2, 3], vec![11, 12, 13]], &mut words);
        packer.pack(&[vec![4], vec![14]], &mut words);
        packer.pack(&[vec![5], vec![15]], &mut words);
        packer.finish(&mut words);
        assert_eq!(
            words,
            vec![
                dop_payload(1, 2), dop_payload(11, 12),
                dop_payload(3, 4), dop_payload(13, 14),
                dop_payload(5, DSD_IDLE_BYTE), dop_payload(15, DSD_IDLE_BYTE),
            ]
        );
        // A seek's leftover inside its block is dropped in whole frames.
        let mut packer = DopPacker::new(&SeekPlan { skip_samples: 35, ..start() });
        let mut words = Vec::new();
        packer.pack(&[vec![1, 2, 3, 4, 5, 6]], &mut words);
        assert_eq!(words, vec![dop_payload(5, 6)]);
    }

    #[test]
    fn dop_is_used_only_when_everything_allows_it() {
        let dsd64 = DsdStream {
            container: Container::Dsf,
            rate_hz: 2_822_400,
            base_hz: 44_100,
            multiple: 64,
            channels: 2,
            layout: Layout::Blocks { block_bytes: 4096, lsb_first: true },
            data_offset: 92,
            samples: 1,
            tag_offset: None,
        };
        let all = || DopRequest { enabled: true, exclusive: true, volume: 1.0, gain: 1.0 };
        assert_eq!(dop_rate_for(true, &dsd64, &all()), Some(176_400));
        let dsd128 = DsdStream { rate_hz: 5_644_800, multiple: 128, ..dsd64 };
        assert_eq!(dop_rate_for(true, &dsd128, &all()), Some(352_800));
        let dsd64_48 = DsdStream { rate_hz: 3_072_000, base_hz: 48_000, ..dsd64 };
        assert_eq!(dop_rate_for(true, &dsd64_48, &all()), Some(192_000));

        // Any one condition missing: PCM conversion.
        let dsd256 = DsdStream { rate_hz: 11_289_600, multiple: 256, ..dsd64 };
        assert_eq!(dop_rate_for(true, &dsd256, &all()), None);
        assert_eq!(dop_rate_for(false, &dsd64, &all()), None, "no DoP off Windows");
        assert_eq!(dop_rate_for(true, &dsd64, &DopRequest { enabled: false, ..all() }), None);
        assert_eq!(dop_rate_for(true, &dsd64, &DopRequest { exclusive: false, ..all() }), None);
        assert_eq!(dop_rate_for(true, &dsd64, &DopRequest { volume: 0.99, ..all() }), None);
        assert_eq!(dop_rate_for(true, &dsd64, &DopRequest { volume: 0.0, ..all() }), None);
        assert_eq!(dop_rate_for(true, &dsd64, &DopRequest { gain: 0.5, ..all() }), None);
        assert_eq!(dop_rate_for(true, &dsd64, &DopRequest { gain: 1.0001, ..all() }), None);
        if !cfg!(target_os = "windows") {
            assert_eq!(dop_rate(&dsd64, &all()), None);
        }
    }

    // ── ReplayGain tags ────────────────────────────────────────────

    #[test]
    fn replay_gain_frames_are_found_past_the_cover_art() {
        let tag = id3_tag(&[("REPLAYGAIN_TRACK_GAIN", "-6.50 dB"), ("replaygain_album_peak", "0.98")]);
        let file = build_dsf(2_822_400, &ramp(2, 100), false, None, Some(&tag));
        let s = parse(&mut Cursor::new(&file)).unwrap();
        let pairs = read_txxx(&mut Cursor::new(&file), &s).unwrap();
        assert_eq!(
            pairs,
            vec![
                ("REPLAYGAIN_TRACK_GAIN".to_string(), "-6.50 dB".to_string()),
                ("replaygain_album_peak".to_string(), "0.98".to_string()),
            ]
        );

        // DSDIFF: the tag is in an "ID3 " chunk after the sound data.
        let trailer = [dff_chunk(b"DIIN", &[0; 5]), dff_chunk(b"ID3 ", &tag)].concat();
        let file = build_dff(2_822_400, &ramp(2, 100), b"DSD ", &trailer);
        let s = parse(&mut Cursor::new(&file)).unwrap();
        assert_eq!(read_txxx(&mut Cursor::new(&file), &s).unwrap().len(), 2);

        // No tag at all.
        let file = build_dsf(2_822_400, &ramp(1, 10), false, None, None);
        let s = parse(&mut Cursor::new(&file)).unwrap();
        assert!(read_txxx(&mut Cursor::new(&file), &s).unwrap().is_empty());
    }

    #[test]
    fn txxx_text_encodings() {
        assert_eq!(parse_txxx(b"\x00DESC\x00-1.00 dB"), Some(("DESC".into(), "-1.00 dB".into())));
        assert_eq!(parse_txxx(b"\x03D\xc3\xa9\x00v\x00"), Some(("Dé".into(), "v".into())));
        // UTF-16 with BOMs.
        let mut b = vec![1u8, 0xFF, 0xFE, b'A', 0, b'B', 0, 0, 0, 0xFF, 0xFE, b'1', 0];
        assert_eq!(parse_txxx(&b), Some(("AB".into(), "1".into())));
        b = vec![2u8, 0, b'A', 0, 0, 0, b'2'];
        assert_eq!(parse_txxx(&b), Some(("A".into(), "2".into())));
        assert_eq!(parse_txxx(b"\x09junk"), None);
        assert_eq!(parse_txxx(b""), None);
    }
}
