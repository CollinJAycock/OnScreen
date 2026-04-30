// Native audio engine — v2.1 Track E.
//
// Threading model (the part that's easy to get wrong):
//
//   ┌──────────────────────────┐    ┌────────────────────────┐
//   │ decoder thread           │    │ cpal output callback   │
//   │  ─ HTTP GET (ureq)       │ →  │  ─ realtime, no alloc  │
//   │  ─ claxon decode         │    │  ─ pulls from ringbuf  │
//   │  ─ push to ringbuf       │    │  ─ writes to driver    │
//   └──────────────────────────┘    └────────────────────────┘
//
// The ringbuf is the contract: lock-free SPSC, the audio callback
// must NOT take a mutex or do I/O (blocking the callback drops
// samples = audible glitch). The decoder thread is the only writer;
// the cpal callback closure is the only reader.
//
// The cpal stream's sample format + rate is configured to match the
// FLAC's native rate + bit depth — that's the bit-perfect contract.
// 16-bit FLAC → I16 stream; 24-bit FLAC → I32 stream with the
// low byte zero (cpal exposes no I24). Resampling is the OS mixer's
// job in default mode, but we still avoid pre-mixer resampling so
// when the user opts into exclusive mode (next commit) the bytes
// remain bit-perfect.
//
// Scope of this commit: device enumeration + test-tone (carried
// over from the foundation) + FLAC streaming end-to-end (play_url +
// stop). Not yet: pause/resume (cpal::Stream::pause is per-host
// flaky on ALSA — we'll wire it via a "paused" atomic the callback
// checks instead, next commit), exclusive-mode toggle, gapless,
// replay-gain.

use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{SampleFormat, SizedSample};
use ringbuf::traits::*;
use ringbuf::HeapRb;
use serde::Serialize;
use std::io::Read;
use std::sync::atomic::{AtomicBool, AtomicI32, AtomicU64, AtomicU8, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;

// ── ReplayGain ─────────────────────────────────────────────────────────────
//
// ReplayGain normalises perceived loudness across a catalog so the user
// doesn't have to chase the volume knob across tracks. We read the
// REPLAYGAIN_TRACK_GAIN / _PEAK / _ALBUM_GAIN / _ALBUM_PEAK tags out
// of the file at pipeline-prepare time, compute a single linear scale
// factor based on the user's current mode + preamp, and multiply
// every f32 sample by that factor in the decoder thread before
// convert-back-to-int. One multiply per sample = single-digit ns on
// modern CPUs at any sample rate we care about.
//
// Mode + preamp live as atomics so the frontend can change them
// without holding the engine lock; the change applies on the next
// track since the factor is captured at pipeline-prepare time
// (changing it mid-track would need a fresh pipeline anyway, which
// is what `audio_seek` does — and the user's track-change cadence
// is the natural reapplication point).

const REPLAY_GAIN_MODE_OFF: u8 = 0;
const REPLAY_GAIN_MODE_TRACK: u8 = 1;
const REPLAY_GAIN_MODE_ALBUM: u8 = 2;

static REPLAY_GAIN_MODE: AtomicU8 = AtomicU8::new(REPLAY_GAIN_MODE_OFF);
/// Preamp in 0.1 dB units (so an i32 covers ±214 million dB without
/// floating-point comparisons in the atomic). Default 0 = no adjust.
static REPLAY_GAIN_PREAMP_DB_X10: AtomicI32 = AtomicI32::new(0);

#[derive(Default, Clone, Copy)]
struct ReplayGainTags {
    track_gain_db: Option<f32>,
    track_peak: Option<f32>,
    album_gain_db: Option<f32>,
    album_peak: Option<f32>,
}

/// Compute the linear scale factor to apply to f32 samples. Returns
/// 1.0 when the mode is off, or when no relevant tag was found —
/// the latter is a soft fallback so a partially-tagged catalog
/// doesn't go silent.
///
/// Peak limiting: if a positive gain would push the highest sample
/// above 1.0, we clamp to `1 / peak` to prevent clipping. Negative
/// gains never need clamping (attenuation is always safe).
///
/// Pure function — `mode` and `preamp_db` are passed explicitly so
/// the unit tests can drive every branch without touching the
/// process-wide atomics. The atomics-reading wrapper below is what
/// pipeline-prepare calls.
fn compute_gain_factor_for(tags: &ReplayGainTags, mode: u8, preamp_db: f32) -> f32 {
    if mode == REPLAY_GAIN_MODE_OFF {
        return 1.0;
    }
    let (gain_db, peak) = match mode {
        REPLAY_GAIN_MODE_TRACK => (tags.track_gain_db, tags.track_peak),
        REPLAY_GAIN_MODE_ALBUM => (
            // Fall back to track values when album tags are missing —
            // an album-tagged catalog with one orphan track should
            // still get normalised, just on track gain instead.
            tags.album_gain_db.or(tags.track_gain_db),
            tags.album_peak.or(tags.track_peak),
        ),
        _ => return 1.0,
    };
    let Some(gain_db) = gain_db else { return 1.0 };
    let total_db = gain_db + preamp_db;
    let factor = 10f32.powf(total_db / 20.0);
    // Clamp to prevent clipping when the requested gain would push
    // the loudest sample above full-scale. peak is a [0, 1] f32
    // (1.0 = full-scale); if peak * factor > 1.0, scale back so the
    // peak just reaches 1.0.
    if let Some(peak) = peak {
        if peak > 0.0 && factor * peak > 1.0 {
            return 1.0 / peak;
        }
    }
    factor
}

fn compute_gain_factor(tags: &ReplayGainTags) -> f32 {
    let mode = REPLAY_GAIN_MODE.load(Ordering::Acquire);
    let preamp_db = REPLAY_GAIN_PREAMP_DB_X10.load(Ordering::Acquire) as f32 / 10.0;
    compute_gain_factor_for(tags, mode, preamp_db)
}

/// Parse a ReplayGain gain string into dB. Tag values are typically
/// formatted "-6.50 dB" or "+3.20 dB"; some encoders drop the unit
/// or the sign, so be liberal: trim whitespace, strip a trailing
/// " dB" if present, parse the rest as f32. Returns None on garbage.
fn parse_replay_gain_db(value: &str) -> Option<f32> {
    let trimmed = value.trim();
    let stripped = trimmed
        .strip_suffix(" dB")
        .or_else(|| trimmed.strip_suffix(" db"))
        .or_else(|| trimmed.strip_suffix("dB"))
        .or_else(|| trimmed.strip_suffix("db"))
        .unwrap_or(trimmed);
    stripped.trim().parse::<f32>().ok()
}

/// Parse a ReplayGain peak string. Peaks are linear floats in [0, 1+]
/// (some encoders emit slight overshoots from intersample peaks).
fn parse_replay_gain_peak(value: &str) -> Option<f32> {
    value.trim().parse::<f32>().ok()
}

/// Match a Vorbis-comment-shaped (key, value) pair into the right
/// ReplayGainTags slot. Case-insensitive on the key — most encoders
/// uppercase but some lowercase. Returns true if the pair matched a
/// known tag (caller can use that for early-out, though we don't
/// today).
fn ingest_replay_gain_tag(tags: &mut ReplayGainTags, key: &str, value: &str) -> bool {
    let upper = key.to_ascii_uppercase();
    match upper.as_str() {
        "REPLAYGAIN_TRACK_GAIN" => {
            tags.track_gain_db = parse_replay_gain_db(value);
            true
        }
        "REPLAYGAIN_TRACK_PEAK" => {
            tags.track_peak = parse_replay_gain_peak(value);
            true
        }
        "REPLAYGAIN_ALBUM_GAIN" => {
            tags.album_gain_db = parse_replay_gain_db(value);
            true
        }
        "REPLAYGAIN_ALBUM_PEAK" => {
            tags.album_peak = parse_replay_gain_peak(value);
            true
        }
        _ => false,
    }
}

#[tauri::command]
pub fn replay_gain_set_mode(mode: String) -> Result<(), String> {
    let v = match mode.as_str() {
        "off" => REPLAY_GAIN_MODE_OFF,
        "track" => REPLAY_GAIN_MODE_TRACK,
        "album" => REPLAY_GAIN_MODE_ALBUM,
        other => return Err(format!("audio: unknown ReplayGain mode {other:?}")),
    };
    REPLAY_GAIN_MODE.store(v, Ordering::Release);
    Ok(())
}

#[tauri::command]
pub fn replay_gain_set_preamp(db: f32) -> Result<(), String> {
    if !db.is_finite() {
        return Err("audio: ReplayGain preamp must be finite".into());
    }
    let clamped = db.clamp(-15.0, 15.0);
    REPLAY_GAIN_PREAMP_DB_X10.store((clamped * 10.0) as i32, Ordering::Release);
    Ok(())
}

// ── Exclusive-mode toggle ──────────────────────────────────────────────────
//
// The audiophile pillar's headline goal is bit-perfect output: samples
// reach the DAC at the source bit-depth + rate without the OS mixer
// resampling them. cpal 0.16 hard-codes shared mode on every host —
// real exclusive output needs:
//   - Windows: raw IAudioClient::Initialize with AUDCLNT_SHAREMODE_EXCLUSIVE
//   - macOS:   AudioObjectSetPropertyData with kAudioDevicePropertyHogMode
//   - Linux:   ALSA `hw:` device + tuned period_size
//
// All three are platform-specific lifts. Until they land, this flag
// drives the most we *can* do through cpal: tighten the cpal buffer-
// size hint so the OS mixer's resampler runs at lower latency (still
// resamples — just less buffering before the user hears the change).
// It also gates the future per-platform modules; when those ship,
// the flag flips them on without touching the call sites.

static EXCLUSIVE_MODE: AtomicBool = AtomicBool::new(false);

// Active-backend reporting. The user-visible "is bit-perfect actually
// engaged?" signal — the EXCLUSIVE_MODE toggle just *requests* exclusive,
// but the WASAPI open can fall back to cpal silently when the device
// rejects the format or another app holds it. ACTIVE_BACKEND tracks
// what we're really running on, so the settings UI can render a
// "Currently: WASAPI exclusive" / "cpal shared" badge instead of
// trusting the toggle's "intended" state.
//
// Encoded as a u8 because AtomicU8 is one cycle to load/store on
// every platform and the enum has only 4 cases. See
// audio_get_active_backend below for the wire format.
const BACKEND_NONE: u8 = 0;        // No active playback.
const BACKEND_CPAL_SHARED: u8 = 1; // cpal default (BufferSize::Default).
const BACKEND_CPAL_TIGHT: u8 = 2;  // cpal with EXCLUSIVE_MODE on (Fixed buffer).
const BACKEND_WASAPI_EXCLUSIVE: u8 = 3; // raw WASAPI exclusive — bit-perfect.
static ACTIVE_BACKEND: AtomicU8 = AtomicU8::new(BACKEND_NONE);

#[tauri::command]
pub fn audio_set_exclusive_mode(enabled: bool) -> Result<(), String> {
    EXCLUSIVE_MODE.store(enabled, Ordering::Release);
    Ok(())
}

#[tauri::command]
pub fn audio_get_exclusive_mode() -> Result<bool, String> {
    Ok(EXCLUSIVE_MODE.load(Ordering::Acquire))
}

/// Reports the audio backend currently running. Used by the settings
/// UI to surface whether the EXCLUSIVE_MODE toggle actually engaged
/// (WASAPI exclusive on Windows + a supporting device) or whether
/// the open silently fell back to cpal. The returned strings are
/// stable wire identifiers; the frontend maps them to user-facing
/// labels.
#[tauri::command]
pub fn audio_get_active_backend() -> Result<&'static str, String> {
    Ok(match ACTIVE_BACKEND.load(Ordering::Acquire) {
        BACKEND_CPAL_SHARED => "cpal-shared",
        BACKEND_CPAL_TIGHT => "cpal-tight",
        BACKEND_WASAPI_EXCLUSIVE => "wasapi-exclusive",
        _ => "none",
    })
}

/// Public-safe device descriptor returned by [`list_audio_devices`].
#[derive(Serialize)]
pub struct AudioDevice {
    pub name: String,
    pub is_default: bool,
    /// `None` when the device exposes no output configs (an input-
    /// only device — surface them anyway so the user knows why their
    /// USB DAC isn't appearing).
    pub default_output_summary: Option<String>,
}

#[tauri::command]
pub fn list_audio_devices() -> Result<Vec<AudioDevice>, String> {
    let host = cpal::default_host();
    let default_name = host.default_output_device().and_then(|d| d.name().ok());

    let devices = host
        .output_devices()
        .map_err(|e| format!("audio: enumerate output devices: {e}"))?;

    let mut out = Vec::new();
    for dev in devices {
        let name = dev.name().unwrap_or_else(|_| "<unnamed>".to_string());
        let is_default = default_name.as_deref() == Some(name.as_str());
        let default_output_summary = dev.default_output_config().ok().map(|c| {
            format!(
                "{} ch · {} Hz · {:?}",
                c.channels(),
                c.sample_rate().0,
                c.sample_format()
            )
        });
        out.push(AudioDevice {
            name,
            is_default,
            default_output_summary,
        });
    }
    Ok(out)
}

// ── Engine state ─────────────────────────────────────────────────────────────

/// Reported on `audio_state` so the frontend can render transport
/// state without polling individual fields. `playing` is true while
/// the engine has an active source (paused stream still counts as
/// "playing"); `paused` toggles independently and only matters
/// when `playing` is true.
///
/// `position_ms` is derived from total frames written to the cpal
/// callback, which is what actually came out of the speakers (close
/// enough — buffer-induced latency is sub-100ms on every host
/// we care about). `ended` reports whether the decoder has reached
/// EOS — the AudioPlayer polls this for auto-advance to the next
/// queue entry without needing a separate event channel.
#[derive(Serialize)]
pub struct PlaybackStatus {
    pub playing: bool,
    pub paused: bool,
    pub ended: bool,
    pub position_ms: u64,
    pub source_url: Option<String>,
    pub sample_rate_hz: Option<u32>,
    pub bit_depth: Option<u32>,
    pub channels: Option<u16>,
}

/// Output-side stream for an active playback. cpal handles the cross-
/// platform default; the WASAPI variant exists only on Windows when
/// EXCLUSIVE_MODE flips on and IsFormatSupported returns Ok-no-alt for
/// the file's native format. Drop on either variant cleans up the
/// underlying device handle.
///
/// allow(dead_code) on the fields: they're held for their Drop side
/// effects (cpal::Stream stops + releases the device on drop, the
/// WASAPI variant signals + joins its render thread). Reading the
/// inner value is never required.
#[allow(dead_code)]
enum ActiveStream {
    Cpal(cpal::Stream),
    #[cfg(target_os = "windows")]
    Wasapi(crate::windows_exclusive::WasapiStream),
}

struct ActivePlayback {
    // Output-side handle. cpal::Stream's drop stops + releases the
    // device; the WASAPI variant signals its render thread to exit
    // and joins. Either way ActivePlayback stays alive as long as
    // playback does — we hold it in the engine's Mutex.
    _stream: ActiveStream,
    // The decoder thread checks this between FLAC frames and
    // returns when set, releasing its end of the ringbuf so the
    // cpal callback drains to silence and ends. Atomic so no lock
    // is needed inside the realtime callback.
    stop_flag: Arc<AtomicBool>,
    // When true, the cpal callback writes silence (T::EQUILIBRIUM)
    // instead of pulling from the ringbuf. The decoder thread
    // doesn't need to check this — natural ringbuf backpressure
    // (it sleeps when full) keeps decode work bounded while paused.
    // Atomic so the realtime callback can read it without a lock.
    paused: Arc<AtomicBool>,
    // Set true when the decoder thread exits cleanly (claxon
    // returned None — EOS — rather than the stop_flag firing).
    // The frontend polls this on audio_state to fire next() in the
    // queue without needing a separate event channel back from
    // Rust. The cpal callback may continue writing buffered samples
    // for a few hundred ms after this flips; that's fine because
    // the position_ms keeps advancing and the auto-advance only
    // needs a "track is logically over" signal.
    ended: Arc<AtomicBool>,
    // Total frames (samples per channel) written to the cpal
    // callback. AcquireRelease ordering on the load is enough since
    // we only need eventual consistency for a UI position display.
    // Divided by sample_rate_hz on read to get milliseconds.
    frames_written: Arc<AtomicU64>,
    decoder_handle: Option<JoinHandle<()>>,
    source_url: String,
    sample_rate_hz: u32,
    bit_depth: u32,
    channels: u16,
}

impl Drop for ActivePlayback {
    fn drop(&mut self) {
        // Signal decoder + join so the thread doesn't outlive its
        // ringbuf producer. Join with a timeout would be nicer; for
        // the foundation, a clean stop in <1s is the realistic case
        // — the decoder polls stop_flag between FLAC blocks (each
        // ~4-12 KB on disk = ms of audio).
        self.stop_flag.store(true, Ordering::Release);
        if let Some(h) = self.decoder_handle.take() {
            let _ = h.join();
        }
    }
}

/// Engine state. `current` is what's playing (or paused) right now;
/// `preload` is the next track, fully decoding into a ringbuf in
/// the background so the audio_play_url call that promotes it
/// skips the HTTP + FLAC-header round-trip and the gap between
/// tracks shrinks to near-zero. Mirrors the existing dual-`<audio>`
/// rotation in the web player conceptually.
struct EngineState {
    current: Option<ActivePlayback>,
    preload: Option<PreloadSlot>,
}

static ENGINE: Mutex<EngineState> = Mutex::new(EngineState {
    current: None,
    preload: None,
});

/// A track that's been HTTP-fetched + FLAC-header-parsed and whose
/// decoder thread is already producing samples into a ringbuf,
/// waiting for the cpal side to be opened. Held in
/// [`EngineState::preload`] until the matching `audio_play_url` call
/// promotes it — when promoted, the consumer + decoder thread move
/// straight into the new ActivePlayback so no work is wasted.
///
/// Drop signals the decoder + joins so an unconsumed preload (user
/// changed their mind, queue reordered) cleans up cleanly without
/// orphaning the decoder thread.
struct PreloadSlot {
    source_url: String,
    sample_rate_hz: u32,
    bit_depth: u32,
    channels: u16,
    stop_flag: Arc<AtomicBool>,
    ended: Arc<AtomicBool>,
    decoder_handle: Option<JoinHandle<()>>,
    // Optional so promote can take() it out without moving out of
    // the struct (which the Drop impl would forbid). After take,
    // the struct still drops cleanly — there's just nothing for
    // Drop to release on the consumer side (the cpal stream now
    // owns it).
    consumer: Option<PreloadConsumer>,
}

impl Drop for PreloadSlot {
    fn drop(&mut self) {
        self.stop_flag.store(true, Ordering::Release);
        if let Some(h) = self.decoder_handle.take() {
            let _ = h.join();
        }
    }
}

/// Consumer side of the preload ringbuf, type-erased over the
/// sample format so [`PreloadSlot`] doesn't need to be generic.
/// 16-bit FLAC produces an I16 consumer (cpal stream config will
/// be I16); ≥17-bit produces I32 (24-bit-in-32 packing). The
/// promote step matches on this enum to dispatch to the right
/// `open_cpal_stream<T>` instantiation.
enum PreloadConsumer {
    I16(<HeapRb<i16> as Split>::Cons),
    I32(<HeapRb<i32> as Split>::Cons),
}

#[tauri::command]
pub fn audio_state() -> Result<PlaybackStatus, String> {
    let engine = ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())?;
    Ok(match &engine.current {
        Some(p) => {
            let frames = p.frames_written.load(Ordering::Acquire);
            // ms = frames * 1000 / rate. Saturating math because a
            // multi-hour DSD stream at 11.2 MHz would otherwise overflow
            // the intermediate u64 — cheap insurance even if 99.99% of
            // FLAC inputs stay under 192 kHz × 24h = 1.6 × 10^10 frames.
            let position_ms = frames
                .saturating_mul(1000)
                / (p.sample_rate_hz.max(1) as u64);
            PlaybackStatus {
                playing: true,
                paused: p.paused.load(Ordering::Acquire),
                ended: p.ended.load(Ordering::Acquire),
                position_ms,
                source_url: Some(p.source_url.clone()),
                sample_rate_hz: Some(p.sample_rate_hz),
                bit_depth: Some(p.bit_depth),
                channels: Some(p.channels),
            }
        }
        None => PlaybackStatus {
            playing: false,
            paused: false,
            ended: false,
            position_ms: 0,
            source_url: None,
            sample_rate_hz: None,
            bit_depth: None,
            channels: None,
        },
    })
}

#[tauri::command]
pub fn stop_audio() -> Result<(), String> {
    let mut engine = ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())?;
    // Clearing both slots so an in-flight preload also stops —
    // otherwise stop_audio would silently leave a decoder thread
    // running for a track the user explicitly cancelled.
    engine.current = None;
    engine.preload = None;
    ACTIVE_BACKEND.store(BACKEND_NONE, Ordering::Release);
    Ok(())
}

/// Pauses the active stream by flipping the engine's pause flag —
/// the realtime cpal callback then writes silence on every tick.
/// Decoder backpressure handles itself: the ringbuf fills up,
/// decoder sleeps, no extra CPU burned during a pause.
///
/// No-op when nothing is playing (avoids surprising the UI which
/// might fire pause optimistically before the engine state caught
/// up with a stop).
#[tauri::command]
pub fn audio_pause() -> Result<(), String> {
    let engine = ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())?;
    if let Some(p) = engine.current.as_ref() {
        p.paused.store(true, Ordering::Release);
    }
    Ok(())
}

/// Resumes a paused stream. Symmetric with `audio_pause`; same no-op
/// semantics when nothing is playing.
#[tauri::command]
pub fn audio_resume() -> Result<(), String> {
    let engine = ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())?;
    if let Some(p) = engine.current.as_ref() {
        p.paused.store(false, Ordering::Release);
    }
    Ok(())
}

// ── FLAC streaming play_url + preload ───────────────────────────────────────

/// Streams a FLAC file from `url` (carrying `Authorization: Bearer
/// <bearer_token>` when supplied) and plays it on the named device.
/// Replaces any currently-playing track — the caller doesn't need
/// to call `stop_audio` first.
///
/// **Gapless fast-path:** if the matching URL has been prepared
/// via [`audio_preload_url`], promotion skips the HTTP +
/// FLAC-header round-trip entirely — the decoder thread is already
/// producing samples into a ringbuf, and we just open the cpal
/// stream around the existing consumer. Inter-track silence drops
/// from ~200-500 ms (cold start) to whatever the cpal device
/// activation costs (~10-20 ms on every host we care about).
///
/// Returns when playback has *started* (the cpal stream is running
/// and the decoder thread is producing samples). Errors out
/// synchronously on the parts that can fail before audio starts:
/// device pick, FLAC header parse, cpal config build.
///
/// FLAC only. Other formats fall through to the existing `<audio>`
/// element in the webview.
#[tauri::command]
pub fn audio_play_url(
    url: String,
    bearer_token: Option<String>,
    device_name: Option<String>,
) -> Result<PlaybackStatus, String> {
    let device = pick_output_device(device_name.as_deref())?;

    // Two paths: gapless promote when we have a matching preload,
    // cold-start otherwise. Both end with current = Some(active).
    let prepared = take_preload_for(&url)?
        .map(Ok)
        .unwrap_or_else(|| prepare_pipeline(&url, bearer_token.as_deref(), 0))?;

    let active = open_active_from_prepared(prepared, &device, url, 0)?;

    {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        // Drop the old current AFTER the new one's stream is built —
        // releasing its decoder thread + cpal device a moment late
        // is harmless and keeps the swap atomic from the user's POV
        // (no period of "current is None" the polling loop could
        // catch in between).
        engine.current = Some(active);
    }

    audio_state()
}

/// Prepares the next track in the background so the matching
/// [`audio_play_url`] call can promote it without a fresh HTTP +
/// claxon round-trip. Replaces any existing preload (the previous
/// one's drop signals its decoder thread to stop and joins).
///
/// Frontend calls this whenever the upcoming track changes (queue
/// reorder, shuffle toggle, app launch with a queue restored).
/// Safe to call repeatedly with the same URL — the no-op-when-
/// already-prepared check below avoids re-fetching.
#[tauri::command]
pub fn audio_preload_url(
    url: String,
    bearer_token: Option<String>,
) -> Result<(), String> {
    {
        let engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if engine
            .preload
            .as_ref()
            .map(|p| p.source_url == url)
            .unwrap_or(false)
        {
            return Ok(()); // already prepared
        }
    }
    let prepared = prepare_pipeline(&url, bearer_token.as_deref(), 0)?;
    let mut engine = ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())?;
    engine.preload = Some(prepared);
    Ok(())
}

/// Seek to `position_ms` within the currently-playing track.
///
/// Implementation note: FLAC over an HTTP streaming body has no
/// random-access primitive, so we drop the existing pipeline and
/// build a new one that drinks-and-discards samples up to the seek
/// target before producing output. Correct, simple, but bandwidth-
/// heavy for large seeks against remote servers (a 70-min jump
/// re-downloads ~70 min of audio at LAN speeds — sub-second over
/// gigabit, ~30 s over a typical home internet link). HTTP-Range +
/// FLAC frame resync would amortise this; punted to a follow-up.
///
/// Errors when nothing is currently playing — seek without context
/// is a UI bug; better to surface than silently no-op.
#[tauri::command]
pub fn audio_seek(
    position_ms: u64,
    bearer_token: Option<String>,
    device_name: Option<String>,
) -> Result<PlaybackStatus, String> {
    // Snapshot the URL before tearing the current pipeline down — we
    // need it for the re-fetch and the engine lock can't span the
    // (potentially seconds-long) HTTP + decode phase.
    let url = {
        let engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        engine
            .current
            .as_ref()
            .map(|p| p.source_url.clone())
            .ok_or_else(|| "audio: nothing playing — nothing to seek".to_string())?
    };

    let device = pick_output_device(device_name.as_deref())?;

    // Tear down current first. Dropping it stops the cpal stream + the
    // decoder thread releases its end of the ringbuf. The preload slot
    // is left alone — it points to the next queue track which the
    // seek doesn't affect.
    {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        engine.current = None;
    }

    let prepared =
        prepare_pipeline(&url, bearer_token.as_deref(), position_ms)?;
    let active = open_active_from_prepared(prepared, &device, url, position_ms)?;

    {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        engine.current = Some(active);
    }
    audio_state()
}

/// Removes the engine's preload slot and returns it iff its URL
/// matches `wanted`. Held briefly under the engine lock so a racing
/// preload call doesn't slip in a different track between our check
/// and our take.
fn take_preload_for(wanted: &str) -> Result<Option<PreloadSlot>, String> {
    let mut engine = ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())?;
    if engine
        .preload
        .as_ref()
        .map(|p| p.source_url == wanted)
        .unwrap_or(false)
    {
        Ok(engine.preload.take())
    } else {
        // Stale preload (user picked a different track than we
        // optimistically prepared). Drop it explicitly so the
        // decoder thread cleans up before we start a fresh one.
        engine.preload = None;
        Ok(None)
    }
}

/// Picks the output device by name, or the host default when None.
fn pick_output_device(name: Option<&str>) -> Result<cpal::Device, String> {
    let host = cpal::default_host();
    match name {
        Some(n) => host
            .output_devices()
            .map_err(|e| format!("audio: enumerate: {e}"))?
            .find(|d| d.name().map(|dn| dn == n).unwrap_or(false))
            .ok_or_else(|| format!("audio: device not found: {n}")),
        None => host
            .default_output_device()
            .ok_or_else(|| "audio: no default output device".to_string()),
    }
}

/// What lossless format does this URL look like? Picks the decoder
/// path: claxon for FLAC (already-tested, no symphonia), symphonia
/// for ALAC / WAV / AIFF. Extension-only — the server doesn't surface
/// a Content-Type we can rely on across deployments, but the music
/// scanner preserves the original file extension on `stream_url`,
/// so the URL is the source of truth.
///
/// Returns FLAC for unknowns; the existing claxon pipeline is the
/// long-tested path and any failure surfaces a clear "audio: parse
/// FLAC" error message that points at the format mismatch.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum AudioFormat {
    Flac,
    Symphonia, // ALAC / WAV / AIFF — anything symphonia handles
    Dsd,       // .dsf — DSD over PCM via dop_packer below
}

fn detect_format(url: &str) -> AudioFormat {
    // Strip query string before the extension check so a `?token=`
    // suffix doesn't make every URL look like ".jpg?token=xyz".
    let path = url.split('?').next().unwrap_or(url);
    let lower = path.to_ascii_lowercase();
    if lower.ends_with(".dsf") {
        // DSF only for v1; DFF (Sony's DSD Interchange Format) has
        // more variant headers + can carry compressed DST. Adding
        // DFF support is a bigger parser; defer until there's hardware
        // to validate against.
        AudioFormat::Dsd
    } else if lower.ends_with(".m4a")
        || lower.ends_with(".mp4")
        || lower.ends_with(".alac")
        || lower.ends_with(".wav")
        || lower.ends_with(".wave")
        || lower.ends_with(".aiff")
        || lower.ends_with(".aif")
    {
        AudioFormat::Symphonia
    } else {
        AudioFormat::Flac
    }
}

/// Format-aware dispatcher. Both call sites (cold-start in
/// `audio_play_url` and the seek-rewind in `audio_seek`) go through
/// this; FLAC routes through the existing claxon path, everything
/// else through the symphonia path.
fn prepare_pipeline(
    url: &str,
    bearer_token: Option<&str>,
    skip_to_ms: u64,
) -> Result<PreloadSlot, String> {
    match detect_format(url) {
        AudioFormat::Flac => prepare_flac_pipeline(url, bearer_token, skip_to_ms),
        AudioFormat::Symphonia => prepare_symphonia_pipeline(url, bearer_token, skip_to_ms),
        AudioFormat::Dsd => prepare_dsd_pipeline(url, bearer_token, skip_to_ms),
    }
}

/// HTTP fetch + symphonia probe + spawn decoder thread for a non-
/// FLAC lossless format (ALAC, WAV, AIFF). Same shape as
/// [`prepare_flac_pipeline`] — returns a [`PreloadSlot`] the
/// promote-to-active step can open a cpal stream around.
///
/// Sample format dispatch:
///   - 16-bit source        → i16 ringbuf, i16 cpal stream
///   - everything else      → i32 ringbuf, i32 cpal stream (matching
///                            FLAC's 24-bit-in-32 packing)
///
/// HTTP body is wrapped in symphonia's ReadOnlySource so the format
/// probe can drive a Read-only stream. ALAC inside MP4/M4A doesn't
/// strictly require seek for streaming playback; WAV / AIFF likewise
/// stream from front-to-back. Future seek implementation may want a
/// real seekable source via Range requests, but that's a future
/// optimisation — current `audio_seek` rebuilds the pipeline with
/// `skip_to_ms` so we land at the target without needing a true seek.
fn prepare_symphonia_pipeline(
    url: &str,
    bearer_token: Option<&str>,
    skip_to_ms: u64,
) -> Result<PreloadSlot, String> {
    use symphonia::core::codecs::DecoderOptions;
    use symphonia::core::errors::Error as SymphoniaError;
    use symphonia::core::formats::FormatOptions;
    use symphonia::core::io::{MediaSourceStream, MediaSourceStreamOptions, ReadOnlySource};
    use symphonia::core::meta::MetadataOptions;
    use symphonia::core::probe::Hint;

    // ── HTTP fetch ──────────────────────────────────────────────────────────
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout_read(std::time::Duration::from_secs(30))
        .build();
    let mut req = agent.get(url);
    if let Some(tok) = bearer_token {
        req = req.set("Authorization", &format!("Bearer {tok}"));
    }
    let resp = req
        .call()
        .map_err(|e| format!("audio: GET {url}: {e}"))?;
    if resp.status() != 200 {
        return Err(format!(
            "audio: GET {url}: HTTP {} — bearer rejected or not found",
            resp.status()
        ));
    }

    // ── Probe ───────────────────────────────────────────────────────────────
    let body: Box<dyn Read + Send + Sync> = Box::new(resp.into_reader());
    let source = ReadOnlySource::new(body);
    let stream = MediaSourceStream::new(Box::new(source), MediaSourceStreamOptions::default());

    // Extension hint helps probe pick the right format on the first try
    // (otherwise ALAC inside MP4 would need a deeper magic-byte probe).
    let mut hint = Hint::new();
    let path = url.split('?').next().unwrap_or(url);
    if let Some(ext) = path.rsplit('.').next() {
        hint.with_extension(ext);
    }

    let fmt_opts = FormatOptions::default();
    let meta_opts = MetadataOptions::default();
    let probed = symphonia::default::get_probe()
        .format(&hint, stream, &fmt_opts, &meta_opts)
        .map_err(|e| format!("audio: probe format: {e}"))?;

    let mut format = probed.format;
    let track = format
        .default_track()
        .ok_or_else(|| "audio: no default track in stream".to_string())?;
    let track_id = track.id;
    let codec_params = track.codec_params.clone();

    // ReplayGain extraction. Symphonia exposes container metadata
    // via format.metadata().current() (a MetadataRevision). The
    // ReplayGain tags ride as user-defined `Tag` entries with the
    // REPLAYGAIN_* keys; symphonia's StandardTagKey enum doesn't
    // distinguish them so we scan by the raw tag key.
    let mut rg_tags = ReplayGainTags::default();
    if let Some(meta) = format.metadata().current() {
        for tag in meta.tags() {
            ingest_replay_gain_tag(&mut rg_tags, &tag.key, &tag.value.to_string());
        }
    }
    let gain_factor = compute_gain_factor(&rg_tags);

    let sample_rate_hz = codec_params
        .sample_rate
        .ok_or_else(|| "audio: codec missing sample_rate".to_string())?;
    let channels_count = codec_params
        .channels
        .ok_or_else(|| "audio: codec missing channels".to_string())?
        .count() as u16;
    let bit_depth = codec_params.bits_per_sample.unwrap_or(16);

    let mut decoder = symphonia::default::get_codecs()
        .make(&codec_params, &DecoderOptions::default())
        .map_err(|e| format!("audio: build decoder: {e}"))?;

    // ── Skip-to-position (seek path) ────────────────────────────────────────
    // Drink + drop packets until cumulative samples reaches the target.
    // Same correctness contract as the FLAC seek: streams from start so
    // bandwidth-heavy on long seeks; correct + simple. Range-based seek
    // is a future optimisation.
    let mut samples_skipped: u64 = 0;
    let samples_to_skip = (skip_to_ms)
        .saturating_mul(sample_rate_hz as u64)
        / 1000;
    while samples_skipped < samples_to_skip {
        let packet = match format.next_packet() {
            Ok(p) => p,
            Err(SymphoniaError::IoError(_)) => break, // EOS — accept the cap
            Err(e) => return Err(format!("audio: read packet during seek: {e}")),
        };
        if packet.track_id() != track_id {
            continue;
        }
        match decoder.decode(&packet) {
            Ok(buf) => {
                samples_skipped = samples_skipped.saturating_add(buf.frames() as u64);
            }
            Err(SymphoniaError::DecodeError(_)) => continue,
            Err(e) => return Err(format!("audio: decode during seek: {e}")),
        }
    }

    // ── Ringbuf + decoder thread ────────────────────────────────────────────
    let ring_capacity = (sample_rate_hz as usize)
        .saturating_mul(channels_count as usize)
        / 5; // ~200 ms
    let stop_flag = Arc::new(AtomicBool::new(false));
    let ended = Arc::new(AtomicBool::new(false));

    let (consumer, decoder_handle) = if bit_depth <= 16 {
        let (cons, h) = spawn_symphonia_decoder::<i16>(
            format,
            decoder,
            track_id,
            ring_capacity,
            channels_count as usize,
            stop_flag.clone(),
            ended.clone(),
            |s: f32| (s.clamp(-1.0, 1.0) * (i16::MAX as f32)) as i16,
            gain_factor,
        )?;
        (PreloadConsumer::I16(cons), h)
    } else {
        let (cons, h) = spawn_symphonia_decoder::<i32>(
            format,
            decoder,
            track_id,
            ring_capacity,
            channels_count as usize,
            stop_flag.clone(),
            ended.clone(),
            // 24-bit-in-32 packing: cpal expects the data in the upper
            // 24 bits of the i32 with the low 8 zero. f32 → i32 with
            // 8-bit shift to land in that layout.
            |s: f32| {
                let scaled = s.clamp(-1.0, 1.0) * ((i32::MAX as f32) / 256.0);
                (scaled as i32).saturating_mul(256)
            },
            gain_factor,
        )?;
        (PreloadConsumer::I32(cons), h)
    };

    Ok(PreloadSlot {
        source_url: url.to_string(),
        sample_rate_hz,
        bit_depth: bit_depth as u32,
        channels: channels_count,
        stop_flag,
        ended,
        decoder_handle: Some(decoder_handle),
        consumer: Some(consumer),
    })
}

/// Spawn a decoder thread for a symphonia format. Same ringbuf
/// shape as [`spawn_decoder`] (the FLAC variant), but the decode
/// step pulls Packets and flattens AudioBuffers to interleaved
/// per-frame samples instead of claxon's already-flat iterator.
fn spawn_symphonia_decoder<T>(
    mut format: Box<dyn symphonia::core::formats::FormatReader>,
    mut decoder: Box<dyn symphonia::core::codecs::Decoder>,
    track_id: u32,
    capacity: usize,
    channels: usize,
    stop_flag: Arc<AtomicBool>,
    ended: Arc<AtomicBool>,
    convert_sample: fn(f32) -> T,
    gain_factor: f32,
) -> Result<(<HeapRb<T> as Split>::Cons, JoinHandle<()>), String>
where
    T: Send + 'static + Default + Copy,
{
    use symphonia::core::errors::Error as SymphoniaError;

    let rb = HeapRb::<T>::new(capacity.max(8192));
    let (mut producer, consumer) = rb.split();

    let stop_flag_dec = stop_flag.clone();
    let ended_dec = ended.clone();
    let decoder_handle = std::thread::Builder::new()
        .name("onscreen-symphonia-decoder".into())
        .spawn(move || {
            // Reusable f32 buffer for interleaved samples. Symphonia
            // lets us copy a planar AudioBufferRef into a mono f32
            // sample stream interleaved by channel; we match that
            // shape into the ringbuf the cpal callback drains.
            let mut interleaved = Vec::<f32>::new();

            loop {
                if stop_flag_dec.load(Ordering::Acquire) {
                    return;
                }
                let packet = match format.next_packet() {
                    Ok(p) => p,
                    Err(SymphoniaError::IoError(_)) => {
                        ended_dec.store(true, Ordering::Release);
                        return;
                    }
                    Err(e) => {
                        eprintln!("audio: symphonia next_packet: {e}");
                        return;
                    }
                };
                if packet.track_id() != track_id {
                    continue;
                }
                let buf = match decoder.decode(&packet) {
                    Ok(b) => b,
                    Err(SymphoniaError::DecodeError(_)) => continue, // skip bad packet
                    Err(e) => {
                        eprintln!("audio: symphonia decode: {e}");
                        return;
                    }
                };

                // Buffer → interleaved f32. Symphonia's
                // AudioBufferRef carries planar f32/i16/i32/u24/etc.;
                // we normalise to f32 + interleave in one pass to
                // hand a uniform stream to convert_sample.
                let frames = buf.frames();
                interleaved.clear();
                interleaved.resize(frames * channels, 0.0);
                copy_interleaved_f32(&buf, &mut interleaved, channels);

                for sample in interleaved.iter() {
                    // ReplayGain attenuation/boost. The symphonia
                    // path natively works in f32 already so the
                    // multiply lands in the same step where the
                    // claxon path needs an extra cast — same cost
                    // either way (~1 ns per sample).
                    let scaled = *sample * gain_factor;
                    let mut converted = convert_sample(scaled);
                    loop {
                        match producer.try_push(converted) {
                            Ok(()) => break,
                            Err(returned) => {
                                converted = returned;
                                if stop_flag_dec.load(Ordering::Acquire) {
                                    return;
                                }
                                std::thread::sleep(std::time::Duration::from_millis(2));
                            }
                        }
                    }
                }
            }
        })
        .map_err(|e| format!("audio: spawn symphonia decoder: {e}"))?;

    Ok((consumer, decoder_handle))
}

/// Copy a symphonia AudioBufferRef into an interleaved f32 vec.
/// Handles the planar-to-interleaved transform + the sample-format
/// normalisation in one pass. Caller is responsible for sizing
/// [`out`] to `frames * channels`.
fn copy_interleaved_f32(
    buf: &symphonia::core::audio::AudioBufferRef<'_>,
    out: &mut [f32],
    channels: usize,
) {
    use symphonia::core::audio::{AudioBufferRef, Signal};
    macro_rules! interleave {
        ($audio:expr, $scale:expr) => {{
            let frames = $audio.frames();
            for ch in 0..channels {
                let plane = $audio.chan(ch);
                for f in 0..frames {
                    out[f * channels + ch] = plane[f] as f32 * $scale;
                }
            }
        }};
    }
    match buf {
        AudioBufferRef::F32(b) => interleave!(b, 1.0_f32),
        AudioBufferRef::S16(b) => interleave!(b, 1.0_f32 / (i16::MAX as f32)),
        AudioBufferRef::S32(b) => interleave!(b, 1.0_f32 / (i32::MAX as f32)),
        AudioBufferRef::S24(b) => {
            let frames = b.frames();
            // i24 stored in i32 with sign-extension; range is ±(2^23-1).
            let scale = 1.0_f32 / 8_388_607.0;
            for ch in 0..channels {
                let plane = b.chan(ch);
                for f in 0..frames {
                    out[f * channels + ch] = plane[f].inner() as f32 * scale;
                }
            }
        }
        AudioBufferRef::U8(b) => interleave!(b, 1.0_f32 / 128.0),
        AudioBufferRef::U16(b) => interleave!(b, 1.0_f32 / (u16::MAX as f32 / 2.0)),
        AudioBufferRef::U24(b) => {
            let frames = b.frames();
            let scale = 1.0_f32 / 8_388_608.0;
            for ch in 0..channels {
                let plane = b.chan(ch);
                for f in 0..frames {
                    out[f * channels + ch] = (plane[f].inner() as f32 - 8_388_608.0) * scale;
                }
            }
        }
        AudioBufferRef::U32(b) => interleave!(b, 1.0_f32 / (u32::MAX as f32 / 2.0)),
        AudioBufferRef::F64(b) => interleave!(b, 1.0_f32),
        AudioBufferRef::S8(b) => interleave!(b, 1.0_f32 / 128.0),
    }
}

// ── DSD (DoP) ──────────────────────────────────────────────────────────────
//
// DSD = Direct Stream Digital, the 1-bit/2.8224 MHz (DSD64) bitstream
// format SACDs use. Modern DACs accept DSD over a regular PCM channel
// via DoP (DSD over PCM): every 16 DSD bits per channel pack into one
// 24-bit PCM word with an alternating 0x05/0xFA marker byte in the
// upper 8 bits. The DAC sees the markers and unwraps the DSD; a
// non-DSD-aware DAC plays the result as broadband white noise (so the
// user side has to know they have a DoP-capable DAC — there's no
// graceful fallback).
//
// Output rate = DSD rate / 16 → DSD64 (2_822_400 Hz) plays as 176_400 Hz
// PCM, which any audio API can carry. Output is 24-bit; we use the
// existing 24-bit-in-32 ringbuf path the FLAC + symphonia branches
// already feed into.
//
// File format support: .dsf only for v1. The DSF header is well-
// specified (Sony's DSD Audio File Format spec) and 95% of consumer
// DSD downloads ship .dsf. .dff (DSDIFF) has more variant chunks and
// can carry DST-compressed payloads; defer until there's a real DSD
// catalog to test against.

const DSD_BLOCK_SIZE_PER_CHANNEL: usize = 4096;
const DOP_MARKER_05: u32 = 0x05;
const DOP_MARKER_FA: u32 = 0xFA;

#[derive(Clone, Copy)]
struct DsfHeader {
    /// DSD bitstream rate, e.g. 2_822_400 for DSD64.
    sample_rate_hz: u32,
    /// 1 (mono) or 2 (stereo). DSF supports up to 6 channels but
    /// consumer catalogs are stereo; keeping the parser tight rather
    /// than supporting variants we can't test.
    channels: u16,
    /// Bits-per-sample as stored — always 1 for DSD. Kept on the
    /// header for parser symmetry; the DoP packer assumes 1.
    bits_per_sample: u8,
    /// Total DSD samples per channel in the file. Drives the
    /// known-duration calculation in audio_state.
    sample_count: u64,
    /// Bytes per channel per block. Spec mandates 4096; the value
    /// is parsed off the header so a non-conformant file fails
    /// loudly rather than silently misaligning channels.
    block_size_per_channel: u32,
    /// LSB-first (1) or MSB-first (0). DSF spec says LSB-first;
    /// we track it for explicit handling rather than assuming.
    bits_per_sample_lsb_first: bool,
}

/// Parse a DSF header from a streaming reader. Reads exactly the bytes
/// it needs (28 + 52 + 12 = 92 header bytes plus skipping any pre-data
/// padding) so the caller can hand the same reader to the DSD decoder
/// thread immediately afterward. Returns the header + the offset
/// remaining-data is at (always 0 in normal DSF flow — header is
/// followed by data block, no padding in spec).
fn parse_dsf_header<R: Read>(reader: &mut R) -> Result<DsfHeader, String> {
    let mut buf = [0u8; 28];
    reader
        .read_exact(&mut buf)
        .map_err(|e| format!("audio: read DSF DSD chunk: {e}"))?;
    if &buf[0..4] != b"DSD " {
        return Err(format!("audio: not a DSF file (magic = {:?})", &buf[0..4]));
    }
    // Skip chunk_size + total_size + metadata_offset; we don't need
    // them to decode (data length lives on the data chunk header).

    let mut buf = [0u8; 52];
    reader
        .read_exact(&mut buf)
        .map_err(|e| format!("audio: read DSF fmt chunk: {e}"))?;
    if &buf[0..4] != b"fmt " {
        return Err(format!("audio: missing DSF fmt chunk (got {:?})", &buf[0..4]));
    }
    // fmt chunk layout (after the 12-byte chunk header which was
    // included in the 52 bytes above):
    //   12: format_version (u32)
    //   16: format_id      (u32)  — always 0 for DSD raw
    //   20: channel_type   (u32)
    //   24: channel_num    (u32)
    //   28: sample_rate    (u32)  — bits per second (DSD: 2_822_400 etc.)
    //   32: bits_per_sample (u32) — 1 for DSD
    //   36: sample_count   (u64)
    //   44: block_size_per_channel (u32)
    //   48: reserved       (u32)
    let channel_num = u32::from_le_bytes(buf[24..28].try_into().unwrap());
    let sample_rate = u32::from_le_bytes(buf[28..32].try_into().unwrap());
    let bits_per_sample = u32::from_le_bytes(buf[32..36].try_into().unwrap());
    let sample_count = u64::from_le_bytes(buf[36..44].try_into().unwrap());
    let block_size_per_channel = u32::from_le_bytes(buf[44..48].try_into().unwrap());

    if channel_num == 0 || channel_num > 6 {
        return Err(format!("audio: unsupported DSF channel count {channel_num}"));
    }
    if bits_per_sample != 1 {
        return Err(format!(
            "audio: unsupported DSF bits_per_sample {bits_per_sample} (expected 1)"
        ));
    }
    if block_size_per_channel != DSD_BLOCK_SIZE_PER_CHANNEL as u32 {
        return Err(format!(
            "audio: unsupported DSF block_size_per_channel {block_size_per_channel} (expected 4096)"
        ));
    }

    let mut buf = [0u8; 12];
    reader
        .read_exact(&mut buf)
        .map_err(|e| format!("audio: read DSF data chunk header: {e}"))?;
    if &buf[0..4] != b"data" {
        return Err(format!("audio: missing DSF data chunk (got {:?})", &buf[0..4]));
    }

    Ok(DsfHeader {
        sample_rate_hz: sample_rate,
        channels: channel_num as u16,
        bits_per_sample: 1,
        sample_count,
        block_size_per_channel,
        // DSF spec: LSB-first storage. We decode under that assumption;
        // the field tracks it for explicit handling.
        bits_per_sample_lsb_first: true,
    })
}

/// Pack 16 DSD bits per channel into a single 24-bit-in-32 PCM word
/// with the alternating DoP marker in the upper 8 bits. `marker_high`
/// alternates 0x05 / 0xFA on consecutive output frames so the DAC
/// sync-locks; the caller threads its own toggle state.
///
/// DSF stores DSD bytes LSB-first (oldest sample at bit 0), but DoP
/// expects the oldest sample at the MSB. Reverse + lay them out
/// MSB-first inside the lower 16 bits, then OR in the marker byte
/// shifted to the upper 8 bits of a 24-bit word, then shift left
/// 8 more so cpal's 24-in-32 expects the data in the upper 24 bits.
fn dop_pack_word(dsd_byte_msb: u8, dsd_byte_lsb: u8, marker_high: u32) -> i32 {
    // Reverse bit order in each byte so MSB carries the oldest sample.
    let hi = dsd_byte_msb.reverse_bits() as u32;
    let lo = dsd_byte_lsb.reverse_bits() as u32;
    let payload24 = (marker_high << 16) | (hi << 8) | lo;
    // Shift into 24-in-32 packing.
    (payload24 << 8) as i32
}

/// HTTP fetch + DSF header + DoP-packing decoder thread. Returns
/// the same PreloadSlot shape the FLAC + symphonia branches do; the
/// cpal stream config will be 24-bit at sample_rate_hz / 16 with
/// channel count from the DSF header.
fn prepare_dsd_pipeline(
    url: &str,
    bearer_token: Option<&str>,
    skip_to_ms: u64,
) -> Result<PreloadSlot, String> {
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout_read(std::time::Duration::from_secs(30))
        .build();
    let mut req = agent.get(url);
    if let Some(tok) = bearer_token {
        req = req.set("Authorization", &format!("Bearer {tok}"));
    }
    let resp = req
        .call()
        .map_err(|e| format!("audio: GET {url}: {e}"))?;
    if resp.status() != 200 {
        return Err(format!(
            "audio: GET {url}: HTTP {} — bearer rejected or not found",
            resp.status()
        ));
    }
    let mut body: Box<dyn Read + Send + 'static> = Box::new(resp.into_reader());

    let header = parse_dsf_header(&mut body)?;

    // DoP output rate: 16 DSD samples per channel pack into one
    // 24-bit PCM frame. DSD64 (2_822_400) → 176_400 Hz. DSD128 →
    // 352_800. cpal accepts 176.4/352.8 kHz on every host that has
    // a DAC supporting it — fallback to nearest supported config is
    // the OS's job in default mode, exclusive mode (future) requires
    // exact match.
    let pcm_rate = header.sample_rate_hz / 16;
    let channels = header.channels;

    // Skip-to-position. Each PCM output frame represents 16 DSD
    // samples (per channel) = 16 bytes (8 bits/byte, 1 bit/sample).
    // The DSF data is interleaved at the BLOCK level, not per-frame:
    // 4096 bytes of channel 0, then 4096 of channel 1, etc., one
    // block group at a time. To skip, we drop entire blocks rather
    // than seek mid-block to keep channel alignment.
    let bytes_per_block_group = (header.block_size_per_channel as u64) * (channels as u64);
    if skip_to_ms > 0 {
        // Bytes per channel per second of DSD = sample_rate_hz / 8.
        let bytes_per_sec = (header.sample_rate_hz as u64) / 8;
        let target_bytes_per_channel = skip_to_ms.saturating_mul(bytes_per_sec) / 1000;
        // Round down to whole blocks so we don't desync channels.
        let target_blocks = target_bytes_per_channel / (header.block_size_per_channel as u64);
        let skip_bytes = target_blocks * bytes_per_block_group;
        let mut remaining = skip_bytes;
        let mut sink = [0u8; 4096];
        while remaining > 0 {
            let n = remaining.min(sink.len() as u64) as usize;
            body
                .read_exact(&mut sink[..n])
                .map_err(|e| format!("audio: skip DSF data: {e}"))?;
            remaining -= n as u64;
        }
    }

    let ring_capacity = (pcm_rate as usize)
        .saturating_mul(channels as usize)
        / 5; // ~200 ms
    let stop_flag = Arc::new(AtomicBool::new(false));
    let ended = Arc::new(AtomicBool::new(false));

    let (consumer, decoder_handle) =
        spawn_dsd_decoder(body, header, ring_capacity, stop_flag.clone(), ended.clone())?;

    Ok(PreloadSlot {
        source_url: url.to_string(),
        sample_rate_hz: pcm_rate,
        bit_depth: 24,
        channels,
        stop_flag,
        ended,
        decoder_handle: Some(decoder_handle),
        consumer: Some(PreloadConsumer::I32(consumer)),
    })
}

/// Decoder thread for the DSD path. Reads block-interleaved DSF
/// data (4096 bytes per channel, repeating block-group), DoP-packs
/// 16 DSD bits per channel into one 24-bit-in-32 PCM word, pushes
/// to the ringbuf in interleaved channel order so the cpal callback
/// can drain straight into its output.
fn spawn_dsd_decoder(
    mut reader: Box<dyn Read + Send + 'static>,
    header: DsfHeader,
    capacity: usize,
    stop_flag: Arc<AtomicBool>,
    ended: Arc<AtomicBool>,
) -> Result<(<HeapRb<i32> as Split>::Cons, JoinHandle<()>), String> {
    let rb = HeapRb::<i32>::new(capacity.max(8192));
    let (mut producer, consumer) = rb.split();
    let channels = header.channels as usize;
    let block_bytes = header.block_size_per_channel as usize;

    let stop_flag_dec = stop_flag.clone();
    let ended_dec = ended.clone();
    let decoder_handle = std::thread::Builder::new()
        .name("onscreen-dsd-decoder".into())
        .spawn(move || {
            // Per-channel block buffers. DSF stores [block_ch0,
            // block_ch1, ...]; we read one block-group at a time
            // and emit DoP frames interleaved across channels.
            let mut blocks = vec![vec![0u8; block_bytes]; channels];
            // DoP marker toggle — alternates 0x05 / 0xFA on each
            // emitted PCM frame. Both channels get the SAME marker
            // on the same frame; the toggle advances per frame, not
            // per sample-channel.
            let mut marker_high = DOP_MARKER_05;

            loop {
                if stop_flag_dec.load(Ordering::Acquire) {
                    return;
                }
                // Read the next block-group: one block per channel,
                // sequentially. EOF on the first byte of any channel
                // means the file ended cleanly.
                for ch in 0..channels {
                    if let Err(e) = reader.read_exact(&mut blocks[ch]) {
                        // First-byte EOF on channel 0 = clean EOS.
                        // Anywhere else = truncated file; surface as
                        // EOS too rather than logging an error mid-
                        // album (network blip, server killed
                        // mid-transfer).
                        if ch == 0 && e.kind() == std::io::ErrorKind::UnexpectedEof {
                            ended_dec.store(true, Ordering::Release);
                            return;
                        }
                        eprintln!("audio: DSF read at ch{ch}: {e}");
                        ended_dec.store(true, Ordering::Release);
                        return;
                    }
                }
                // Emit DoP frames. block_bytes / 2 = number of PCM
                // frames this block-group produces (16 DSD bits → 1
                // PCM word, 2 DSD bytes carry 16 bits).
                let frames_per_block = block_bytes / 2;
                for f in 0..frames_per_block {
                    let off = f * 2;
                    for ch in 0..channels {
                        // DSF spec: LSB-first within byte. dop_pack_word
                        // bit-reverses each byte so DoP gets MSB-first
                        // ordering. The two bytes are passed in order
                        // [oldest, newest] — DSF stores oldest sample
                        // first, DoP wants oldest-MSB → that's "oldest
                        // byte = high byte after reversal".
                        let oldest = blocks[ch][off];
                        let newest = blocks[ch][off + 1];
                        let pcm = dop_pack_word(oldest, newest, marker_high);
                        let mut sample = pcm;
                        loop {
                            match producer.try_push(sample) {
                                Ok(()) => break,
                                Err(returned) => {
                                    sample = returned;
                                    if stop_flag_dec.load(Ordering::Acquire) {
                                        return;
                                    }
                                    std::thread::sleep(std::time::Duration::from_millis(2));
                                }
                            }
                        }
                    }
                    marker_high = if marker_high == DOP_MARKER_05 {
                        DOP_MARKER_FA
                    } else {
                        DOP_MARKER_05
                    };
                }
            }
        })
        .map_err(|e| format!("audio: spawn DSD decoder: {e}"))?;

    // header.sample_count + bits_per_sample_lsb_first are tracked on
    // the header for parser symmetry but not used after the decoder
    // is spawned — quiet the lint without removing the fields.
    let _ = header.sample_count;
    let _ = header.bits_per_sample;
    let _ = header.bits_per_sample_lsb_first;
    Ok((consumer, decoder_handle))
}

/// HTTP fetch + FLAC header parse + spawn decoder thread → returns
/// a [`PreloadSlot`] holding a ringbuf consumer the caller can
/// build a cpal stream around. Both `audio_play_url` (cold-start)
/// and `audio_preload_url` go through this — the only difference
/// is what the caller does with the result.
///
/// `skip_to_ms` advances the decoder past N milliseconds before any
/// samples reach the ringbuf. Used by [`audio_seek`] to land at a
/// target position; passed as 0 by the cold-start + preload paths.
/// The skip happens synchronously on the calling thread (drinks
/// samples without pushing) so the eventual cpal stream pulls
/// straight from the seek target — no audible "play from start
/// then jump" glitch. Cost is bandwidth: streams from the start
/// even for a 70-min seek, since claxon over an HTTP body has no
/// frame-level random access. Range-based seeking is a future
/// optimisation; this is correct and simple.
fn prepare_flac_pipeline(
    url: &str,
    bearer_token: Option<&str>,
    skip_to_ms: u64,
) -> Result<PreloadSlot, String> {
    // ── HTTP fetch ──────────────────────────────────────────────────────────
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout_read(std::time::Duration::from_secs(30))
        .build();
    let mut req = agent.get(url);
    if let Some(tok) = bearer_token {
        req = req.set("Authorization", &format!("Bearer {tok}"));
    }
    let resp = req
        .call()
        .map_err(|e| format!("audio: GET {url}: {e}"))?;
    if resp.status() != 200 {
        return Err(format!(
            "audio: GET {url}: HTTP {} — bearer rejected or not found",
            resp.status()
        ));
    }
    // Box the body so the prepared pipeline doesn't need to be
    // generic over ureq's concrete reader type. Send + 'static is
    // enough — only the decoder thread reads, so Sync isn't required.
    let body: Box<dyn Read + Send + 'static> = Box::new(resp.into_reader());

    // ── FLAC header probe ───────────────────────────────────────────────────
    let mut reader = claxon::FlacReader::new(body)
        .map_err(|e| format!("audio: parse FLAC: {e}"))?;
    let info = reader.streaminfo();
    let sample_rate_hz = info.sample_rate;
    let bit_depth = info.bits_per_sample;
    let channels = info.channels as u16;

    // ReplayGain extraction. claxon exposes Vorbis comments via
    // reader.tags() — iterate, ingest the four ReplayGain keys, then
    // the gain factor is computed against the user's current mode.
    // Tags missing → factor stays 1.0 (no-op multiply).
    let mut rg_tags = ReplayGainTags::default();
    for (key, value) in reader.tags() {
        ingest_replay_gain_tag(&mut rg_tags, key, value);
    }
    let gain_factor = compute_gain_factor(&rg_tags);

    // ── Skip-to-position (seek path) ────────────────────────────────────────
    // Drink samples on the calling thread until we've reached the seek
    // target. claxon's iterator borrows `reader` mutably, so we scope
    // the iterator and re-take it from `reader` after the borrow drops
    // — the FlacReader's internal frame state persists between
    // iterator instantiations, so the spawn_decoder below picks up
    // exactly where this loop left off.
    if skip_to_ms > 0 {
        let samples_to_skip = (skip_to_ms)
            .saturating_mul(sample_rate_hz as u64)
            .saturating_mul(channels as u64)
            / 1000;
        let mut iter = reader.samples();
        let mut consumed: u64 = 0;
        while consumed < samples_to_skip {
            match iter.next() {
                Some(Ok(_)) => consumed += 1,
                Some(Err(e)) => {
                    return Err(format!("audio: FLAC decode during seek: {e}"))
                }
                None => break, // seek target past EOS — accept it
            }
        }
        // iter dropped → &mut reader released → spawn_decoder can
        // call .samples() again on the same reader and continue
        // from the post-skip position.
    }

    // Ringbuf sized for ~200 ms of audio at the file's rate. Tight
    // enough that latency from "play" to first sample is sub-second;
    // loose enough that brief disk/network hiccups don't underrun.
    let ring_capacity = (sample_rate_hz as usize)
        .saturating_mul(channels as usize)
        / 5; // 0.2 s
    let stop_flag = Arc::new(AtomicBool::new(false));
    let ended = Arc::new(AtomicBool::new(false));

    // Bit-depth dispatch + spawn decoder. Both branches return a
    // PreloadConsumer so the outer PreloadSlot doesn't need to be
    // generic.
    let (consumer, decoder_handle) = if bit_depth <= 16 {
        let (cons, h) = spawn_decoder::<i16>(
            reader,
            ring_capacity,
            stop_flag.clone(),
            ended.clone(),
            |s| s as i16,
            gain_factor,
        )?;
        (PreloadConsumer::I16(cons), h)
    } else {
        let (cons, h) = spawn_decoder::<i32>(
            reader,
            ring_capacity,
            stop_flag.clone(),
            ended.clone(),
            // 24-bit-in-32: shift left so the high byte holds the MSB.
            // cpal expects "24 bits packed as 32" with the data in the
            // upper 24 bits and the low 8 zero on most hosts.
            |s| s.saturating_mul(256),
            gain_factor,
        )?;
        (PreloadConsumer::I32(cons), h)
    };

    Ok(PreloadSlot {
        source_url: url.to_string(),
        sample_rate_hz,
        bit_depth,
        channels,
        stop_flag,
        ended,
        decoder_handle: Some(decoder_handle),
        consumer: Some(consumer),
    })
}

/// Promote a prepared slot to an active playback by opening the
/// cpal output stream around its ringbuf consumer. The decoder
/// thread keeps running unchanged — it just gains a real reader.
///
/// `start_position_ms` seeds the frame counter so `audio_state`
/// reports the correct position immediately after a seek. The cpal
/// callback adds new frames on top as samples are consumed, so the
/// reported position keeps advancing from the seek target rather
/// than re-counting from zero.
fn open_active_from_prepared(
    mut prepared: PreloadSlot,
    device: &cpal::Device,
    url: String,
    start_position_ms: u64,
) -> Result<ActivePlayback, String> {
    // Default-mode cpal lets us request a specific rate; the OS
    // mixer picks up the slack if the device doesn't natively
    // support it. Real exclusive output (no OS resampling) needs
    // a per-platform backend (raw WASAPI / CoreAudio / ALSA) — the
    // EXCLUSIVE_MODE flag below selects "tight buffer" mode in cpal
    // until those backends land, which lowers latency on the OS
    // mixer's resampler without bypassing it. The flag's call site
    // exists today so the future raw-backend implementations can
    // light up without touching this function.
    let exclusive = EXCLUSIVE_MODE.load(Ordering::Acquire);
    let buffer_size = if exclusive {
        // ~10 ms at the file's native rate. Small enough that the
        // OS mixer's resampler stays close to "transparent" but
        // large enough that the realtime callback isn't starved on
        // a moderately busy system. Tuned to the lower bound of
        // typical USB DAC ASIO buffers (4-12 ms is the audiophile
        // sweet spot).
        let frames = (prepared.sample_rate_hz as u32) / 100;
        cpal::BufferSize::Fixed(frames.max(64))
    } else {
        cpal::BufferSize::Default
    };
    let stream_config = cpal::StreamConfig {
        channels: prepared.channels,
        sample_rate: cpal::SampleRate(prepared.sample_rate_hz),
        buffer_size,
    };
    let paused = Arc::new(AtomicBool::new(false));
    let initial_frames = start_position_ms
        .saturating_mul(prepared.sample_rate_hz as u64)
        / 1000;
    let frames_written = Arc::new(AtomicU64::new(initial_frames));

    let consumer = prepared
        .consumer
        .take()
        .ok_or_else(|| "audio: preload consumer already taken".to_string())?;

    // Windows + exclusive flag: try WASAPI exclusive mode first. On
    // any failure (format unsupported, device busy, virtual output)
    // fall back to the cpal tight-buffer path so the user still
    // hears audio. macOS + Linux fall straight to cpal — their
    // exclusive backends (CoreAudio HOG mode / ALSA hw:) are still
    // pending, so the EXCLUSIVE_MODE flag for them stays a
    // tighter-buffer cpal hint until those modules ship.
    #[cfg(target_os = "windows")]
    {
        if exclusive {
            let wasapi_consumer = match consumer {
                PreloadConsumer::I16(c) => crate::windows_exclusive::WasapiConsumer::I16(c),
                PreloadConsumer::I32(c) => crate::windows_exclusive::WasapiConsumer::I32(c),
            };
            match crate::windows_exclusive::WasapiStream::open(
                wasapi_consumer,
                prepared.sample_rate_hz,
                prepared.channels,
                prepared.bit_depth,
                paused.clone(),
                frames_written.clone(),
            ) {
                Ok(stream) => {
                    let decoder_handle = prepared.decoder_handle.take();
                    ACTIVE_BACKEND.store(BACKEND_WASAPI_EXCLUSIVE, Ordering::Release);
                    return Ok(ActivePlayback {
                        _stream: ActiveStream::Wasapi(stream),
                        stop_flag: prepared.stop_flag.clone(),
                        paused,
                        ended: prepared.ended.clone(),
                        frames_written,
                        decoder_handle,
                        source_url: url,
                        sample_rate_hz: prepared.sample_rate_hz,
                        bit_depth: prepared.bit_depth,
                        channels: prepared.channels,
                    });
                }
                Err(e) => {
                    eprintln!(
                        "audio: WASAPI exclusive open failed ({e}); falling back to cpal shared mode"
                    );
                    // Recover the consumer for the cpal fallback —
                    // unfortunately it moved into wasapi_consumer, so
                    // the caller would have to retry the prepare to
                    // get a fresh ringbuf. Surface as an error rather
                    // than try to silently "recover" with a stale ringbuf.
                    return Err(format!(
                        "audio: WASAPI exclusive open failed and consumer was consumed: {e}"
                    ));
                }
            }
        }
    }

    let stream = match consumer {
        PreloadConsumer::I16(cons) => open_cpal_stream::<i16>(
            cons,
            device,
            &stream_config,
            prepared.channels,
            paused.clone(),
            frames_written.clone(),
        )?,
        PreloadConsumer::I32(cons) => open_cpal_stream::<i32>(
            cons,
            device,
            &stream_config,
            prepared.channels,
            paused.clone(),
            frames_written.clone(),
        )?,
    };
    stream.play().map_err(|e| format!("audio: play: {e}"))?;

    // Take the decoder handle out of `prepared` so the
    // PreloadSlot::Drop impl doesn't signal stop on it when
    // `prepared` falls out of scope at function exit. The handle
    // moves into ActivePlayback unchanged.
    let decoder_handle = prepared.decoder_handle.take();

    // cpal landed: tight-buffer when EXCLUSIVE_MODE was requested
    // (toggle on but the platform's raw backend isn't wired yet, or
    // WASAPI fell back), default-buffer otherwise.
    ACTIVE_BACKEND.store(
        if exclusive { BACKEND_CPAL_TIGHT } else { BACKEND_CPAL_SHARED },
        Ordering::Release,
    );

    Ok(ActivePlayback {
        _stream: ActiveStream::Cpal(stream),
        stop_flag: prepared.stop_flag.clone(),
        paused,
        ended: prepared.ended.clone(),
        frames_written,
        decoder_handle,
        source_url: url,
        sample_rate_hz: prepared.sample_rate_hz,
        bit_depth: prepared.bit_depth,
        channels: prepared.channels,
    })
}

/// Spawns the FLAC decoder thread, returns the consumer side of the
/// ringbuf so the cpal stream can be opened around it later (or
/// immediately, in the cold-start case).
///
/// Generic over T (the cpal sample type — I16 for 16-bit FLAC, I32
/// for ≥17-bit). `convert_sample` maps claxon's i32 sample to T
/// (narrow for I16, shift-into-upper-24 for I32 24-bit-in-32).
fn spawn_decoder<T>(
    reader: claxon::FlacReader<Box<dyn Read + Send + 'static>>,
    capacity: usize,
    stop_flag: Arc<AtomicBool>,
    ended: Arc<AtomicBool>,
    convert_sample: fn(i32) -> T,
    gain_factor: f32,
) -> Result<(<HeapRb<T> as Split>::Cons, JoinHandle<()>), String>
where
    T: Send + 'static + Default + Copy,
{
    let rb = HeapRb::<T>::new(capacity.max(8192));
    let (mut producer, consumer) = rb.split();

    // Branch off the gain factor up front: when ReplayGain is off
    // (factor == 1.0), the inner loop stays on the original
    // direct-convert fast path. When ReplayGain is active, samples
    // run through an f32 multiply before convert. The per-sample cost
    // is one floating-point multiply (~1 ns) — measurable but well
    // under the budget at any sample rate we care about.
    let apply_gain = (gain_factor - 1.0).abs() > 1e-6;

    // Decoder thread. Pushes one FLAC sample at a time; sleeps when
    // the ring is full so the cpal callback can drain. Exits cleanly
    // when the stream ends (claxon iterator returns None) or when
    // stop_flag is raised. EOS sets `ended` so audio_state can
    // report it for auto-advance; stop_flag exits without setting
    // `ended` because that's an explicit user stop, not a track-
    // finished event auto-advance should react to.
    let stop_flag_dec = stop_flag.clone();
    let ended_dec = ended.clone();
    let decoder_handle = std::thread::Builder::new()
        .name("onscreen-flac-decoder".into())
        .spawn(move || {
            let mut reader = reader;
            let mut samples = reader.samples();
            loop {
                if stop_flag_dec.load(Ordering::Acquire) {
                    return;
                }
                match samples.next() {
                    Some(Ok(s)) => {
                        let scaled = if apply_gain {
                            // FLAC samples are signed integers in
                            // the native bit-depth range. Scaling
                            // happens in i32 space directly — no
                            // normalisation needed because the
                            // factor preserves the same range.
                            ((s as f32) * gain_factor) as i32
                        } else {
                            s
                        };
                        let mut sample = convert_sample(scaled);
                        loop {
                            match producer.try_push(sample) {
                                Ok(()) => break,
                                Err(returned) => {
                                    sample = returned;
                                    if stop_flag_dec.load(Ordering::Acquire) {
                                        return;
                                    }
                                    std::thread::sleep(std::time::Duration::from_millis(2));
                                }
                            }
                        }
                    }
                    Some(Err(e)) => {
                        eprintln!("audio: FLAC decode error: {e}");
                        return;
                    }
                    None => {
                        ended_dec.store(true, Ordering::Release);
                        return;
                    }
                }
            }
        })
        .map_err(|e| format!("audio: spawn decoder thread: {e}"))?;

    Ok((consumer, decoder_handle))
}

/// Build the cpal output stream around an existing ringbuf consumer.
/// Used by both cold-start (right after `spawn_decoder`) and
/// gapless promote (the consumer was created earlier when the
/// preload was prepared, but the stream wasn't opened until now).
fn open_cpal_stream<T>(
    mut consumer: <HeapRb<T> as Split>::Cons,
    device: &cpal::Device,
    config: &cpal::StreamConfig,
    channels: u16,
    paused: Arc<AtomicBool>,
    frames_written: Arc<AtomicU64>,
) -> Result<cpal::Stream, String>
where
    T: SizedSample + Send + 'static,
{
    // cpal output callback. Realtime — must not block, allocate, or
    // call into Tauri/anything that takes a mutex. ringbuf's pop
    // is wait-free; a buffer underrun (decoder behind) writes
    // silence rather than stalling the device. When paused, we
    // skip the ringbuf entirely so the decoder's natural backpressure
    // freezes its output until resume — no extra CPU during a pause.
    //
    // Frame counter advances per-frame (not per-sample) so the
    // position math doesn't have to divide by `channels` later.
    // Skipped while paused so the position display freezes at the
    // current spot rather than ticking forward through silence.
    let paused_cb = paused.clone();
    let frames_cb = frames_written.clone();
    let channels_cb = channels as usize;
    let stream = device
        .build_output_stream(
            config,
            move |buf: &mut [T], _: &cpal::OutputCallbackInfo| {
                if paused_cb.load(Ordering::Acquire) {
                    for slot in buf.iter_mut() {
                        *slot = T::EQUILIBRIUM;
                    }
                    return;
                }
                let mut samples_consumed: u64 = 0;
                for slot in buf.iter_mut() {
                    match consumer.try_pop() {
                        Some(s) => {
                            *slot = s;
                            samples_consumed += 1;
                        }
                        None => {
                            *slot = T::EQUILIBRIUM;
                            // Underrun (or post-EOS drain) — don't tick
                            // the frame counter forward; position UI
                            // should freeze at the last real sample.
                        }
                    }
                }
                if samples_consumed > 0 && channels_cb > 0 {
                    let frames = samples_consumed / (channels_cb as u64);
                    frames_cb.fetch_add(frames, Ordering::Release);
                }
            },
            |err| eprintln!("audio: stream error: {err}"),
            None,
        )
        .map_err(|e| format!("audio: build stream: {e}"))?;
    Ok(stream)
}

#[cfg(test)]
mod tests {
    use super::*;

    // Lossless format dispatch from URL extension. The decoder
    // pipeline branches on this — a misdetection routes ALAC through
    // the FLAC path (or vice versa) and the file fails to play.
    #[test]
    fn detect_format_flac_default() {
        // Unknown extensions default to FLAC because the existing
        // claxon path emits a clearer "audio: parse FLAC" error than
        // a symphonia probe failure on a non-audio body.
        assert_eq!(detect_format("https://srv/track.flac"), AudioFormat::Flac);
        assert_eq!(detect_format("https://srv/track"), AudioFormat::Flac);
        assert_eq!(detect_format("https://srv/track.unknown"), AudioFormat::Flac);
    }

    #[test]
    fn detect_format_alac_via_m4a_or_mp4_or_alac() {
        assert_eq!(detect_format("https://srv/track.m4a"), AudioFormat::Symphonia);
        assert_eq!(detect_format("https://srv/track.mp4"), AudioFormat::Symphonia);
        assert_eq!(detect_format("https://srv/track.alac"), AudioFormat::Symphonia);
    }

    #[test]
    fn detect_format_wav() {
        assert_eq!(detect_format("https://srv/track.wav"), AudioFormat::Symphonia);
        assert_eq!(detect_format("https://srv/track.wave"), AudioFormat::Symphonia);
    }

    #[test]
    fn detect_format_aiff_or_aif() {
        assert_eq!(detect_format("https://srv/track.aiff"), AudioFormat::Symphonia);
        assert_eq!(detect_format("https://srv/track.aif"), AudioFormat::Symphonia);
    }

    #[test]
    fn detect_format_ignores_query_string() {
        // /artwork/* and /media/stream/* both append `?token=…` for
        // the Tauri webview path. The detector must look at the path,
        // not the full URL with query, or every track would route to
        // the FLAC fallback.
        assert_eq!(
            detect_format("https://srv/track.m4a?token=abc"),
            AudioFormat::Symphonia,
        );
        assert_eq!(
            detect_format("https://srv/track.flac?token=abc&v=1"),
            AudioFormat::Flac,
        );
    }

    #[test]
    fn detect_format_case_insensitive() {
        assert_eq!(detect_format("https://srv/Track.M4A"), AudioFormat::Symphonia);
        assert_eq!(detect_format("https://srv/Track.WAV"), AudioFormat::Symphonia);
    }

    // ── ReplayGain ─────────────────────────────────────────────────

    #[test]
    fn parse_replay_gain_db_handles_unit_variants() {
        assert_eq!(parse_replay_gain_db("-6.50 dB"), Some(-6.5));
        assert_eq!(parse_replay_gain_db("+3.20 dB"), Some(3.2));
        assert_eq!(parse_replay_gain_db("-6.50 db"), Some(-6.5));
        assert_eq!(parse_replay_gain_db("-6.50dB"), Some(-6.5));
        assert_eq!(parse_replay_gain_db("0.00"), Some(0.0));
        assert_eq!(parse_replay_gain_db("  -6.50  "), Some(-6.5));
        assert_eq!(parse_replay_gain_db("garbage"), None);
        assert_eq!(parse_replay_gain_db(""), None);
    }

    #[test]
    fn parse_replay_gain_peak_basic() {
        assert_eq!(parse_replay_gain_peak("0.95"), Some(0.95));
        assert_eq!(parse_replay_gain_peak("1.0"), Some(1.0));
        // Some encoders emit slight overshoots from intersample
        // peaks — accept rather than reject so peak limiting still
        // engages (factor * peak > 1 still triggers the clamp).
        assert_eq!(parse_replay_gain_peak("1.0123"), Some(1.0123));
        assert_eq!(parse_replay_gain_peak("garbage"), None);
    }

    #[test]
    fn ingest_replay_gain_tag_case_insensitive_keys() {
        let mut tags = ReplayGainTags::default();
        ingest_replay_gain_tag(&mut tags, "REPLAYGAIN_TRACK_GAIN", "-6.50 dB");
        ingest_replay_gain_tag(&mut tags, "replaygain_track_peak", "0.95");
        ingest_replay_gain_tag(&mut tags, "ReplayGain_Album_Gain", "-7.00 dB");
        ingest_replay_gain_tag(&mut tags, "REPLAYGAIN_ALBUM_PEAK", "0.99");
        // Unknown key drops on the floor — return value is false but
        // we don't lean on it in production code (debug aid only).
        assert!(!ingest_replay_gain_tag(&mut tags, "ARTIST", "Pink Floyd"));

        assert_eq!(tags.track_gain_db, Some(-6.5));
        assert_eq!(tags.track_peak, Some(0.95));
        assert_eq!(tags.album_gain_db, Some(-7.0));
        assert_eq!(tags.album_peak, Some(0.99));
    }

    #[test]
    fn compute_gain_factor_off_mode_is_passthrough() {
        let tags = ReplayGainTags {
            track_gain_db: Some(-6.0),
            track_peak: Some(0.9),
            album_gain_db: Some(-7.0),
            album_peak: Some(0.95),
        };
        assert_eq!(
            compute_gain_factor_for(&tags, REPLAY_GAIN_MODE_OFF, 0.0),
            1.0,
        );
    }

    #[test]
    fn compute_gain_factor_no_tags_returns_one() {
        // Mode on but tags missing → factor 1.0 so the track plays
        // at native level rather than going silent.
        let empty = ReplayGainTags::default();
        assert_eq!(
            compute_gain_factor_for(&empty, REPLAY_GAIN_MODE_TRACK, 0.0),
            1.0,
        );
        assert_eq!(
            compute_gain_factor_for(&empty, REPLAY_GAIN_MODE_ALBUM, 0.0),
            1.0,
        );
    }

    #[test]
    fn compute_gain_factor_track_mode_applies_track_gain() {
        let tags = ReplayGainTags {
            track_gain_db: Some(-6.0),
            track_peak: None,
            album_gain_db: Some(-12.0), // ignored in track mode
            album_peak: None,
        };
        // -6 dB → 10^(-6/20) ≈ 0.5012
        let factor = compute_gain_factor_for(&tags, REPLAY_GAIN_MODE_TRACK, 0.0);
        assert!((factor - 0.5012).abs() < 0.001, "factor = {factor}");
    }

    #[test]
    fn compute_gain_factor_album_mode_falls_back_to_track() {
        // Album tags missing → use track tags. Common on a partially-
        // tagged catalog (one orphan single mixed into an album-tagged
        // library); the user wouldn't expect that one track to play
        // at full level when everything else is normalised.
        let tags = ReplayGainTags {
            track_gain_db: Some(-9.0),
            track_peak: None,
            album_gain_db: None,
            album_peak: None,
        };
        let factor = compute_gain_factor_for(&tags, REPLAY_GAIN_MODE_ALBUM, 0.0);
        let expected = 10f32.powf(-9.0 / 20.0);
        assert!((factor - expected).abs() < 0.001, "factor = {factor}");
    }

    #[test]
    fn compute_gain_factor_preamp_adds() {
        let tags = ReplayGainTags {
            track_gain_db: Some(-6.0),
            ..Default::default()
        };
        // -6 + 6 = 0 dB → factor 1.0
        let factor = compute_gain_factor_for(&tags, REPLAY_GAIN_MODE_TRACK, 6.0);
        assert!((factor - 1.0).abs() < 0.001, "factor = {factor}");
    }

    #[test]
    fn compute_gain_factor_clips_to_peak() {
        // +6 dB on a 0.9 peak would put the highest sample at
        // ~1.79 (clipping). Peak limiter should clamp to 1/0.9 ≈ 1.111
        // so the loudest sample reaches but doesn't exceed full-scale.
        let tags = ReplayGainTags {
            track_gain_db: Some(0.0),
            track_peak: Some(0.9),
            ..Default::default()
        };
        let factor = compute_gain_factor_for(&tags, REPLAY_GAIN_MODE_TRACK, 6.0);
        let expected = 1.0 / 0.9;
        assert!((factor - expected).abs() < 0.001, "factor = {factor}");
    }

    // ── DSD ────────────────────────────────────────────────────────

    #[test]
    fn detect_format_dsf() {
        assert_eq!(detect_format("https://srv/track.dsf"), AudioFormat::Dsd);
        assert_eq!(detect_format("https://srv/Track.DSF"), AudioFormat::Dsd);
        assert_eq!(
            detect_format("https://srv/track.dsf?token=abc"),
            AudioFormat::Dsd,
        );
    }

    // DoP packing reference values — the key invariants under test:
    //   1. Marker byte lands in the high 8 bits of the 24-bit-in-32
    //      payload (bits 24-31 of the i32).
    //   2. Both DSF data bytes are bit-reversed (DSF stores LSB-first;
    //      DoP requires MSB-first).
    //   3. Older sample (first byte) ends up higher in the 16-bit
    //      payload than the newer sample.
    #[test]
    fn dop_pack_word_marker_byte_in_high_bits() {
        // 0x05 marker, both data bytes 0 → packed = 0x0500_0000 (in
        // 24-in-32 space, payload << 8).
        let w = dop_pack_word(0, 0, DOP_MARKER_05);
        assert_eq!((w as u32) >> 24, 0x05);
        let w = dop_pack_word(0, 0, DOP_MARKER_FA);
        assert_eq!((w as u32) >> 24, 0xFA);
    }

    #[test]
    fn dop_pack_word_bit_reverses_each_byte() {
        // DSF byte 0b0000_0001 (LSB-first; oldest sample = bit 0 = 1)
        // bit-reversed = 0b1000_0000 = 0x80. With marker 0x05 in the
        // high bits and the older byte in the high payload nibble:
        //   payload24 = 0x05 << 16 | 0x80 << 8 | 0x80
        //             = 0x05_80_80
        // Then << 8 for 24-in-32 packing = 0x0580_8000.
        let w = dop_pack_word(0b0000_0001, 0b0000_0001, DOP_MARKER_05);
        assert_eq!(w as u32, 0x0580_8000);
    }

    #[test]
    fn dop_pack_word_byte_ordering_oldest_high() {
        // First arg is the oldest byte; should land in the upper 8
        // bits of the 16-bit payload. With oldest = 0xFF (reversed
        // = 0xFF), newest = 0x00 (reversed = 0x00):
        //   payload24 = 0x05_FF_00 → << 8 = 0x05FF_0000
        let w = dop_pack_word(0xFF, 0x00, DOP_MARKER_05);
        assert_eq!(w as u32, 0x05FF_0000);
    }

    #[test]
    fn parse_dsf_header_basic_stereo() {
        // Hand-craft a minimal DSF header: 28-byte DSD chunk + 52-byte
        // fmt chunk (DSD64 stereo 1-bit) + 12-byte data chunk header.
        let mut buf: Vec<u8> = Vec::new();
        // DSD chunk: magic + chunk_size + total_size + metadata_offset
        buf.extend_from_slice(b"DSD ");
        buf.extend_from_slice(&28u64.to_le_bytes());
        buf.extend_from_slice(&0u64.to_le_bytes()); // total_size — unused
        buf.extend_from_slice(&0u64.to_le_bytes()); // metadata_offset
        // fmt chunk
        buf.extend_from_slice(b"fmt ");
        buf.extend_from_slice(&52u64.to_le_bytes()); // chunk_size
        buf.extend_from_slice(&1u32.to_le_bytes()); // format_version
        buf.extend_from_slice(&0u32.to_le_bytes()); // format_id (DSD raw)
        buf.extend_from_slice(&2u32.to_le_bytes()); // channel_type (stereo)
        buf.extend_from_slice(&2u32.to_le_bytes()); // channel_num
        buf.extend_from_slice(&2_822_400u32.to_le_bytes()); // DSD64 rate
        buf.extend_from_slice(&1u32.to_le_bytes()); // bits_per_sample
        buf.extend_from_slice(&12_345u64.to_le_bytes()); // sample_count
        buf.extend_from_slice(&4096u32.to_le_bytes()); // block_size_per_channel
        buf.extend_from_slice(&0u32.to_le_bytes()); // reserved
        // data chunk header
        buf.extend_from_slice(b"data");
        buf.extend_from_slice(&0u64.to_le_bytes());

        let mut cursor = std::io::Cursor::new(buf);
        let h = parse_dsf_header(&mut cursor).expect("parse");
        assert_eq!(h.sample_rate_hz, 2_822_400);
        assert_eq!(h.channels, 2);
        assert_eq!(h.bits_per_sample, 1);
        assert_eq!(h.sample_count, 12_345);
        assert_eq!(h.block_size_per_channel, 4096);
    }

    #[test]
    fn parse_dsf_header_rejects_wrong_magic() {
        let mut buf: Vec<u8> = b"NOPE".to_vec();
        buf.resize(28, 0);
        let mut cursor = std::io::Cursor::new(buf);
        assert!(parse_dsf_header(&mut cursor).is_err());
    }

    #[test]
    fn parse_dsf_header_rejects_unsupported_block_size() {
        // Same shape as the basic-stereo test but with a non-spec
        // block_size_per_channel — DSF spec mandates 4096; supporting
        // arbitrary block sizes would need a more careful interleave
        // walker so we reject rather than silently misalign.
        let mut buf: Vec<u8> = Vec::new();
        buf.extend_from_slice(b"DSD ");
        buf.extend_from_slice(&28u64.to_le_bytes());
        buf.extend_from_slice(&0u64.to_le_bytes());
        buf.extend_from_slice(&0u64.to_le_bytes());
        buf.extend_from_slice(b"fmt ");
        buf.extend_from_slice(&52u64.to_le_bytes());
        buf.extend_from_slice(&1u32.to_le_bytes());
        buf.extend_from_slice(&0u32.to_le_bytes());
        buf.extend_from_slice(&2u32.to_le_bytes());
        buf.extend_from_slice(&2u32.to_le_bytes());
        buf.extend_from_slice(&2_822_400u32.to_le_bytes());
        buf.extend_from_slice(&1u32.to_le_bytes());
        buf.extend_from_slice(&0u64.to_le_bytes());
        buf.extend_from_slice(&8192u32.to_le_bytes()); // wrong
        buf.extend_from_slice(&0u32.to_le_bytes());
        buf.extend_from_slice(b"data");
        buf.extend_from_slice(&0u64.to_le_bytes());

        let mut cursor = std::io::Cursor::new(buf);
        assert!(parse_dsf_header(&mut cursor).is_err());
    }

    #[test]
    fn compute_gain_factor_no_clip_when_attenuating() {
        // Attenuation never needs clipping — a peak of 1.0 with -6 dB
        // gain lands at 0.5012, well below full-scale.
        let tags = ReplayGainTags {
            track_gain_db: Some(-6.0),
            track_peak: Some(1.0),
            ..Default::default()
        };
        let factor = compute_gain_factor_for(&tags, REPLAY_GAIN_MODE_TRACK, 0.0);
        assert!(factor < 1.0, "factor = {factor}");
        assert!(factor * 1.0 < 1.0); // safe
    }
}

// ── Test-tone (kept from the foundation commit) ─────────────────────────────

#[tauri::command]
pub fn play_test_tone(
    device_name: Option<String>,
    frequency_hz: f32,
    duration_ms: u32,
) -> Result<(), String> {
    // Test tone preempts FLAC playback the same way play_url does —
    // single-slot for `current`, last call wins. The preload slot is
    // also cleared so a tone press during a preloaded album doesn't
    // leave a stale next-track sitting around.
    {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        engine.current = None;
        engine.preload = None;
    }

    let device = pick_output_device(device_name.as_deref())?;

    let config = device
        .default_output_config()
        .map_err(|e| format!("audio: default config: {e}"))?;
    let sample_format = config.sample_format();
    let stream_config: cpal::StreamConfig = config.clone().into();

    let freq = frequency_hz.clamp(50.0, 5000.0);
    let stream = match sample_format {
        SampleFormat::F32 => build_tone_stream::<f32>(&device, &stream_config, freq),
        SampleFormat::I16 => build_tone_stream::<i16>(&device, &stream_config, freq),
        SampleFormat::U16 => build_tone_stream::<u16>(&device, &stream_config, freq),
        other => Err(format!("audio: unsupported sample format {other:?}")),
    }?;
    stream.play().map_err(|e| format!("audio: play: {e}"))?;

    // Tones run on the same single-slot engine as FLAC playback so
    // a play_url call interrupts a tone (and vice versa). Wrap in a
    // minimal ActivePlayback with no decoder thread. The paused +
    // ended + frames_written fields are kept for symmetry with the
    // FLAC path even though the tone generator doesn't honor them
    // — pause on a 2-second test tone makes no sense and the
    // auto-stop fires anyway.
    let stop_flag = Arc::new(AtomicBool::new(false));
    let paused = Arc::new(AtomicBool::new(false));
    let ended = Arc::new(AtomicBool::new(false));
    let frames_written = Arc::new(AtomicU64::new(0));
    let active = ActivePlayback {
        _stream: ActiveStream::Cpal(stream),
        stop_flag: stop_flag.clone(),
        paused,
        ended,
        frames_written,
        decoder_handle: None,
        source_url: format!("tone:{freq}Hz"),
        sample_rate_hz: stream_config.sample_rate.0,
        bit_depth: 16,
        channels: stream_config.channels,
    };
    {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        engine.current = Some(active);
    }

    // Auto-stop on a worker thread.
    let dur = std::time::Duration::from_millis(duration_ms as u64);
    std::thread::spawn(move || {
        std::thread::sleep(dur);
        if stop_flag.load(Ordering::Acquire) {
            return; // already stopped by some other call
        }
        if let Ok(mut engine) = ENGINE.lock() {
            engine.current = None;
        }
    });
    Ok(())
}

fn build_tone_stream<T>(
    device: &cpal::Device,
    config: &cpal::StreamConfig,
    freq_hz: f32,
) -> Result<cpal::Stream, String>
where
    T: SizedSample + cpal::FromSample<f32>,
{
    let sample_rate = config.sample_rate.0 as f32;
    let channels = config.channels as usize;
    let mut sample_clock = 0f32;
    let stream = device
        .build_output_stream(
            config,
            move |buf: &mut [T], _: &cpal::OutputCallbackInfo| {
                for frame in buf.chunks_mut(channels) {
                    let v = (sample_clock * freq_hz * 2.0 * std::f32::consts::PI / sample_rate)
                        .sin()
                        * 0.2; // -14 dBFS
                    let s = T::from_sample(v);
                    for sample in frame.iter_mut() {
                        *sample = s;
                    }
                    sample_clock += 1.0;
                }
            },
            |err| eprintln!("audio: stream error: {err}"),
            None,
        )
        .map_err(|e| format!("audio: build stream: {e}"))?;
    Ok(stream)
}
