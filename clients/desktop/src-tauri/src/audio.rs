// Native audio engine — v2.1 Track E.
//
// Size note: this file is large (~3700 LOC) because it bundles HTTP
// fetch + symphonia + ReplayGain + ringbuf orchestration + the Tauri
// command surface (DSD parsing, DSD -> PCM conversion and DoP framing
// live in dsd.rs; the DSD pipeline glue is here). Splitting into
// modules (`http.rs`, `format.rs`, `replay_gain.rs`, `pipeline.rs`)
// is on the cleanup list but deferred until after macOS/Linux
// exclusive backends land — those touch the same dispatch tables and
// would force a second pass through the boundaries.
//
// Threading model (the part that's easy to get wrong):
//
//   ┌──────────────────────────┐    ┌────────────────────────┐
//   │ decoder thread           │    │ output thread          │
//   │  ─ HTTP GET (ureq)       │ →  │  ─ realtime, no alloc  │
//   │  ─ symphonia decode      │    │  ─ pulls from ringbuf  │
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
use std::sync::atomic::{AtomicBool, AtomicI32, AtomicU32, AtomicU64, AtomicU8, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

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
// resampling them. cpal (still as of 0.18) hard-codes shared mode on every host —
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

// Reopen budget for the HTTP source (HttpSeekableSource), per Read
// call. It ends at the agent's connect/read timeouts long before the
// budget is exhausted — an upper bound against an infinite retry loop,
// not a realistic count. A transient blip on a single read needs one
// reopen + retry; we add one for slack. Anything past that is a hard
// outage we'd rather surface than spin on.
const HTTP_SEEKABLE_READ_MAX_RETRIES: u32 = 3;

// Interruptible stream reads. The audio body is read on a socket whose
// read timeout is short (STREAM_READ_POLL), so a read on a stalled
// connection returns every couple of seconds and the reader can check
// its pipeline's stop flag; a stall is still waited out on the same
// connection until STREAM_STALL_BUDGET (the old single 300 s read
// timeout) has passed. Stop/Next therefore never wait on a dead server,
// while a wifi roam or a disk spinning up mid-track still recovers.
const STREAM_READ_POLL: Duration = Duration::from_secs(2);
const STREAM_STALL_BUDGET: Duration = Duration::from_secs(300);
// How long dropping a pipeline waits for its stopped decoder thread to
// exit before leaving it to finish on its own (see join_decoder).
const DECODER_JOIN_BUDGET: Duration = Duration::from_millis(250);
// After the ringbuf has drained at end of track, how long the device's
// own buffer (10-20 ms on WASAPI, more on some ALSA setups) gets to play
// out before `ended` tells the frontend to move on.
const END_OF_TRACK_TAIL: Duration = Duration::from_millis(50);

// What the engine accepts from a stream header before sizing anything
// from it. 8 kHz is the lowest rate any lossless format here produces,
// 768 kHz the highest PCM rate a DAC takes; 8 channels is 7.1.
const MIN_SAMPLE_RATE_HZ: u32 = 8_000;
const MAX_SAMPLE_RATE_HZ: u32 = 768_000;
const MAX_CHANNELS: u16 = 8;
// ~200 ms of samples at the largest accepted rate × channels.
const MAX_RING_SAMPLES: usize = (MAX_SAMPLE_RATE_HZ as usize) * (MAX_CHANNELS as usize) / 5;

/// Rejects a stream whose header declares a rate or channel count no
/// real file has, before the ringbuf (sized from them) is allocated: a
/// crafted header would otherwise abort the app on the allocation.
fn validate_stream_shape(sample_rate_hz: u32, channels: u16) -> Result<(), String> {
    if !(MIN_SAMPLE_RATE_HZ..=MAX_SAMPLE_RATE_HZ).contains(&sample_rate_hz) {
        return Err(format!("audio: unsupported sample rate {sample_rate_hz} Hz"));
    }
    if !(1..=MAX_CHANNELS).contains(&channels) {
        return Err(format!("audio: unsupported channel count {channels}"));
    }
    Ok(())
}

/// Ringbuf capacity in samples: ~200 ms of audio, at least 8192
/// samples, never more than MAX_RING_SAMPLES.
fn ring_capacity(sample_rate_hz: u32, channels: u16) -> usize {
    ((sample_rate_hz as usize).saturating_mul(channels as usize) / 5)
        .clamp(8192, MAX_RING_SAMPLES)
}

/// The error a reader returns once its pipeline has been stopped. Not
/// `Interrupted`: std's read_exact / read_to_end retry that kind, which
/// would spin on a stopped reader.
fn stopped_error() -> std::io::Error {
    std::io::Error::new(std::io::ErrorKind::Other, "audio: stream stopped")
}

/// A socket read that hit its (short) timeout rather than failing.
/// Windows reports TimedOut; Unix reports WouldBlock (ureq normalises
/// most of those, but not every layer goes through it).
fn is_timeout(e: &std::io::Error) -> bool {
    matches!(
        e.kind(),
        std::io::ErrorKind::TimedOut | std::io::ErrorKind::WouldBlock
    )
}

/// A request that failed because the response headers didn't arrive
/// within the read timeout (ureq wraps the socket's io::Error).
fn is_read_timeout(e: &ureq::Error) -> bool {
    match e {
        ureq::Error::Transport(t) if t.kind() == ureq::ErrorKind::Io => {
            std::error::Error::source(t)
                .and_then(|s| s.downcast_ref::<std::io::Error>())
                .is_some_and(is_timeout)
        }
        _ => false,
    }
}

/// Sleeps for `total`, waking early when `stop` is set.
fn sleep_unless_stopped(total: Duration, stop: &AtomicBool) {
    let deadline = Instant::now() + total;
    while !stop.load(Ordering::Acquire) {
        let now = Instant::now();
        if now >= deadline {
            return;
        }
        std::thread::sleep((deadline - now).min(Duration::from_millis(10)));
    }
}

/// The ureq agent for audio bodies: short per-read timeout (see
/// STREAM_READ_POLL) and no redirects.
fn stream_agent(read_poll: Duration) -> ureq::Agent {
    ureq::AgentBuilder::new()
        .timeout_connect(Duration::from_secs(10))
        .timeout_read(read_poll)
        // Disable HTTP redirects: enforce_url_origin already pinned
        // the URL to the configured server, but a malicious or
        // misconfigured server could 30x to a different origin and
        // we'd silently follow. ureq strips the Authorization header
        // across redirects by default — but no reason to make the
        // request at all. Audio bodies should be served direct.
        .redirects(0)
        .build()
}

/// GET `url`, from byte `range_from` on when given. Each attempt's
/// response headers get one STREAM_READ_POLL; a slow server (a disk
/// spinning up) is retried until STREAM_STALL_BUDGET has passed, and
/// `stop` ends the wait at once.
fn get_stream(
    agent: &ureq::Agent,
    url: &str,
    bearer: Option<&str>,
    range_from: Option<u64>,
    stop: &AtomicBool,
) -> Result<ureq::Response, ureq::Error> {
    let started = Instant::now();
    loop {
        if stop.load(Ordering::Acquire) {
            return Err(stopped_error().into());
        }
        let mut req = agent.get(url);
        if let Some(t) = bearer {
            req = req.set("Authorization", &format!("Bearer {t}"));
        }
        if let Some(offset) = range_from {
            req = req.set("Range", &format!("bytes={offset}-"));
        }
        match req.call() {
            Err(e) if is_read_timeout(&e) && started.elapsed() < STREAM_STALL_BUDGET => {
                eprintln!("audio: slow response from the server; retrying");
            }
            result => return result,
        }
    }
}

/// Joins a stopped decoder thread, waiting at most DECODER_JOIN_BUDGET.
/// A thread still busy after that (blocked in a connect, or a read that
/// hasn't come back yet) is left to exit on its own: it holds only its
/// HTTP body and its end of the ringbuf, and every read it makes checks
/// the stop flag, so it ends within about STREAM_READ_POLL. This is
/// what keeps Stop/Next from waiting on a stalled server.
fn join_decoder(handle: JoinHandle<()>) {
    let deadline = Instant::now() + DECODER_JOIN_BUDGET;
    while !handle.is_finished() {
        if Instant::now() >= deadline {
            eprintln!("audio: decoder thread still busy after stop; leaving it to exit on its own");
            return;
        }
        std::thread::sleep(Duration::from_millis(2));
    }
    let _ = handle.join();
}

// ── Last call wins ─────────────────────────────────────────────────────────
//
// The play / seek / preload / stop / test-tone commands run off the main
// thread (a slow server must never freeze the window), so two of them can
// be in flight at once: a seek still fetching when the user seeks again,
// a play the user skips past before it starts. Each call opens a new
// generation for the slot it replaces and supersedes the previous one.
// A superseded call stops reading (its generation flag doubles as its
// pipeline's stop flag), drops what it prepared instead of installing
// it, and reports the engine state instead of an error: the frontend
// reads an error as "this file can't play natively" and would fall back
// for a track the user has already moved past.
struct Generation(Mutex<Option<Arc<AtomicBool>>>);

impl Generation {
    const fn new() -> Self {
        Generation(Mutex::new(None))
    }

    /// Start a new generation, superseding the one in flight. The
    /// returned flag is set once a newer call (or a stop) supersedes it.
    fn begin(&self) -> Arc<AtomicBool> {
        let mine = Arc::new(AtomicBool::new(false));
        let mut slot = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(older) = slot.replace(mine.clone()) {
            older.store(true, Ordering::Release);
        }
        mine
    }

    /// The call holding `mine` is done (installed or abandoned); later
    /// calls no longer need to supersede it.
    fn end(&self, mine: &Arc<AtomicBool>) {
        let mut slot = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if slot.as_ref().is_some_and(|f| Arc::ptr_eq(f, mine)) {
            *slot = None;
        }
    }

    /// Start a generation only if no call is in flight. For the engine's
    /// own work (a DoP track switching to PCM) that must not supersede a
    /// play or seek the user started: that call decides again anyway.
    fn begin_if_idle(&self) -> Option<Arc<AtomicBool>> {
        let mut slot = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if slot.is_some() {
            return None;
        }
        let mine = Arc::new(AtomicBool::new(false));
        *slot = Some(mine.clone());
        Some(mine)
    }

    /// For the moment a call installs what it prepared: true when it is
    /// still the newest, and from then on no later call sets `mine` (which
    /// has become the installed pipeline's stop flag). One step under the
    /// generation's lock, so a `begin` can't slip in between the check and
    /// the hand-over. False when superseded: drop what was prepared.
    fn claim(&self, mine: &Arc<AtomicBool>) -> bool {
        let mut slot = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if mine.load(Ordering::Acquire) {
            return false;
        }
        if slot.as_ref().is_some_and(|f| Arc::ptr_eq(f, mine)) {
            *slot = None;
        }
        true
    }
}

/// Calls that replace `current`: play, seek, stop, test tone.
static CURRENT_GEN: Generation = Generation::new();
/// Calls that replace `preload`: preload, stop, test tone.
static PRELOAD_GEN: Generation = Generation::new();
/// Serialises "drop the old output, open the new one, install it", so
/// two calls never hold the device at once (WASAPI exclusive refuses a
/// second open). The network phase before it runs in parallel.
static SWAP_LOCK: Mutex<()> = Mutex::new(());

/// Runs a blocking engine call on Tauri's blocking pool instead of the
/// main thread, which would freeze the window for as long as the call
/// waits on the network or a decoder thread.
async fn off_main_thread<T: Send + 'static>(
    f: impl FnOnce() -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(f)
        .await
        .map_err(|e| format!("audio: engine call failed: {e}"))?
}

static EXCLUSIVE_MODE: AtomicBool = AtomicBool::new(false);

// "DSD over DoP": send DSD to the DAC as DoP instead of converting it to
// PCM. Off by default — a DAC without DoP support plays DoP as loud
// noise, and nothing on the host can tell whether it has it. Even when
// on, DoP is used only where its words reach the DAC untouched (see
// dsd::dop_rate): Windows, WASAPI exclusive at the DoP rate, volume at
// exactly 100 %, no ReplayGain change. Persisted by the frontend
// (localStorage) and re-applied on launch, like EXCLUSIVE_MODE.
static DSD_DOP: AtomicBool = AtomicBool::new(false);

// User-controlled output volume, 0..=1.0. Stored as the bit pattern
// of an f32 in an AtomicU32 so the realtime write loops can read it
// lock-free. 1.0 = unity (no scaling), 0.0 = silent. The HTML
// <audio> path applies volume on the element directly; the native
// engine has no element to attach to, so each backend's write loop
// multiplies samples by this value before handing bytes to the
// device. Applied at the output side rather than in the decoder so
// slider movements take effect immediately, not after the ringbuf
// drains.
static OUTPUT_VOLUME_BITS: AtomicU32 = AtomicU32::new(0x3f800000); // 1.0_f32

#[inline]
pub fn output_volume() -> f32 {
    f32::from_bits(OUTPUT_VOLUME_BITS.load(Ordering::Acquire))
}

/// How many samples one render period may take from the ring: whole
/// frames only. The decoder can leave half a frame in the ring for a
/// moment (a push split by a full ring); an output that took it and
/// padded the rest of the frame with silence would start every later
/// frame on the wrong channel, swapping left and right for the rest of
/// the track. `wanted` is the period's size in samples.
#[inline]
pub(crate) fn whole_frame_samples(available: usize, channels: usize, wanted: usize) -> usize {
    if channels == 0 {
        return 0;
    }
    (available.min(wanted) / channels) * channels
}

/// The software volume, for the integer sample formats the cpal output
/// carries. Callers skip it at unity so 100 % stays bit-perfect.
pub(crate) trait ScaleVolume: Copy {
    fn scaled(self, vol: f32) -> Self;
}

impl ScaleVolume for i16 {
    #[inline]
    fn scaled(self, vol: f32) -> Self {
        (f32::from(self) * vol) as i16
    }
}

impl ScaleVolume for i32 {
    #[inline]
    fn scaled(self, vol: f32) -> Self {
        (f64::from(self) * f64::from(vol)) as i32
    }
}

// Active-backend reporting. The user-visible "is bit-perfect actually
// engaged?" signal — the EXCLUSIVE_MODE toggle just *requests* exclusive,
// but the WASAPI open can fall back to cpal silently when the device
// rejects the format or another app holds it. ACTIVE_BACKEND tracks
// what we're really running on, so the settings UI can render a
// "Currently: WASAPI exclusive" / "cpal shared" badge instead of
// trusting the toggle's "intended" state.
//
// OUTPUT_IS_BLUETOOTH carries a separate truth: the active output
// device is a Bluetooth endpoint, which means there's a lossy codec
// (SBC/AAC/aptX/LDAC) in the chain regardless of which WASAPI mode
// we engaged. Bit-perfect is unattainable on BT — the badge needs
// to say so honestly.
pub static OUTPUT_IS_BLUETOOTH: AtomicBool = AtomicBool::new(false);

/// Manual override for the BT detection. Detection from device names
/// is heuristic — wireless headphones often report identically to
/// wired ones plugged into a USB DAC (same `Headphones (Brand Model)`
/// string shape, same form factor). Robust detection requires Windows
/// PnP enumeration (~150 LOC of windows-rs walking the device tree
/// to find the parent's bus enumerator); not worth it for the niche
/// case. Instead, the user flips this toggle once per session if the
/// auto-detect misses their BT device. The settings UI persists the
/// flag in localStorage and re-applies it on launch.
pub static OUTPUT_BLUETOOTH_OVERRIDE: AtomicBool = AtomicBool::new(false);

/// Heuristic Bluetooth detection across Windows BT stacks. Returns
/// true when the device is plausibly a Bluetooth endpoint based on
/// (in order of reliability) the device ID, the adapter friendly
/// name, and the endpoint friendly name.
///
/// Detection layers, most-to-least reliable:
///   1. Endpoint ID containing `BTHENUM` — Microsoft's stack always
///      enumerates BT devices through this bus. Strongest signal.
///   2. Interface or device-friendly name containing explicit BT
///      keywords ("bluetooth", "hands-free", "a2dp"). Catches the
///      Microsoft and most third-party stacks (Broadcom, Realtek BT).
///   3. Friendly-name endings BT manufacturers commonly use
///      (" stereo", " hands free", "headset") combined with absence
///      of wired indicators. Catches Soundcore, Sony WH-/WF-, AirPods,
///      Bose QC, Beats, Jabra, etc., where the brand name in isolation
///      isn't a tell.
///
/// Logs the probe inputs at debug-build time so when a heuristic
/// miss surfaces in the wild, the user can paste the log line and
/// we can widen layer 3 without guessing at name formats.
///
/// Used at stream-open time on the Windows backends to set the
/// OUTPUT_IS_BLUETOOTH flag the settings UI reads.
#[cfg(target_os = "windows")]
pub fn device_appears_to_be_bluetooth(device: &wasapi::Device) -> bool {
    let interface_name = device.get_interface_friendlyname().unwrap_or_default();
    let friendly_name = device.get_friendlyname().unwrap_or_default();
    let id = device.get_id().unwrap_or_default();
    let probe = format!("{interface_name} | {friendly_name} | {id}");
    let lower = probe.to_ascii_lowercase();

    if cfg!(debug_assertions) {
        eprintln!("audio: BT probe — {probe}");
    }

    // Layer 1: BTHENUM bus enumerator in the device ID.
    if lower.contains("bthenum") {
        return true;
    }
    // Layer 2: explicit BT keywords anywhere in the name fields.
    if lower.contains("bluetooth")
        || lower.contains("hands-free")
        || lower.contains("hands free")
        || lower.contains("a2dp")
        || lower.contains("avrcp")
    {
        return true;
    }
    // Layer 3 (heuristic) was tried and removed: BT vendors like
    // Soundcore, Sony, Bose etc. report device strings that are
    // shape-identical to wired headphones on a USB sound card
    // ("Headphones (Soundcore Life Q30)"). String-based heuristics
    // can't distinguish them. The settings page exposes a manual
    // OUTPUT_BLUETOOTH_OVERRIDE toggle for these cases.
    false
}
//
// Encoded as a u8 because AtomicU8 is one cycle to load/store on
// every platform and the enum has only 4 cases. See
// audio_get_active_backend below for the wire format.
const BACKEND_NONE: u8 = 0;        // No active playback.
const BACKEND_CPAL_SHARED: u8 = 1; // cpal default (BufferSize::Default).
const BACKEND_CPAL_TIGHT: u8 = 2;  // cpal with EXCLUSIVE_MODE on (Fixed buffer).
const BACKEND_WASAPI_EXCLUSIVE: u8 = 3; // raw WASAPI exclusive — bit-perfect.
const BACKEND_WASAPI_SHARED: u8 = 4;    // raw WASAPI shared + AUTOCONVERTPCM (OS resamples).
static ACTIVE_BACKEND: AtomicU8 = AtomicU8::new(BACKEND_NONE);

#[tauri::command]
pub fn audio_set_exclusive_mode(enabled: bool) -> Result<(), String> {
    EXCLUSIVE_MODE.store(enabled, Ordering::Release);
    leave_dop_if_needed();
    Ok(())
}

#[tauri::command]
pub fn audio_get_exclusive_mode() -> Result<bool, String> {
    Ok(EXCLUSIVE_MODE.load(Ordering::Acquire))
}

#[tauri::command]
pub fn audio_set_dsd_dop(enabled: bool) -> Result<(), String> {
    DSD_DOP.store(enabled, Ordering::Release);
    leave_dop_if_needed();
    Ok(())
}

#[tauri::command]
pub fn audio_get_dsd_dop() -> Result<bool, String> {
    Ok(DSD_DOP.load(Ordering::Acquire))
}

#[tauri::command]
pub fn audio_set_volume(value: f32) -> Result<(), String> {
    if !value.is_finite() {
        return Err("audio: volume must be finite".into());
    }
    let clamped = value.clamp(0.0, 1.0);
    OUTPUT_VOLUME_BITS.store(clamped.to_bits(), Ordering::Release);
    // DoP words can't be scaled: a DoP track switches to PCM (see
    // leave_dop_if_needed); until it has, the DoP output plays silence.
    leave_dop_if_needed();
    Ok(())
}

/// What the DoP decision for a track about to be prepared looks at.
fn dop_request(gain: f32) -> crate::dsd::DopRequest {
    crate::dsd::DopRequest {
        enabled: DSD_DOP.load(Ordering::Acquire),
        exclusive: EXCLUSIVE_MODE.load(Ordering::Acquire),
        volume: output_volume(),
        gain,
    }
}

/// The DoP conditions that can change while a track plays (its gain was
/// fixed when it was prepared): checked again when the output opens and
/// whenever one of them changes.
fn dop_conditions_hold() -> bool {
    DSD_DOP.load(Ordering::Acquire)
        && EXCLUSIVE_MODE.load(Ordering::Acquire)
        && output_volume() == 1.0
}

// ── DoP -> PCM mid-track ───────────────────────────────────────────────────
//
// Volume changes while DoP plays must not scale DoP words (a scaled DoP
// word is noise). The choice made here: the track switches to PCM
// conversion at the next safe point, rather than the volume change being
// refused — so the volume slider, mute and every other way of changing
// it keep doing what they say, with no special case in the UI. The same
// happens when DoP or exclusive output is switched off mid-track (turning
// DoP off is what someone hearing noise from a DAC without DoP does).
//
// The safe point is a rebuild of the track at its current position, the
// way a seek rebuilds it: the DoP output is closed (it plays out the DoP
// idle pattern), the same track is prepared as PCM from a computed byte
// offset, and the PCM output opens. Until the DoP output is closed its
// render loop sends idle frames instead of the DoP words whenever the
// volume isn't 100 %, so nothing louder than asked for comes out in
// between. The gap is a seek's (a Range request and a device open);
// audio_state keeps reporting the track as playing across it, so the
// frontend's position polling carries on.

/// A DoP track switching to PCM, while neither output is installed.
struct Switching {
    // The switch's generation, so only it clears this.
    owner: Arc<AtomicBool>,
    // Shared with the PCM output it is opening, so a pause or resume in
    // between isn't lost.
    paused: Arc<AtomicBool>,
    source_url: String,
    position_ms: u64,
    sample_rate_hz: u32,
    bit_depth: u32,
    channels: u16,
}

/// Starts the switch of a DoP track to PCM if one is playing and DoP is
/// no longer allowed. Returns at once (it runs from the volume command,
/// on the main thread); the switch runs on the blocking pool.
fn leave_dop_if_needed() {
    if dop_conditions_hold() {
        return;
    }
    let is_dop = |engine: &EngineState| {
        engine
            .current
            .as_ref()
            .and_then(|p| p.dop.as_ref())
            .is_some_and(|d| !d.leaving)
    };
    match ENGINE.lock() {
        Ok(engine) if is_dop(&engine) => {}
        _ => return,
    }
    // A play or seek in flight opens its output with DoP decided afresh,
    // so it needs no switch — and must not be superseded by one.
    let Some(gen) = CURRENT_GEN.begin_if_idle() else {
        return;
    };
    let claimed = match ENGINE.lock() {
        Ok(mut engine) if is_dop(&engine) => {
            if let Some(d) = engine.current.as_mut().and_then(|p| p.dop.as_mut()) {
                d.leaving = true;
            }
            true
        }
        _ => false,
    };
    if !claimed {
        CURRENT_GEN.end(&gen);
        return;
    }
    tauri::async_runtime::spawn_blocking(move || {
        if let Err(e) = leave_dop_now(&gen) {
            eprintln!("audio: switching DSD from DoP to PCM failed: {e}");
        }
        CURRENT_GEN.end(&gen);
    });
}

fn leave_dop_now(gen: &Arc<AtomicBool>) -> Result<(), String> {
    let (old, url, position_ms, paused, rebuild) = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if gen.load(Ordering::Acquire) {
            return Ok(()); // a newer play/seek/stop took over
        }
        let Some(p) = engine.current.as_ref() else {
            return Ok(());
        };
        let Some(dop) = p.dop.as_ref() else {
            return Ok(());
        };
        let rebuild = dop.rebuild.clone();
        let switching = Switching {
            owner: gen.clone(),
            paused: p.paused.clone(),
            source_url: p.source_url.clone(),
            position_ms: p.position_ms(),
            sample_rate_hz: p.sample_rate_hz,
            bit_depth: p.bit_depth,
            channels: p.channels,
        };
        let (url, position_ms, paused) = (
            switching.source_url.clone(),
            switching.position_ms,
            switching.paused.clone(),
        );
        engine.switching = Some(switching);
        (engine.current.take(), url, position_ms, paused, rebuild)
    };
    // However this ends, a switch that didn't install its PCM output
    // leaves no snapshot behind (dropped after install has returned).
    let _clear = ClearSwitching(gen.clone());
    // Closing the DoP output plays out the idle pattern and frees the
    // device (exclusive mode allows one stream). Outside the lock.
    drop(old);
    let device = pick_output_device(rebuild.device_name.as_deref())?;
    let prepared = prepare_pipeline(&url, rebuild.bearer.as_deref(), position_ms, gen.clone(), false)?;
    install(gen, prepared, &device, url, position_ms, rebuild, paused).map(|_| ())
}

struct ClearSwitching(Arc<AtomicBool>);

impl Drop for ClearSwitching {
    fn drop(&mut self) {
        if let Ok(mut engine) = ENGINE.lock() {
            if engine
                .switching
                .as_ref()
                .is_some_and(|s| Arc::ptr_eq(&s.owner, &self.0))
            {
                engine.switching = None;
            }
        }
    }
}

/// Reports the audio backend currently running. Used by the settings
/// UI to surface whether the EXCLUSIVE_MODE toggle actually engaged
/// (WASAPI exclusive on Windows + a supporting device) or whether
/// the open silently fell back to cpal. The returned strings are
/// stable wire identifiers; the frontend maps them to user-facing
/// labels.
/// Reports whether the active output endpoint is a Bluetooth device.
/// The bit-perfect chain is broken at the Windows BT audio service
/// (samples go through SBC/AAC/aptX/LDAC encode before transmission)
/// regardless of WASAPI mode. The settings UI uses this to soften
/// the "bit-perfect" badge to "Bluetooth · lossy codec".
#[tauri::command]
pub fn audio_get_output_is_bluetooth() -> Result<bool, String> {
    let detected = OUTPUT_IS_BLUETOOTH.load(Ordering::Acquire);
    let overridden = OUTPUT_BLUETOOTH_OVERRIDE.load(Ordering::Acquire);
    Ok(detected || overridden)
}

#[tauri::command]
pub fn audio_set_bluetooth_override(enabled: bool) -> Result<(), String> {
    OUTPUT_BLUETOOTH_OVERRIDE.store(enabled, Ordering::Release);
    Ok(())
}

#[tauri::command]
pub fn audio_get_active_backend() -> Result<&'static str, String> {
    Ok(match ACTIVE_BACKEND.load(Ordering::Acquire) {
        BACKEND_CPAL_SHARED => "cpal-shared",
        BACKEND_CPAL_TIGHT => "cpal-tight",
        BACKEND_WASAPI_EXCLUSIVE => "wasapi-exclusive",
        BACKEND_WASAPI_SHARED => "wasapi-shared",
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
    let default_name = host.default_output_device().and_then(|d| device_name(&d));

    let devices = host
        .output_devices()
        .map_err(|e| format!("audio: enumerate output devices: {e}"))?;

    let mut out = Vec::new();
    for dev in devices {
        let name = device_name(&dev).unwrap_or_else(|| "<unnamed>".to_string());
        let is_default = default_name.as_deref() == Some(name.as_str());
        let default_output_summary = dev.default_output_config().ok().map(|c| {
            format!(
                "{} ch · {} Hz · {:?}",
                c.channels(),
                c.sample_rate(),
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
    #[cfg(target_os = "windows")]
    WasapiShared(crate::windows_shared::WasapiSharedStream),
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
    // Set true once the decoder reached the end of the stream (or hit
    // an unrecoverable error) AND the samples it produced have played
    // out of the ringbuf — never when the stop_flag fired. The frontend
    // polls this on audio_state to fire next() in the queue without
    // needing a separate event channel back from Rust; the next play
    // drops this stream, so raising it any earlier cut the track's tail.
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
    // Some while DSD goes out as DoP: what switching it to PCM needs.
    dop: Option<DopPlayback>,
}

/// What a play or seek was called with, kept so the engine can rebuild
/// the track on its own (a DoP track switching to PCM).
#[derive(Clone, Default)]
struct Rebuild {
    bearer: Option<String>,
    device_name: Option<String>,
}

#[derive(Default)]
#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
struct DopPlayback {
    rebuild: Rebuild,
    // A switch to PCM is under way.
    leaving: bool,
}

impl ActivePlayback {
    fn position_ms(&self) -> u64 {
        // ms = frames * 1000 / rate. Saturating so a stream that has run
        // for days can't overflow the intermediate u64.
        self.frames_written
            .load(Ordering::Acquire)
            .saturating_mul(1000)
            / u64::from(self.sample_rate_hz.max(1))
    }
}

impl Drop for ActivePlayback {
    fn drop(&mut self) {
        // Signal the decoder and give it a moment to exit; the decoder
        // polls stop_flag between packets and on every stalled read, so
        // the normal case is a clean stop in milliseconds. A decoder
        // stuck on a dead connection is left to finish on its own (see
        // join_decoder) rather than holding up Stop/Next.
        self.stop_flag.store(true, Ordering::Release);
        if let Some(h) = self.decoder_handle.take() {
            join_decoder(h);
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
    // The track the latest play asked for. A seek targets this rather
    // than `current`, which is empty while a play or an earlier seek is
    // still fetching.
    requested_url: Option<String>,
    // A DoP track switching to PCM (see leave_dop_if_needed).
    switching: Option<Switching>,
}

static ENGINE: Mutex<EngineState> = Mutex::new(EngineState {
    current: None,
    preload: None,
    requested_url: None,
    switching: None,
});

impl ActiveStream {
    /// True when the output stopped on its own (device unplugged or
    /// invalidated) rather than being stopped by the engine.
    fn output_failed(&self) -> bool {
        match self {
            ActiveStream::Cpal(_) => false,
            #[cfg(target_os = "windows")]
            ActiveStream::Wasapi(s) => s.failed(),
            #[cfg(target_os = "windows")]
            ActiveStream::WasapiShared(s) => s.failed(),
        }
    }
}

/// A track that's been HTTP-fetched + FLAC-header-parsed and whose
/// decoder thread is already producing samples into a ringbuf,
/// waiting for the cpal side to be opened. Held in
/// [`EngineState::preload`] until the matching `audio_play_url` call
/// promotes it — when promoted, the consumer + decoder thread move
/// straight into the new ActivePlayback so no work is wasted.
///
/// Drop signals the decoder to stop (and waits briefly for it, see
/// join_decoder) so an unconsumed preload (user changed their mind,
/// queue reordered) doesn't leave its decoder thread running.
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
    // DSD that may go out as DoP: its decoder waits for the output to
    // say which (PCM pipelines, DSD converted to PCM included, are None).
    dsd: Option<DsdChoice>,
}

/// The output's half of a DSD pipeline prepared while DoP was allowed.
/// `sample_rate_hz` on the slot is the PCM rate (the fallback); DoP runs
/// at `dop_rate_hz`. The decoder produces nothing until the output opens
/// and sends the mode, so the ring never holds the wrong kind of sample;
/// dropping the slot unsent ends the decoder.
struct DsdChoice {
    // Read by the Windows open only; elsewhere DoP is never offered.
    #[cfg_attr(not(target_os = "windows"), allow(dead_code))]
    dop_rate_hz: u32,
    mode_tx: std::sync::mpsc::SyncSender<DsdMode>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[cfg_attr(not(target_os = "windows"), allow(dead_code))]
enum DsdMode {
    Pcm,
    Dop,
}

impl DsdChoice {
    fn settle(self, mode: DsdMode) {
        // Sent once into a one-slot channel: never blocks. A decoder that
        // has already exited (stopped) just doesn't get it.
        let _ = self.mode_tx.send(mode);
    }
}

impl Drop for PreloadSlot {
    fn drop(&mut self) {
        // Only signal stop if we still own the decoder. If
        // `decoder_handle` was taken (ownership transferred to an
        // ActivePlayback), the decoder is now driving live playback
        // and stopping it here would silence the stream as soon as
        // open_active_from_prepared returns. ActivePlayback's own
        // Drop will signal stop when playback ends.
        if let Some(h) = self.decoder_handle.take() {
            self.stop_flag.store(true, Ordering::Release);
            join_decoder(h);
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
    Ok(match (&engine.current, &engine.switching) {
        (Some(p), _) => {
            PlaybackStatus {
                // An output that died under us (device unplugged) reads
                // as "engine stopped on its own", which the frontend
                // already handles, instead of playing at a frozen clock.
                playing: !p._stream.output_failed(),
                paused: p.paused.load(Ordering::Acquire),
                ended: p.ended.load(Ordering::Acquire),
                position_ms: p.position_ms(),
                source_url: Some(p.source_url.clone()),
                sample_rate_hz: Some(p.sample_rate_hz),
                bit_depth: Some(p.bit_depth),
                channels: Some(p.channels),
            }
        }
        // A DoP track switching to PCM is still the track playing.
        (None, Some(s)) => PlaybackStatus {
            playing: true,
            paused: s.paused.load(Ordering::Acquire),
            ended: false,
            position_ms: s.position_ms,
            source_url: Some(s.source_url.clone()),
            sample_rate_hz: Some(s.sample_rate_hz),
            bit_depth: Some(s.bit_depth),
            channels: Some(s.channels),
        },
        (None, None) => PlaybackStatus {
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
pub async fn stop_audio() -> Result<(), String> {
    // A stop is the newest call for both slots: it supersedes any play,
    // seek or preload still fetching, so none of them installs after it.
    let current_gen = CURRENT_GEN.begin();
    let preload_gen = PRELOAD_GEN.begin();
    off_main_thread(move || {
        let result = stop_now(&current_gen, &preload_gen);
        CURRENT_GEN.end(&current_gen);
        PRELOAD_GEN.end(&preload_gen);
        result
    })
    .await
}

fn stop_now(current_gen: &AtomicBool, preload_gen: &AtomicBool) -> Result<(), String> {
    // Clearing both slots so an in-flight preload also stops — otherwise
    // stop_audio would leave a decoder thread running for a cancelled track.
    // Take the slots out UNDER the lock but drop them AFTER releasing it: the
    // ActivePlayback/PreloadSlot Drop waits (briefly) for the decoder thread —
    // holding the engine lock across that would stall every other audio command.
    // A slot whose generation a newer call has already taken over is left
    // alone: that call installs its own track (or stop) in it.
    let (old_current, old_preload) = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        let old_current = if current_gen.load(Ordering::Acquire) {
            None
        } else {
            engine.requested_url = None;
            engine.switching = None;
            ACTIVE_BACKEND.store(BACKEND_NONE, Ordering::Release);
            OUTPUT_IS_BLUETOOTH.store(false, Ordering::Release);
            engine.current.take()
        };
        let old_preload = if preload_gen.load(Ordering::Acquire) {
            None
        } else {
            engine.preload.take()
        };
        (old_current, old_preload)
    };
    drop(old_current);
    drop(old_preload);
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
    } else if let Some(s) = engine.switching.as_ref() {
        s.paused.store(true, Ordering::Release);
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
    } else if let Some(s) = engine.switching.as_ref() {
        s.paused.store(false, Ordering::Release);
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
/// SSRF guard for the URL-taking IPC commands. The native engine
/// attaches the bearer to whatever URL the frontend hands it; without
/// this check, a compromised page in the webview (XSS via embedded
/// content, malicious dev-tools eval) could call the IPC with
/// `https://attacker.example/...` and exfiltrate the bearer to any
/// host. We reject anything whose host or scheme doesn't match the
/// configured server URL.
///
/// First-run state (no server URL configured) rejects all play
/// requests — the frontend can't reach a state where it has a track
/// to play without first having a server, so this can't false-positive
/// on a real user. Configurable-server-URL flow is the threat model
/// here, not anonymous web users.
pub(crate) fn enforce_url_origin<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    url: &str,
) -> Result<(), String> {
    use tauri_plugin_store::StoreExt;
    let parsed = url::Url::parse(url).map_err(|e| format!("audio: invalid URL: {e}"))?;
    let store = app
        .store(crate::STORE_FILE)
        .map_err(|e| format!("audio: open store: {e}"))?;
    let server_str = store
        .get(crate::KEY_SERVER_URL)
        .and_then(|v| v.as_str().map(String::from))
        .ok_or_else(|| "audio: no server URL configured".to_string())?;
    let server = url::Url::parse(&server_str)
        .map_err(|e| format!("audio: stored server URL invalid: {e}"))?;
    let same_origin = parsed.scheme() == server.scheme()
        && parsed.host_str() == server.host_str()
        && parsed.port_or_known_default() == server.port_or_known_default();
    if !same_origin {
        return Err(format!(
            "audio: refusing to play cross-origin URL ({} vs configured server {})",
            parsed.host_str().unwrap_or("?"),
            server.host_str().unwrap_or("?"),
        ));
    }
    Ok(())
}

#[tauri::command]
pub async fn audio_play_url(
    app: tauri::AppHandle,
    url: String,
    bearer_token: Option<String>,
    device_name: Option<String>,
) -> Result<PlaybackStatus, String> {
    let gen = CURRENT_GEN.begin();
    off_main_thread(move || {
        let result = play_now(&app, &gen, url, bearer_token, device_name);
        CURRENT_GEN.end(&gen);
        settle(&gen, result)
    })
    .await
}

fn play_now(
    app: &tauri::AppHandle,
    gen: &Arc<AtomicBool>,
    url: String,
    bearer_token: Option<String>,
    device_name: Option<String>,
) -> Result<PlaybackStatus, String> {
    enforce_url_origin(app, &url)?;
    let device = pick_output_device(device_name.as_deref())?;
    {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if !gen.load(Ordering::Acquire) {
            engine.requested_url = Some(url.clone());
        }
    }

    // Two paths: gapless promote when we have a matching preload,
    // cold-start otherwise. Both end with current = Some(active). The
    // cold-start pipeline's stop flag is this call's generation, so a
    // newer call stops its fetch at once.
    let prepared = match take_preload_for(&url)? {
        Some(p) => p,
        None => prepare_pipeline(&url, bearer_token.as_deref(), 0, gen.clone(), true)?,
    };
    let rebuild = Rebuild {
        bearer: bearer_token,
        device_name,
    };
    install(gen, prepared, &device, url, 0, rebuild, Arc::new(AtomicBool::new(false)))
}

/// A superseded call reports the engine as it is now instead of its own
/// error (see "Last call wins").
fn settle(
    gen: &AtomicBool,
    result: Result<PlaybackStatus, String>,
) -> Result<PlaybackStatus, String> {
    match result {
        Err(e) if gen.load(Ordering::Acquire) => {
            eprintln!("audio: superseded call ended with: {e}");
            audio_state()
        }
        other => other,
    }
}

/// Swap a prepared pipeline in as `current`, unless a newer call has
/// superseded this one (then it is dropped and the engine left alone).
/// `rebuild` is what the call was made with (kept for a DoP track);
/// `paused` becomes the new output's pause flag.
fn install(
    gen: &Arc<AtomicBool>,
    prepared: PreloadSlot,
    device: &cpal::Device,
    url: String,
    start_position_ms: u64,
    rebuild: Rebuild,
    paused: Arc<AtomicBool>,
) -> Result<PlaybackStatus, String> {
    let swap = SWAP_LOCK.lock().unwrap_or_else(|e| e.into_inner());
    if gen.load(Ordering::Acquire) {
        drop(swap);
        drop(prepared);
        return audio_state();
    }
    // Release any prior active stream BEFORE opening the new one.
    // WASAPI exclusive mode holds the device — if we open the new
    // stream first, the second IAudioClient::Initialize hits
    // AUDCLNT_E_DEVICE_IN_USE and falls back to the shared path.
    // Dropping the old ActivePlayback signals its stop_flag + joins
    // the WASAPI thread, releasing the device before the new open.
    // cpal users don't notice — cpal's device handle is shared mode
    // and tolerant of the brief gap. The swap is not "atomic" from
    // the polling loop's POV (current = None for ~ms) but the
    // audio_state poller is a UI-side scrubber tick that already
    // tolerates a momentary null state.
    let old_current = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        engine.current.take()
    };
    // Drop (and thus stop the decoder) OUTSIDE the lock — see stop_audio.
    drop(old_current);

    let mut active = open_active_from_prepared(prepared, device, url, start_position_ms, paused)?;
    let is_dop = match active.dop.as_mut() {
        Some(d) => {
            d.rebuild = rebuild;
            true
        }
        None => false,
    };

    let superseded = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if CURRENT_GEN.claim(gen) {
            engine.current = Some(active);
            engine.switching = None;
            None
        } else {
            Some(active)
        }
    };
    drop(swap);
    drop(superseded);
    // The volume (or a setting) may have changed between the open's DoP
    // check and the install; nothing would switch the track then.
    if is_dop {
        leave_dop_if_needed();
    }
    audio_state()
}

/// Prepares the next track in the background so the matching
/// [`audio_play_url`] call can promote it without a fresh HTTP +
/// symphonia round-trip. Replaces any existing preload (the previous
/// one's drop signals its decoder thread to stop).
///
/// Frontend calls this whenever the upcoming track changes (queue
/// reorder, shuffle toggle, app launch with a queue restored).
/// Safe to call repeatedly with the same URL — the no-op-when-
/// already-prepared check below avoids re-fetching.
#[tauri::command]
pub async fn audio_preload_url(
    app: tauri::AppHandle,
    url: String,
    bearer_token: Option<String>,
) -> Result<(), String> {
    let gen = PRELOAD_GEN.begin();
    off_main_thread(move || {
        let result = preload_now(&app, &gen, url, bearer_token);
        PRELOAD_GEN.end(&gen);
        match result {
            Err(_) if gen.load(Ordering::Acquire) => Ok(()),
            other => other,
        }
    })
    .await
}

fn preload_now(
    app: &tauri::AppHandle,
    gen: &Arc<AtomicBool>,
    url: String,
    bearer_token: Option<String>,
) -> Result<(), String> {
    enforce_url_origin(app, &url)?;
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
    let prepared = prepare_pipeline(&url, bearer_token.as_deref(), 0, gen.clone(), true)?;
    let replaced = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if PRELOAD_GEN.claim(gen) {
            engine.preload.replace(prepared)
        } else {
            Some(prepared) // a newer preload (or a stop) took over
        }
    };
    // Stop the replaced preload's decoder OUTSIDE the lock — see stop_audio.
    drop(replaced);
    Ok(())
}

/// Seek to `position_ms` within the currently-playing track.
///
/// Implementation note: the pipeline is rebuilt at the target —
/// symphonia seeks through the container's index (FLAC SEEKTABLE,
/// MP4/WAV tables) with HTTP Range requests, falling back to
/// drink-and-discard from the start when the stream can't seek.
///
/// Errors when nothing is currently playing — seek without context
/// is a UI bug; better to surface than silently no-op.
#[tauri::command]
pub async fn audio_seek(
    app: tauri::AppHandle,
    position_ms: u64,
    bearer_token: Option<String>,
    device_name: Option<String>,
) -> Result<PlaybackStatus, String> {
    let gen = CURRENT_GEN.begin();
    off_main_thread(move || {
        let result = seek_now(&app, &gen, position_ms, bearer_token, device_name);
        CURRENT_GEN.end(&gen);
        settle(&gen, result)
    })
    .await
}

fn seek_now(
    app: &tauri::AppHandle,
    gen: &Arc<AtomicBool>,
    position_ms: u64,
    bearer_token: Option<String>,
    device_name: Option<String>,
) -> Result<PlaybackStatus, String> {
    // The track the latest play asked for: `current` is empty while that
    // play (or an earlier seek) is still fetching, and a rapid second
    // seek must still land on the same track.
    let url = {
        let engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        engine
            .requested_url
            .clone()
            .or_else(|| engine.current.as_ref().map(|p| p.source_url.clone()))
            .ok_or_else(|| "audio: nothing playing — nothing to seek".to_string())?
    };

    // Re-validate origin in case the user changed the configured
    // server URL between the original audio_play_url and this seek.
    // The original call already passed enforce_url_origin, but the
    // stored URL is now stale relative to a re-pointed server.
    enforce_url_origin(app, &url)?;

    let device = pick_output_device(device_name.as_deref())?;

    // Tear down current first. Dropping it stops the output + the
    // decoder thread releases its end of the ringbuf. The preload slot
    // is left alone — it points to the next queue track which the
    // seek doesn't affect.
    let old_current = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if gen.load(Ordering::Acquire) {
            None
        } else {
            engine.current.take()
        }
    };
    // Drop (stop the decoder) OUTSIDE the lock — see stop_audio.
    drop(old_current);

    let prepared = prepare_pipeline(&url, bearer_token.as_deref(), position_ms, gen.clone(), true)?;
    let rebuild = Rebuild {
        bearer: bearer_token,
        device_name,
    };
    install(gen, prepared, &device, url, position_ms, rebuild, Arc::new(AtomicBool::new(false)))
}

/// Removes the engine's preload slot and returns it iff its URL
/// matches `wanted`. Held briefly under the engine lock so a racing
/// preload call doesn't slip in a different track between our check
/// and our take.
fn take_preload_for(wanted: &str) -> Result<Option<PreloadSlot>, String> {
    let (matched, stale) = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if engine
            .preload
            .as_ref()
            .map(|p| p.source_url == wanted)
            .unwrap_or(false)
        {
            (engine.preload.take(), None)
        } else {
            // Stale preload (user picked a different track than we optimistically
            // prepared). Take it out here, drop it below.
            (None, engine.preload.take())
        }
    };
    // Drop the stale preload (stopping its decoder) OUTSIDE the lock — see
    // stop_audio; a stalled decoder must not wedge the lock on the play hot path.
    drop(stale);
    Ok(matched)
}

/// The name a device is listed and picked by. cpal 0.18 removed
/// `DeviceTrait::name()`; `description().name()` is its replacement and
/// on WASAPI is the same `FriendlyName` string 0.16 returned, e.g.
/// "Speakers (Realtek(R) Audio)". [`list_audio_devices`] and
/// [`pick_output_device`] must both go through this so a listed name
/// always picks the device it came from.
fn device_name(dev: &cpal::Device) -> Option<String> {
    dev.description().ok().map(|d| d.name().to_string())
}

/// Picks the output device by name, or the host default when None.
fn pick_output_device(name: Option<&str>) -> Result<cpal::Device, String> {
    let host = cpal::default_host();
    match name {
        Some(n) => host
            .output_devices()
            .map_err(|e| format!("audio: enumerate: {e}"))?
            .find(|d| device_name(d).is_some_and(|dn| dn == n))
            .ok_or_else(|| format!("audio: device not found: {n}")),
        None => host
            .default_output_device()
            .ok_or_else(|| "audio: no default output device".to_string()),
    }
}

/// What the stream's first bytes say it is. Picks the decoder path:
/// symphonia for FLAC / ALAC / WAV / AIFF (one pipeline covers the
/// full lossless catalog, and its probe identifies the codec), the DSD
/// path (dsd.rs) for DSF and DSDIFF. The URL can't decide this: the web
/// player streams `/media/stream/<file id>`, which carries no extension.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum AudioFormat {
    Symphonia, // FLAC / ALAC / WAV / AIFF / unknown — symphonia probes it
    Dsf,       // "DSD " — DSD Stream File
    Dff,       // "FRM8" — DSDIFF (uncompressed; DST is refused)
}

fn sniff_format(magic: &[u8; 4]) -> AudioFormat {
    match magic {
        b"DSD " => AudioFormat::Dsf,
        b"FRM8" => AudioFormat::Dff,
        // Anything else goes to symphonia: its probe recognises the
        // container or surfaces a clean "no suitable format reader
        // found" error the frontend falls back to `<audio>` on.
        _ => AudioFormat::Symphonia,
    }
}

/// HTTP-backed seekable source for symphonia. Implements `Read +
/// Seek + MediaSource` so symphonia's FLAC demuxer can binary-search
/// the SEEKTABLE and jump straight to the target byte offset rather
/// than scanning the stream packet-by-packet (the slow path that
/// took ~6 s on 24/192 content). Each Seek call invalidates the
/// current body and the next Read reopens with `Range: bytes=N-`.
///
/// MediaSource also wants `byte_len()` for the demuxer's binary
/// search bound — we lift it from the initial response's
/// Content-Length header. Servers serving via `http.ServeFile`
/// always set it; if it's missing we still work, just with a
/// less-tight search bound (`None` falls back to a linear scan
/// inside symphonia).
///
/// Resilience: a body read Err drops the body and the next Read
/// reopens with Range from the current offset, so a socket an
/// intermediary killed mid-track (proxy idle, NAT timeout) resumes
/// where it died. Every stream goes through it, symphonia's and the
/// DSD path's alike. A read that merely timed out (the
/// socket's short read timeout) is a stall, not an error: it is
/// retried on the same body, checking `stop` in between, until
/// STREAM_STALL_BUDGET has passed.
struct HttpSeekableSource {
    url: String,
    bearer: Option<String>,
    agent: ureq::Agent,
    file_size: Option<u64>,
    offset: u64,
    body: Option<Box<dyn Read + Send + Sync>>,
    // The pipeline's stop flag: once set, every read fails at once.
    stop: Arc<AtomicBool>,
}

impl HttpSeekableSource {
    /// Initial open — issues a GET, reads Content-Length from the
    /// headers, takes ownership of the body. Caller passes the
    /// already-built agent so timeout config stays consistent.
    fn open(
        url: String,
        bearer: Option<String>,
        agent: ureq::Agent,
        stop: Arc<AtomicBool>,
    ) -> Result<Self, String> {
        let resp = get_stream(&agent, &url, bearer.as_deref(), None, &stop)
            .map_err(|e| format!("audio: GET {url}: {e}"))?;
        if resp.status() != 200 {
            return Err(format!(
                "audio: GET {url}: HTTP {} — bearer rejected or not found",
                resp.status()
            ));
        }
        let file_size = resp
            .header("Content-Length")
            .and_then(|s| s.parse::<u64>().ok());
        let body: Box<dyn Read + Send + Sync> = Box::new(resp.into_reader());
        Ok(Self {
            url,
            bearer,
            agent,
            file_size,
            offset: 0,
            body: Some(body),
            stop,
        })
    }

    /// Reopen at `self.offset` with Range: bytes=offset-. Called by
    /// Read on Err (resilience) and by Seek when invalidating the
    /// current body before the next Read.
    fn reopen(&mut self) -> std::io::Result<()> {
        let range_from = (self.offset > 0).then_some(self.offset);
        let resp = get_stream(
            &self.agent,
            &self.url,
            self.bearer.as_deref(),
            range_from,
            &self.stop,
        )
        .map_err(|e| {
            std::io::Error::new(std::io::ErrorKind::Other, format!("Range GET: {e}"))
        })?;
        let status = resp.status();
        if !(200..300).contains(&status) {
            return Err(std::io::Error::new(
                std::io::ErrorKind::Other,
                format!("Range GET returned HTTP {status}"),
            ));
        }
        self.body = Some(Box::new(resp.into_reader()));
        Ok(())
    }
}

impl Read for HttpSeekableSource {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        // Bounded reopen budget per individual Read call. Sized for
        // a transient blip (one socket close + immediate retry +
        // one slack); a hard outage hits ureq's own connect timeout
        // and we surface that as Err rather than spinning here.
        let mut failures = 0;
        let mut stalled_since: Option<Instant> = None;
        loop {
            if self.stop.load(Ordering::Acquire) {
                return Err(stopped_error());
            }
            if self.body.is_none() {
                self.reopen()?;
            }
            let body = self.body.as_mut().unwrap();
            match body.read(buf) {
                Ok(0) => return Ok(0),
                Ok(n) => {
                    self.offset += n as u64;
                    return Ok(n);
                }
                Err(e) if is_timeout(&e) => {
                    let since = *stalled_since.get_or_insert_with(Instant::now);
                    if since.elapsed() < STREAM_STALL_BUDGET {
                        continue; // still waiting on the same body
                    }
                    eprintln!(
                        "audio: HttpSeekableSource stalled at offset {} for {:?}; reopening",
                        self.offset, STREAM_STALL_BUDGET
                    );
                    stalled_since = None;
                }
                Err(e) => {
                    eprintln!(
                        "audio: HttpSeekableSource read error at offset {}: {e}; reopening",
                        self.offset
                    );
                }
            }
            self.body = None;
            failures += 1;
            if failures >= HTTP_SEEKABLE_READ_MAX_RETRIES {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::Other,
                    "audio: HttpSeekableSource exhausted reopen retries",
                ));
            }
        }
    }
}

impl std::io::Seek for HttpSeekableSource {
    fn seek(&mut self, pos: std::io::SeekFrom) -> std::io::Result<u64> {
        let new_offset = match pos {
            std::io::SeekFrom::Start(n) => n,
            std::io::SeekFrom::End(n) => {
                let len = self.file_size.ok_or_else(|| {
                    std::io::Error::new(
                        std::io::ErrorKind::Other,
                        "audio: SeekFrom::End without Content-Length",
                    )
                })?;
                (len as i64).saturating_add(n).max(0) as u64
            }
            std::io::SeekFrom::Current(n) => {
                (self.offset as i64).saturating_add(n).max(0) as u64
            }
        };
        if new_offset != self.offset {
            // Drop the current body — the next Read reopens with a
            // Range request from the new offset. Cheap: no HTTP
            // traffic happens on Seek itself, only on the next Read.
            self.body = None;
            self.offset = new_offset;
        }
        Ok(self.offset)
    }
}

impl symphonia::core::io::MediaSource for HttpSeekableSource {
    fn is_seekable(&self) -> bool {
        true
    }
    fn byte_len(&self) -> Option<u64> {
        self.file_size
    }
}

/// Format-aware dispatcher. Every call site (cold-start in
/// `audio_play_url`, the rebuild in `audio_seek`, `audio_preload_url`)
/// goes through this. It opens the stream, reads its first four bytes
/// and routes FLAC / ALAC / WAV / AIFF through symphonia (one
/// pipeline), DSF and DSDIFF through the DSD pipeline (converted to
/// PCM, or DoP when `allow_dop` and every DoP condition holds).
///
/// `stop` becomes the pipeline's stop flag: setting it — a newer call
/// superseding this one, or the pipeline being dropped — ends every
/// read, during the prepare as well as in the decoder thread.
fn prepare_pipeline(
    url: &str,
    bearer_token: Option<&str>,
    skip_to_ms: u64,
    stop: Arc<AtomicBool>,
    allow_dop: bool,
) -> Result<PreloadSlot, String> {
    use symphonia::core::io::{
        MediaSourceStream, MediaSourceStreamOptions, ReadBytes, SeekBuffered,
    };

    // ── HTTP fetch ──────────────────────────────────────────────────────────
    // HttpSeekableSource gives symphonia a Read+Seek+MediaSource it
    // can binary-search via the FLAC seek table (and the equivalent
    // index for ALAC/WAV). This is what makes mid-track scrubbing
    // sub-200 ms instead of seconds.
    let source = HttpSeekableSource::open(
        url.to_string(),
        bearer_token.map(|s| s.to_string()),
        stream_agent(STREAM_READ_POLL),
        stop.clone(),
    )?;
    let mut stream =
        MediaSourceStream::new(Box::new(source), MediaSourceStreamOptions::default());

    // Sniff the container from the first bytes, then rewind within the
    // stream's own buffer so symphonia's probe sees them again.
    stream.ensure_seekback_buffer(4);
    let magic = stream
        .read_quad_bytes()
        .map_err(|e| format!("audio: read stream header: {e}"))?;
    stream.seek_buffered_rev(4);

    match sniff_format(&magic) {
        AudioFormat::Symphonia => prepare_symphonia_pipeline(url, stream, skip_to_ms, stop),
        AudioFormat::Dsf | AudioFormat::Dff => {
            prepare_dsd_pipeline(url, stream, skip_to_ms, stop, allow_dop)
        }
    }
}

/// Symphonia probe + spawn decoder thread for an opened stream (FLAC,
/// ALAC, WAV, AIFF). Returns a [`PreloadSlot`] the promote-to-active
/// step can open an output stream around.
///
/// Sample format dispatch:
///   - 16-bit source        → i16 ringbuf, i16 output stream
///   - everything else      → i32 ringbuf, i32 output stream (matching
///                            FLAC's 24-bit-in-32 packing)
///
/// `skip_to_ms` is the seek target: `audio_seek` rebuilds the pipeline
/// with it, and the container's index (or drink-and-discard) lands
/// there before the first sample is produced.
fn prepare_symphonia_pipeline(
    url: &str,
    stream: symphonia::core::io::MediaSourceStream<'static>,
    skip_to_ms: u64,
    stop_flag: Arc<AtomicBool>,
) -> Result<PreloadSlot, String> {
    use symphonia::core::codecs::audio::AudioDecoderOptions;
    use symphonia::core::errors::Error as SymphoniaError;
    use symphonia::core::formats::probe::Hint;
    use symphonia::core::formats::{FormatOptions, TrackType};
    use symphonia::core::meta::MetadataOptions;

    // Extension hint helps probe pick the right format on the first try
    // (otherwise ALAC inside MP4 would need a deeper magic-byte probe).
    let mut hint = Hint::new();
    let path = url.split('?').next().unwrap_or(url);
    if let Some(ext) = path.rsplit('.').next() {
        hint.with_extension(ext);
    }

    let fmt_opts = FormatOptions::default();
    let meta_opts = MetadataOptions::default();
    let mut format = symphonia::default::get_probe()
        .probe(&hint, stream, fmt_opts, meta_opts)
        .map_err(|e| format!("audio: probe format: {e}"))?;

    let track = format
        .default_track(TrackType::Audio)
        .ok_or_else(|| "audio: no default track in stream".to_string())?;
    let track_id = track.id;
    let codec_params = track
        .codec_params
        .as_ref()
        .and_then(|p| p.audio())
        .cloned()
        .ok_or_else(|| "audio: no default track in stream".to_string())?;

    // ReplayGain extraction. Symphonia exposes container metadata
    // via format.metadata().current() (a MetadataRevision). The
    // ReplayGain tags ride as user-defined `Tag` entries with the
    // REPLAYGAIN_* keys; symphonia's StandardTagKey enum doesn't
    // distinguish them so we scan by the raw tag key.
    //
    // symphonia 0.6 splits a revision into container-wide (`media`)
    // and per-track tags; 0.5 had one flat list, so read both (the
    // per-track list only for the track we decode). Keys are still
    // the raw on-disk keys.
    let mut rg_tags = ReplayGainTags::default();
    if let Some(meta) = format.metadata().current() {
        let track_tags = meta
            .per_track
            .iter()
            .filter(|t| t.track_id == u64::from(track_id))
            .flat_map(|t| t.metadata.tags.iter());
        for tag in meta.media.tags.iter().chain(track_tags) {
            ingest_replay_gain_tag(&mut rg_tags, &tag.raw.key, &tag.raw.value.to_string());
        }
    }
    let gain_factor = compute_gain_factor(&rg_tags);

    let sample_rate_hz = codec_params
        .sample_rate
        .ok_or_else(|| "audio: codec missing sample_rate".to_string())?;
    let channels_count = u16::try_from(
        codec_params
            .channels
            .as_ref()
            .ok_or_else(|| "audio: codec missing channels".to_string())?
            .count(),
    )
    .unwrap_or(u16::MAX);
    // The ringbuf and the output stream are sized from these; refuse a
    // header no real file has before anything is allocated from it.
    validate_stream_shape(sample_rate_hz, channels_count)?;
    let bit_depth = codec_params
        .bits_per_sample
        .or_else(|| alac_bit_depth(&codec_params))
        .unwrap_or(16);

    // Gapless trimming off: symphonia 0.5 left it off by default
    // (FormatOptions::enable_gapless = false) and 0.6 turned it on by
    // default (now AudioDecoderOptions::gapless). The lossless codecs
    // we decode have no encoder delay or padding to trim, and the
    // track-to-track gapless handoff is ours (preload + promote), not
    // symphonia's — so keep the 0.5 behaviour exactly.
    let mut decoder = symphonia::default::get_codecs()
        .make_audio_decoder(&codec_params, &AudioDecoderOptions::default().gapless(false))
        .map_err(|e| format!("audio: build decoder: {e}"))?;

    // ── Skip-to-position (seek path) ────────────────────────────────────────
    // True random-access seek via symphonia's seek API. For FLAC this
    // binary-searches the SEEKTABLE block to find the byte offset of
    // the packet containing the target timestamp, then HttpSeekableSource
    // satisfies the seek with a Range request. For ALAC/WAV it uses the
    // container's index. Sub-200 ms on LAN regardless of seek distance.
    if skip_to_ms > 0 {
        let seek_start = std::time::Instant::now();
        match format.seek(
            symphonia::core::formats::SeekMode::Accurate,
            symphonia::core::formats::SeekTo::Time {
                time: symphonia::core::units::Time::from_millis_u64(skip_to_ms),
                track_id: Some(track_id),
            },
        ) {
            Ok(seeked) => {
                eprintln!(
                    "audio: symphonia seek to {} ms — landed at ts {} in {:?}",
                    skip_to_ms,
                    seeked.actual_ts,
                    seek_start.elapsed()
                );
                // Reset the decoder so it doesn't apply state from the
                // pre-seek packet stream to the post-seek packets.
                decoder.reset();
            }
            Err(e) => {
                eprintln!(
                    "audio: symphonia seek to {} ms failed: {e}; falling back to drink-and-discard",
                    skip_to_ms
                );
                let mut samples_skipped: u64 = 0;
                let samples_to_skip = (skip_to_ms)
                    .saturating_mul(sample_rate_hz as u64)
                    / 1000;
                while samples_skipped < samples_to_skip {
                    if stop_flag.load(Ordering::Acquire) {
                        return Err("audio: seek cancelled".into());
                    }
                    // 0.6 reports end-of-stream as Ok(None); 0.5
                    // reported it as an IoError. Both end the skip.
                    let packet = match format.next_packet() {
                        Ok(Some(p)) => p,
                        Ok(None) | Err(SymphoniaError::IoError(_)) => break,
                        Err(e) => return Err(format!("audio: read packet during seek: {e}")),
                    };
                    if packet.track_id != track_id {
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
            }
        }
    }

    // ── Ringbuf + decoder thread ────────────────────────────────────────────
    let ring_capacity = ring_capacity(sample_rate_hz, channels_count);
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
            f32_to_i16,
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
            f32_to_i24_in_i32,
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
        dsd: None,
    })
}

/// Bit depth of an ALAC track, read from its magic cookie.
///
/// symphonia 0.5 couldn't open ALAC-in-MP4 here at all (the MP4 reader
/// left the channel layout unset, so the probe above failed with
/// "codec missing channels" and the frontend fell back to `<audio>`).
/// 0.6 fills in the channels and sample rate but still not
/// `bits_per_sample`, which would send 24-bit ALAC down the 16-bit
/// path and truncate it. The cookie carries the real depth.
fn alac_bit_depth(
    params: &symphonia::core::codecs::audio::AudioCodecParameters,
) -> Option<u32> {
    use symphonia::core::codecs::audio::well_known::CODEC_ID_ALAC;
    if params.codec != CODEC_ID_ALAC {
        return None;
    }
    let cookie = symphonia_common::apple::audio::alac::MagicCookie::read(
        params.extra_data.as_deref()?,
    )
    .ok()?;
    Some(u32::from(cookie.bit_depth))
}

/// The decoder has produced its last sample: raise `ended` once those
/// samples have actually played. Waits for the ringbuf to drain (the
/// output keeps pulling unless paused) plus END_OF_TRACK_TAIL for the
/// device's own buffer, because the frontend's auto-advance drops this
/// stream as soon as it sees `ended`. A pipeline stopped meanwhile (or
/// already stopped: the error that brought us here may be the stop
/// itself) never reports ended.
fn end_of_track<P: Observer>(ring: &P, stop: &AtomicBool, ended: &AtomicBool) {
    while !ring.is_empty() {
        if stop.load(Ordering::Acquire) {
            return;
        }
        std::thread::sleep(Duration::from_millis(5));
    }
    sleep_unless_stopped(END_OF_TRACK_TAIL, stop);
    if !stop.load(Ordering::Acquire) {
        ended.store(true, Ordering::Release);
    }
}

/// Spawn a decoder thread for a symphonia format. Pulls Packets and
/// flattens AudioBuffers to interleaved per-frame samples into the
/// ringbuf the output thread drains.
fn spawn_symphonia_decoder<T>(
    mut format: Box<dyn symphonia::core::formats::FormatReader>,
    mut decoder: Box<dyn symphonia::core::codecs::audio::AudioDecoder>,
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

    let rb = HeapRb::<T>::new(capacity);
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
                // End of stream: Ok(None) in symphonia 0.6 (0.5
                // signalled it with an IoError, still treated the
                // same way for a body that dies past the retries).
                // Every way out but a stop ends the track, so the
                // frontend's queue moves on instead of waiting forever.
                let packet = match format.next_packet() {
                    Ok(Some(p)) => p,
                    Ok(None) | Err(SymphoniaError::IoError(_)) => {
                        end_of_track(&producer, &stop_flag_dec, &ended_dec);
                        return;
                    }
                    Err(e) => {
                        eprintln!("audio: symphonia next_packet: {e}");
                        end_of_track(&producer, &stop_flag_dec, &ended_dec);
                        return;
                    }
                };
                if packet.track_id != track_id {
                    continue;
                }
                let buf = match decoder.decode(&packet) {
                    Ok(b) => b,
                    Err(SymphoniaError::DecodeError(_)) => continue, // skip bad packet
                    Err(e) => {
                        eprintln!("audio: symphonia decode: {e}");
                        end_of_track(&producer, &stop_flag_dec, &ended_dec);
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
                    // ReplayGain attenuation/boost — single f32
                    // multiply per sample (~1 ns), inlined here so
                    // the convert step picks up the gain-applied
                    // value directly.
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

// ── Sample conversion ──────────────────────────────────────────────────────
//
// Signed integer samples go to f32 and back through power-of-two scale
// factors (2^-15 and 2^15 for 16-bit, 2^-23 and 2^23 for 24-bit, 2^-31
// for 32-bit buffers), which f32 represents exactly. So with a gain of
// 1.0 (ReplayGain off or untagged) every output sample is the source
// sample, bit for bit; any other gain is rounded to the nearest step.

/// f32 in [-1, 1] → 16-bit output sample, rounded; +1.0 saturates to
/// i16::MAX (`as` saturates out-of-range floats and maps NaN to 0).
fn f32_to_i16(s: f32) -> i16 {
    (s * 32_768.0).round() as i16
}

/// f32 in [-1, 1] → a 24-bit sample in the upper 24 bits of an i32
/// with the low byte zero, the layout the outputs take for 24-bit.
fn f32_to_i24_in_i32(s: f32) -> i32 {
    let v = (s * 8_388_608.0).round().clamp(-8_388_608.0, 8_388_607.0) as i32;
    v << 8
}

/// Copy a symphonia GenericAudioBufferRef into an interleaved f32 vec.
/// Handles the planar-to-interleaved transform + the sample-format
/// normalisation in one pass. Caller is responsible for sizing
/// [`out`] to `frames * channels`. A plane the decoder didn't produce
/// (fewer planes than the codec parameters announced) stays at the
/// zero the caller filled `out` with.
fn copy_interleaved_f32(
    buf: &symphonia::core::audio::GenericAudioBufferRef<'_>,
    out: &mut [f32],
    channels: usize,
) {
    use symphonia::core::audio::{Audio, GenericAudioBufferRef};
    // `$conv` maps one sample to f32. The signed scales are powers of
    // two so the round trip through f32 is exact (see f32_to_i16).
    macro_rules! interleave {
        ($audio:expr, |$s:ident| $conv:expr) => {{
            let frames = $audio.frames();
            for ch in 0..channels {
                let Some(plane) = $audio.plane(ch) else { continue };
                for f in 0..frames {
                    let $s = plane[f];
                    out[f * channels + ch] = $conv;
                }
            }
        }};
    }
    match buf {
        GenericAudioBufferRef::F32(b) => interleave!(b, |s| s * 1.0_f32),
        GenericAudioBufferRef::S16(b) => interleave!(b, |s| s16_to_f32(s)),
        GenericAudioBufferRef::S32(b) => interleave!(b, |s| s32_to_f32(s)),
        // i24 stored in i32 with sign-extension; range is -2^23..2^23-1.
        GenericAudioBufferRef::S24(b) => interleave!(b, |s| s24_to_f32(s.inner())),
        // Unsigned PCM is centred on half its range (8-bit WAV's silence is
        // 128), so the offset comes off before scaling, as U24 already did.
        GenericAudioBufferRef::U8(b) => interleave!(b, |s| u8_to_f32(s)),
        GenericAudioBufferRef::U16(b) => interleave!(b, |s| u16_to_f32(s)),
        GenericAudioBufferRef::U24(b) => {
            interleave!(b, |s| (s.inner() as f32 - 8_388_608.0) * (1.0_f32 / 8_388_608.0))
        }
        GenericAudioBufferRef::U32(b) => interleave!(b, |s| u32_to_f32(s)),
        GenericAudioBufferRef::F64(b) => interleave!(b, |s| s as f32 * 1.0_f32),
        GenericAudioBufferRef::S8(b) => interleave!(b, |s| s as f32 * (1.0_f32 / 128.0)),
    }
}

fn s16_to_f32(s: i16) -> f32 {
    s as f32 * (1.0 / 32_768.0)
}

fn s24_to_f32(s: i32) -> f32 {
    s as f32 * (1.0 / 8_388_608.0)
}

/// 32-bit buffers: what symphonia's FLAC decoder hands over, with the
/// source sample in the top bits (a 16-bit sample is `s << 16`).
fn s32_to_f32(s: i32) -> f32 {
    s as f32 * (1.0 / 2_147_483_648.0)
}

fn u8_to_f32(s: u8) -> f32 {
    (f32::from(s) - 128.0) * (1.0 / 128.0)
}

fn u16_to_f32(s: u16) -> f32 {
    (f32::from(s) - 32_768.0) * (1.0 / 32_768.0)
}

// f64 for the subtraction: f32 can't hold every u32 near the midpoint.
fn u32_to_f32(s: u32) -> f32 {
    ((f64::from(s) - 2_147_483_648.0) * (1.0 / 2_147_483_648.0)) as f32
}

// ── DSD ────────────────────────────────────────────────────────────────────
//
// DSD (DSF / DSDIFF) plays everywhere as PCM: dsd.rs converts it to
// 24-bit PCM at twice its base rate (88.2 / 96 kHz), which goes through
// the same ringbuf and outputs as any other track, so volume, ReplayGain,
// gapless preload, seek, position and `ended` work as for FLAC. With the
// user's "DSD over DoP" setting on, and only when every DoP condition
// holds (see dsd::dop_rate and open_active_from_prepared), DSD64/128 go
// to the DAC as DoP over WASAPI exclusive instead.

/// Header, ReplayGain tags, the seek (one Range request at a computed
/// offset) and the decoder thread for a DSF or DSDIFF stream. `stream`
/// stands at the start of the file.
fn prepare_dsd_pipeline(
    url: &str,
    mut stream: symphonia::core::io::MediaSourceStream<'static>,
    skip_to_ms: u64,
    stop_flag: Arc<AtomicBool>,
    allow_dop: bool,
) -> Result<PreloadSlot, String> {
    use std::io::{Seek, SeekFrom};
    use symphonia::core::io::SeekBuffered;

    let info = crate::dsd::parse(&mut stream)?;
    let pcm_rate = info.pcm_rate_hz();
    validate_stream_shape(pcm_rate, info.channels)?;

    // ReplayGain from the TXXX frames of the file's ID3v2 tag. Only read
    // when ReplayGain is on: the tag sits after the audio, so it costs a
    // Range request (cover art in it is skipped, not downloaded).
    let mut rg_tags = ReplayGainTags::default();
    if REPLAY_GAIN_MODE.load(Ordering::Acquire) != REPLAY_GAIN_MODE_OFF {
        match crate::dsd::read_txxx(&mut stream, &info) {
            Ok(pairs) => {
                for (key, value) in pairs {
                    ingest_replay_gain_tag(&mut rg_tags, &key, &value);
                }
            }
            Err(e) => eprintln!("audio: DSD tag: {e}; playing without ReplayGain"),
        }
    }
    let gain_factor = compute_gain_factor(&rg_tags);
    let dop_rate = if allow_dop {
        crate::dsd::dop_rate(&info, &dop_request(gain_factor))
    } else {
        None
    };

    // Seek by byte offset. From the start, with no tag read, the stream
    // already stands there (no extra request); otherwise the next read
    // reopens with `Range: bytes=<offset>-`.
    let plan = crate::dsd::seek_plan(&info, skip_to_ms);
    if stream.seek_buffered(plan.byte_offset) != plan.byte_offset {
        stream
            .seek(SeekFrom::Start(plan.byte_offset))
            .map_err(|e| format!("audio: seek in DSD data: {e}"))?;
    }

    let ring_capacity = ring_capacity(dop_rate.unwrap_or(pcm_rate), info.channels);
    let ended = Arc::new(AtomicBool::new(false));
    let (mode_tx, mode_rx) = std::sync::mpsc::sync_channel(1);
    let dsd = match dop_rate {
        Some(dop_rate_hz) => Some(DsdChoice {
            dop_rate_hz,
            mode_tx,
        }),
        None => {
            // PCM only: start converting now, so a preload fills its ring.
            let _ = mode_tx.send(DsdMode::Pcm);
            None
        }
    };
    let (consumer, decoder_handle) = spawn_dsd_decoder(
        Box::new(stream),
        info,
        plan,
        mode_rx,
        ring_capacity,
        stop_flag.clone(),
        ended.clone(),
        gain_factor,
    )?;

    Ok(PreloadSlot {
        source_url: url.to_string(),
        sample_rate_hz: pcm_rate,
        bit_depth: 24,
        channels: info.channels,
        stop_flag,
        ended,
        decoder_handle: Some(decoder_handle),
        consumer: Some(PreloadConsumer::I32(consumer)),
        dsd,
    })
}

/// Pushes `samples` into the ring, waiting while it is full. False once
/// the pipeline is stopped.
fn push_all<P: Producer<Item = i32>>(producer: &mut P, samples: &[i32], stop: &AtomicBool) -> bool {
    let mut rest = samples;
    while !rest.is_empty() {
        let n = producer.push_slice(rest);
        rest = &rest[n..];
        if !rest.is_empty() {
            if stop.load(Ordering::Acquire) {
                return false;
            }
            std::thread::sleep(Duration::from_millis(2));
        }
    }
    true
}

/// The DSD decoder thread. It waits for the output's decision (sent at
/// once for a PCM-only pipeline), then reads the sound data and pushes
/// either 24-in-32 PCM (converted, ReplayGain applied) or DoP payloads.
#[allow(clippy::too_many_arguments)]
fn spawn_dsd_decoder(
    reader: Box<dyn Read + Send>,
    info: crate::dsd::DsdStream,
    plan: crate::dsd::SeekPlan,
    mode_rx: std::sync::mpsc::Receiver<DsdMode>,
    capacity: usize,
    stop_flag: Arc<AtomicBool>,
    ended: Arc<AtomicBool>,
    gain_factor: f32,
) -> Result<(<HeapRb<i32> as Split>::Cons, JoinHandle<()>), String> {
    use std::sync::mpsc::RecvTimeoutError;

    let rb = HeapRb::<i32>::new(capacity);
    let (mut producer, consumer) = rb.split();
    let handle = std::thread::Builder::new()
        .name("onscreen-dsd-decoder".into())
        .spawn(move || {
            let stop = &stop_flag;
            let mode = loop {
                if stop.load(Ordering::Acquire) {
                    return;
                }
                match mode_rx.recv_timeout(Duration::from_millis(20)) {
                    Ok(mode) => break mode,
                    Err(RecvTimeoutError::Timeout) => {}
                    // The slot was dropped before its output opened.
                    Err(RecvTimeoutError::Disconnected) => return,
                }
            };
            let channels = usize::from(info.channels);
            let mut reader = crate::dsd::DsdReader::new(reader, &info, &plan);
            let mut pcm = match mode {
                DsdMode::Pcm => Some(crate::dsd::PcmConverter::new(&info, &plan)),
                DsdMode::Dop => None,
            };
            let mut dop = match mode {
                DsdMode::Dop => Some(crate::dsd::DopPacker::new(&plan)),
                DsdMode::Pcm => None,
            };
            let gain = f64::from(gain_factor);
            let to_word = |s: &f64| f32_to_i24_in_i32((s * gain) as f32);
            let mut bytes = vec![Vec::new(); channels];
            let mut samples: Vec<f64> = Vec::new();
            let mut words: Vec<i32> = Vec::new();
            loop {
                if stop.load(Ordering::Acquire) {
                    return;
                }
                let n = match reader.read_chunk(&mut bytes) {
                    Ok(n) => n,
                    Err(_) if stop.load(Ordering::Acquire) => return,
                    Err(e) => {
                        // Every way out but a stop ends the track, so the
                        // queue moves on (as in the symphonia decoder).
                        eprintln!("audio: DSD read: {e}");
                        0
                    }
                };
                samples.clear();
                words.clear();
                if n == 0 {
                    // End of the audio: flush what the filters still hold.
                    if let Some(conv) = pcm.as_mut() {
                        conv.finish(&mut samples);
                        words.extend(samples.iter().map(to_word));
                    }
                    if let Some(packer) = dop.as_mut() {
                        packer.finish(&mut words);
                    }
                    if push_all(&mut producer, &words, stop) {
                        end_of_track(&producer, stop, &ended);
                    }
                    return;
                }
                if let Some(conv) = pcm.as_mut() {
                    conv.convert(&bytes, &mut samples);
                    words.extend(samples.iter().map(to_word));
                }
                if let Some(packer) = dop.as_mut() {
                    packer.pack(&bytes, &mut words);
                }
                if !push_all(&mut producer, &words, stop) {
                    return;
                }
            }
        })
        .map_err(|e| format!("audio: spawn DSD decoder: {e}"))?;
    Ok((consumer, handle))
}

/// Promote a prepared slot to an active playback by opening the
/// cpal output stream around its ringbuf consumer. The decoder
/// thread keeps running unchanged — it just gains a real reader.
///
/// `start_position_ms` seeds the frame counter so `audio_state`
/// reports the correct position immediately after a seek. The cpal
/// callback adds new frames on top as samples are consumed, so the
/// reported position keeps advancing from the seek target rather
/// than re-counting from zero. `paused` is the new output's pause flag.
///
/// DSD prepared while DoP was allowed (`prepared.dsd`) is decided here:
/// DoP only if the conditions still hold and WASAPI exclusive accepts
/// the DoP rate, PCM conversion in every other case — including a
/// refused DoP open, which hands the (still empty) ring back.
#[cfg_attr(target_os = "windows", allow(unused_variables, unreachable_code))]
fn open_active_from_prepared(
    mut prepared: PreloadSlot,
    device: &cpal::Device,
    url: String,
    start_position_ms: u64,
    paused: Arc<AtomicBool>,
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
        sample_rate: prepared.sample_rate_hz,
        buffer_size,
    };
    let initial_frames = start_position_ms
        .saturating_mul(prepared.sample_rate_hz as u64)
        / 1000;
    let frames_written = Arc::new(AtomicU64::new(initial_frames));

    let consumer = prepared
        .consumer
        .take()
        .ok_or_else(|| "audio: preload consumer already taken".to_string())?;
    // Its decoder waits for this decision; dropping it undecided (an
    // error below) ends the decoder.
    let dsd_choice = prepared.dsd.take();

    // Windows + exclusive flag: try WASAPI exclusive mode first. On
    // any failure (format unsupported, device busy, virtual output)
    // fall back to WASAPI shared mode with the consumer the exclusive
    // open handed back, so the user still hears audio (and the badge
    // says shared). macOS + Linux fall straight to cpal — their
    // exclusive backends (CoreAudio HOG mode / ALSA hw:) are still
    // pending, so the EXCLUSIVE_MODE flag for them stays a
    // tighter-buffer cpal hint until those modules ship.
    #[cfg(target_os = "windows")]
    {
        let mut consumer = consumer;
        // DSD over DoP: to WASAPI exclusive at the DoP rate, or not at
        // all. DoP through shared mode, the mixer or any volume is noise,
        // so a refused open (or conditions that changed since the
        // prepare) means PCM conversion, never DoP somewhere else.
        if let Some(choice) = dsd_choice {
            if dop_conditions_hold() {
                let dop_rate = choice.dop_rate_hz;
                let dop_frames = Arc::new(AtomicU64::new(
                    start_position_ms.saturating_mul(u64::from(dop_rate)) / 1000,
                ));
                let wasapi_consumer = match consumer {
                    PreloadConsumer::I16(c) => crate::windows_exclusive::WasapiConsumer::I16(c),
                    PreloadConsumer::I32(c) => crate::windows_exclusive::WasapiConsumer::I32(c),
                };
                match crate::windows_exclusive::WasapiStream::open(
                    wasapi_consumer,
                    dop_rate,
                    prepared.channels,
                    24,
                    paused.clone(),
                    dop_frames.clone(),
                    true,
                ) {
                    Ok(stream) => {
                        choice.settle(DsdMode::Dop);
                        let decoder_handle = prepared.decoder_handle.take();
                        ACTIVE_BACKEND.store(BACKEND_WASAPI_EXCLUSIVE, Ordering::Release);
                        return Ok(ActivePlayback {
                            _stream: ActiveStream::Wasapi(stream),
                            stop_flag: prepared.stop_flag.clone(),
                            paused,
                            ended: prepared.ended.clone(),
                            frames_written: dop_frames,
                            decoder_handle,
                            source_url: url,
                            sample_rate_hz: dop_rate,
                            bit_depth: 24,
                            channels: prepared.channels,
                            dop: Some(DopPlayback::default()),
                        });
                    }
                    Err(crate::windows_exclusive::OpenError {
                        reason,
                        consumer: Some(returned),
                    }) => {
                        eprintln!("{reason} (DoP at {dop_rate} Hz); converting DSD to PCM instead");
                        consumer = match returned {
                            crate::windows_exclusive::WasapiConsumer::I16(c) => PreloadConsumer::I16(c),
                            crate::windows_exclusive::WasapiConsumer::I32(c) => PreloadConsumer::I32(c),
                        };
                    }
                    Err(crate::windows_exclusive::OpenError {
                        reason,
                        consumer: None,
                    }) => {
                        return Err(format!("audio: WASAPI exclusive (DoP) open failed: {reason}"));
                    }
                }
            }
            // Nothing has been decoded yet: the PCM outputs below get a
            // ring the converter fills from here on.
            choice.settle(DsdMode::Pcm);
        }
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
                false,
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
                        dop: None,
                    });
                }
                Err(crate::windows_exclusive::OpenError {
                    reason,
                    consumer: Some(returned),
                }) => {
                    eprintln!("{reason}; falling back to WASAPI shared mode");
                    consumer = match returned {
                        crate::windows_exclusive::WasapiConsumer::I16(c) => PreloadConsumer::I16(c),
                        crate::windows_exclusive::WasapiConsumer::I32(c) => PreloadConsumer::I32(c),
                    };
                }
                Err(crate::windows_exclusive::OpenError {
                    reason,
                    consumer: None,
                }) => {
                    // The render thread kept the consumer (it hung in
                    // the driver, or panicked): nothing left to play.
                    return Err(format!("audio: WASAPI exclusive open failed: {reason}"));
                }
            }
        }

        // Non-exclusive Windows path: raw WASAPI shared with
        // AUTOCONVERTPCM. cpal's WASAPI shared backend doesn't pass
        // that flag and so refuses any rate that doesn't match the
        // device's mix-format — which is what was breaking 96/192 kHz
        // FLAC playback. The OS engine still resamples (so this isn't
        // bit-perfect — that's what the exclusive path is for) but it
        // *always* plays.
        let shared_consumer = match consumer {
            PreloadConsumer::I16(c) => crate::windows_shared::SharedConsumer::I16(c),
            PreloadConsumer::I32(c) => crate::windows_shared::SharedConsumer::I32(c),
        };
        match crate::windows_shared::WasapiSharedStream::open(
            shared_consumer,
            prepared.sample_rate_hz,
            prepared.channels,
            prepared.bit_depth,
            paused.clone(),
            frames_written.clone(),
        ) {
            Ok(stream) => {
                let decoder_handle = prepared.decoder_handle.take();
                ACTIVE_BACKEND.store(BACKEND_WASAPI_SHARED, Ordering::Release);
                return Ok(ActivePlayback {
                    _stream: ActiveStream::WasapiShared(stream),
                    stop_flag: prepared.stop_flag.clone(),
                    paused,
                    ended: prepared.ended.clone(),
                    frames_written,
                    decoder_handle,
                    source_url: url,
                    sample_rate_hz: prepared.sample_rate_hz,
                    bit_depth: prepared.bit_depth,
                    channels: prepared.channels,
                    dop: None,
                });
            }
            Err(e) => {
                return Err(format!(
                    "audio: WASAPI shared open failed: {e}"
                ));
            }
        }
    }

    // macOS / Linux: cpal. (Windows always returns above.)
    #[cfg(not(target_os = "windows"))]
    {
        // No DoP here (dsd::dop_rate never offers it off Windows).
        if let Some(choice) = dsd_choice {
            choice.settle(DsdMode::Pcm);
        }
        let stream = open_with_fallback(
            device,
            &stream_config,
            prepared.channels,
            consumer,
            paused.clone(),
            frames_written.clone(),
        )?;
        stream.play().map_err(|e| format!("audio: play: {e}"))?;
        let decoder_handle = prepared.decoder_handle.take();
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
            dop: None,
        })
    }
    #[cfg(target_os = "windows")]
    unreachable!("Windows branches above always return")
}

/// Open a cpal output stream, picking a config the device actually
/// supports. The Windows shared-mode validator rejects rates that
/// don't match the device's mix-format setting, so asking for 44.1
/// kHz on a 48 kHz-locked device fails with "stream configuration
/// not supported." This helper validates the requested config first
/// and falls back to the device's default config when the requested
/// rate isn't in the supported range.
///
/// The consumer is consumed exactly once (after the config is
/// chosen), so retry-from-scratch concerns don't apply.
#[cfg_attr(target_os = "windows", allow(dead_code))]
fn open_with_fallback(
    device: &cpal::Device,
    requested: &cpal::StreamConfig,
    channels: u16,
    consumer: PreloadConsumer,
    paused: Arc<AtomicBool>,
    frames_written: Arc<AtomicU64>,
) -> Result<cpal::Stream, String> {
    let config = pick_supported_config(device, requested);
    if config.sample_rate != requested.sample_rate {
        eprintln!(
            "audio: cpal — device doesn't support {} Hz in shared mode; \
             falling back to {} Hz (OS mixer will resample). To restore \
             bit-perfect output, set Sound Settings → Output Device → \
             Properties → Advanced → Default Format to a rate that \
             matches your music — or enable WASAPI exclusive mode.",
            requested.sample_rate, config.sample_rate,
        );
    }
    match consumer {
        PreloadConsumer::I16(cons) => open_cpal_stream::<i16>(
            cons,
            device,
            &config,
            channels,
            paused,
            frames_written,
        ),
        PreloadConsumer::I32(cons) => open_cpal_stream::<i32>(
            cons,
            device,
            &config,
            channels,
            paused,
            frames_written,
        ),
    }
}

/// Walk the device's supported_output_configs ranges and pick the best
/// match for `requested`. Returns the requested config when the device
/// supports it directly; otherwise falls back to the device's default
/// output config (which the OS mixer resamples to from the file's
/// native rate). The fallback config keeps the same buffer-size hint
/// so the EXCLUSIVE_MODE-derived "tight buffer" still applies.
#[cfg_attr(target_os = "windows", allow(dead_code))]
fn pick_supported_config(
    device: &cpal::Device,
    requested: &cpal::StreamConfig,
) -> cpal::StreamConfig {
    use cpal::traits::DeviceTrait;
    if let Ok(ranges) = device.supported_output_configs() {
        for range in ranges {
            if range.channels() != requested.channels {
                continue;
            }
            if range.min_sample_rate() <= requested.sample_rate
                && range.max_sample_rate() >= requested.sample_rate
            {
                return *requested;
            }
        }
    }
    if let Ok(default) = device.default_output_config() {
        #[cfg(target_os = "linux")]
        let sample_rate = alsa_fallback_rate(device, &default);
        #[cfg(not(target_os = "linux"))]
        let sample_rate = default.sample_rate();
        return cpal::StreamConfig {
            channels: default.channels(),
            sample_rate,
            buffer_size: requested.buffer_size,
        };
    }
    *requested
}

/// The fallback rate cpal 0.16's ALSA backend chose for its default
/// output config: 44.1 kHz when the default format's range allows it,
/// otherwise the range's maximum. cpal 0.18 prefers 48 kHz instead;
/// re-apply the 0.16 rule so the fallback negotiation is unchanged.
/// (macOS needs nothing: CoreAudio's default is still the device's
/// current rate.)
#[cfg(target_os = "linux")]
fn alsa_fallback_rate(device: &cpal::Device, default: &cpal::SupportedStreamConfig) -> u32 {
    const HZ_44100: u32 = 44_100;
    if let Ok(ranges) = device.supported_output_configs() {
        for range in ranges {
            if range.channels() == default.channels()
                && range.sample_format() == default.sample_format()
                && range.contains_rate(default.sample_rate())
            {
                return if range.contains_rate(HZ_44100) {
                    HZ_44100
                } else {
                    range.max_sample_rate()
                };
            }
        }
    }
    default.sample_rate()
}


/// Build the cpal output stream around an existing ringbuf consumer.
/// Used by both cold-start (right after `spawn_decoder`) and
/// gapless promote (the consumer was created earlier when the
/// preload was prepared, but the stream wasn't opened until now).
#[cfg_attr(target_os = "windows", allow(dead_code))]
fn open_cpal_stream<T>(
    mut consumer: <HeapRb<T> as Split>::Cons,
    device: &cpal::Device,
    config: &cpal::StreamConfig,
    channels: u16,
    paused: Arc<AtomicBool>,
    frames_written: Arc<AtomicU64>,
) -> Result<cpal::Stream, String>
where
    T: SizedSample + ScaleVolume + Send + 'static,
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
            *config,
            move |buf: &mut [T], _: &cpal::OutputCallbackInfo| {
                if paused_cb.load(Ordering::Acquire) {
                    for slot in buf.iter_mut() {
                        *slot = T::EQUILIBRIUM;
                    }
                    return;
                }
                // The slider applies here too (it used to be read only by
                // the WASAPI outputs, so on Linux it did nothing); skipped
                // at unity so 100 % stays bit-perfect.
                let vol = output_volume();
                let apply_vol = (vol - 1.0).abs() > 1e-6;
                // Whole frames only (whole_frame_samples): the rest of
                // the buffer is silence, and a half frame waits for its
                // other half instead of shifting the channels.
                let take = whole_frame_samples(consumer.occupied_len(), channels_cb, buf.len());
                let mut samples_consumed: u64 = 0;
                for (i, slot) in buf.iter_mut().enumerate() {
                    let popped = if i < take { consumer.try_pop() } else { None };
                    match popped {
                        Some(s) => {
                            *slot = if apply_vol { s.scaled(vol) } else { s };
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

    // Format dispatch from the stream's first bytes. The web player
    // streams /media/stream/<file id>, so the URL carries no extension
    // to go by; a misdetection sends a file down the wrong decoder.
    #[test]
    fn sniff_format_routes_by_magic_bytes() {
        assert_eq!(sniff_format(b"DSD "), AudioFormat::Dsf);
        assert_eq!(sniff_format(b"FRM8"), AudioFormat::Dff);
        // Everything else is symphonia's to probe (FLAC, RIFF/WAVE,
        // FORM/AIFF, MP4 atoms, an ID3v2 tag in front, garbage).
        for magic in [b"fLaC", b"RIFF", b"FORM", b"\0\0\0 ", b"ID3\x04", b"dsd ", b"NOPE"] {
            assert_eq!(sniff_format(magic), AudioFormat::Symphonia, "{magic:?}");
        }
    }

    // 24-bit ALAC must reach the 24-in-32 path: symphonia 0.6's MP4
    // reader leaves bits_per_sample unset, so the depth comes from the
    // magic cookie. A miss here truncates hi-res ALAC to 16 bits.
    #[test]
    fn alac_bit_depth_reads_the_magic_cookie() {
        use symphonia::core::codecs::audio::well_known::{CODEC_ID_ALAC, CODEC_ID_FLAC};
        use symphonia::core::codecs::audio::AudioCodecParameters;

        // ALACSpecificConfig (24 bytes, big-endian): frameLength 4096,
        // compatibleVersion 0, bitDepth 24, pb 40, mb 10, kb 14,
        // numChannels 2, maxRun 255, maxFrameBytes 0, avgBitRate 0,
        // sampleRate 96000.
        let mut cookie = Vec::new();
        cookie.extend_from_slice(&4096u32.to_be_bytes());
        cookie.extend_from_slice(&[0, 24, 40, 10, 14, 2]);
        cookie.extend_from_slice(&255u16.to_be_bytes());
        cookie.extend_from_slice(&0u32.to_be_bytes());
        cookie.extend_from_slice(&0u32.to_be_bytes());
        cookie.extend_from_slice(&96_000u32.to_be_bytes());

        let mut alac = AudioCodecParameters::new();
        alac.for_codec(CODEC_ID_ALAC).with_extra_data(cookie.clone().into_boxed_slice());
        assert_eq!(alac_bit_depth(&alac), Some(24));

        // No cookie, or not ALAC: no opinion (caller keeps its default).
        let mut bare = AudioCodecParameters::new();
        bare.for_codec(CODEC_ID_ALAC);
        assert_eq!(alac_bit_depth(&bare), None);
        let mut flac = AudioCodecParameters::new();
        flac.for_codec(CODEC_ID_FLAC).with_extra_data(cookie.into_boxed_slice());
        assert_eq!(alac_bit_depth(&flac), None);
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

    // ── Bit-exact output ───────────────────────────────────────────

    // With ReplayGain off (gain 1.0) a 16-bit source must come out as
    // the same 16-bit samples, bit for bit — from a 16-bit PCM buffer
    // (WAV / AIFF / ALAC) and from the 32-bit buffer symphonia's FLAC
    // decoder fills (sample << 16). It used to scale by 32767 and
    // truncate: +1000 came out as 999.
    #[test]
    fn sixteen_bit_round_trip_is_bit_exact_over_the_full_range() {
        let unity = compute_gain_factor_for(&ReplayGainTags::default(), REPLAY_GAIN_MODE_OFF, 0.0);
        assert_eq!(unity, 1.0);
        for s in i16::MIN..=i16::MAX {
            assert_eq!(f32_to_i16(s16_to_f32(s) * unity), s, "S16 buffer, sample {s}");
            assert_eq!(
                f32_to_i16(s32_to_f32(i32::from(s) << 16) * unity),
                s,
                "S32 buffer, sample {s}"
            );
        }
    }

    // Unsigned PCM (8-bit WAV above all) is centred on half its range:
    // silence is 128, not 0. Without the offset an 8-bit file played as a
    // DC-shifted, half-wave signal.
    // An output takes whole frames only. The decoder can leave half a
    // frame in the ring for a moment; padding the other half with
    // silence used to start every later frame on the wrong channel.
    #[test]
    fn outputs_take_whole_frames_so_channels_never_shift() {
        assert_eq!(whole_frame_samples(3, 2, 8), 2);
        assert_eq!(whole_frame_samples(9, 2, 8), 8);
        assert_eq!(whole_frame_samples(5, 6, 12), 0);
        assert_eq!(whole_frame_samples(7, 0, 8), 0);

        // Stereo, L = odd values, R = even. The ring holds L1 R1 L2 when
        // a 2-frame period comes due, then the rest arrives.
        let (mut prod, mut cons) = HeapRb::<i16>::new(16).split();
        let render = |cons: &mut <HeapRb<i16> as Split>::Cons| -> Vec<i16> {
            let take = whole_frame_samples(cons.occupied_len(), 2, 4);
            (0..4).map(|i| if i < take { cons.try_pop().unwrap_or(0) } else { 0 }).collect()
        };
        assert_eq!(prod.push_slice(&[1, 2, 3]), 3);
        assert_eq!(render(&mut cons), vec![1, 2, 0, 0]);
        assert_eq!(prod.push_slice(&[4, 5, 6]), 3);
        // L2 R2 L3 R3: left stays left.
        assert_eq!(render(&mut cons), vec![3, 4, 5, 6]);
    }

    #[test]
    fn software_volume_scales_both_integer_formats() {
        assert_eq!(20_000i16.scaled(0.5), 10_000);
        assert_eq!((-20_000i16).scaled(0.5), -10_000);
        assert_eq!(0x4000_0000i32.scaled(0.5), 0x2000_0000);
        assert_eq!(i32::MIN.scaled(0.25), i32::MIN / 4);
        assert_eq!(123i16.scaled(0.0), 0);
    }

    #[test]
    fn unsigned_pcm_is_centred_before_scaling() {
        assert_eq!(u8_to_f32(128), 0.0);
        assert_eq!(u8_to_f32(0), -1.0);
        assert_eq!(u16_to_f32(32_768), 0.0);
        assert_eq!(u16_to_f32(0), -1.0);
        assert_eq!(u32_to_f32(2_147_483_648), 0.0);
        assert_eq!(u32_to_f32(0), -1.0);
        assert!(u8_to_f32(255) > 0.99 && u8_to_f32(255) < 1.0);
        for s in 0..=u8::MAX {
            assert_eq!(f32_to_i16(u8_to_f32(s)), (i16::from(s) - 128) << 8, "U8 sample {s}");
        }
        for s in 0..=u16::MAX {
            assert_eq!(f32_to_i16(u16_to_f32(s)), (i32::from(s) - 32_768) as i16, "U16 sample {s}");
        }
    }

    #[test]
    fn twenty_four_bit_round_trip_is_bit_exact_over_the_full_range() {
        for s in -8_388_608i32..=8_388_607 {
            assert_eq!(f32_to_i24_in_i32(s24_to_f32(s)), s << 8, "S24 buffer, sample {s}");
            assert_eq!(f32_to_i24_in_i32(s32_to_f32(s << 8)), s << 8, "S32 buffer, sample {s}");
        }
    }

    #[test]
    fn conversion_saturates_at_full_scale() {
        assert_eq!(f32_to_i16(1.0), i16::MAX);
        assert_eq!(f32_to_i16(-1.0), i16::MIN);
        assert_eq!(f32_to_i16(1.5), i16::MAX);
        assert_eq!(f32_to_i16(f32::NAN), 0);
        assert_eq!(f32_to_i24_in_i32(1.0), 8_388_607 << 8);
        assert_eq!(f32_to_i24_in_i32(-1.5), -8_388_608 << 8);
    }

    // ── Stream header limits ───────────────────────────────────────

    #[test]
    fn stream_shape_is_checked_before_the_ring_is_sized() {
        assert!(validate_stream_shape(44_100, 2).is_ok());
        assert!(validate_stream_shape(8_000, 1).is_ok());
        assert!(validate_stream_shape(768_000, 8).is_ok());
        assert!(validate_stream_shape(7_999, 2).is_err());
        assert!(validate_stream_shape(768_001, 2).is_err());
        assert!(validate_stream_shape(u32::MAX, 2).is_err());
        assert!(validate_stream_shape(44_100, 0).is_err());
        assert!(validate_stream_shape(44_100, 9).is_err());
        assert!(validate_stream_shape(44_100, u16::MAX).is_err());
        // DoP: DSD256 fits a 768 kHz PCM stream, DSD512 doesn't.
        assert!(validate_stream_shape(11_289_600 / 16, 2).is_ok());
        assert!(validate_stream_shape(22_579_200 / 16, 2).is_err());
    }

    #[test]
    fn ring_capacity_is_about_200_ms_and_bounded() {
        assert_eq!(ring_capacity(48_000, 2), 19_200);
        assert_eq!(ring_capacity(8_000, 1), 8192);
        assert_eq!(ring_capacity(u32::MAX, u16::MAX), MAX_RING_SAMPLES);
        assert!(MAX_RING_SAMPLES * std::mem::size_of::<i32>() <= 5 * 1024 * 1024);
    }

    // ── End of track ───────────────────────────────────────────────

    // `ended` drives the frontend's auto-advance, which drops this
    // stream: it must wait until the buffered tail has played.
    #[test]
    fn ended_waits_for_the_ring_to_drain() {
        let (mut prod, mut cons) = HeapRb::<i16>::new(16).split();
        for i in 0..8 {
            prod.try_push(i).unwrap();
        }
        let stop = Arc::new(AtomicBool::new(false));
        let ended = Arc::new(AtomicBool::new(false));
        let waiter = {
            let (stop, ended) = (stop.clone(), ended.clone());
            std::thread::spawn(move || end_of_track(&prod, &stop, &ended))
        };
        std::thread::sleep(Duration::from_millis(100));
        assert!(!ended.load(Ordering::Acquire), "samples are still buffered");
        while cons.try_pop().is_some() {}
        waiter.join().unwrap();
        assert!(ended.load(Ordering::Acquire));
    }

    #[test]
    fn a_stopped_pipeline_never_reports_ended() {
        let stop = Arc::new(AtomicBool::new(false));
        let ended = Arc::new(AtomicBool::new(false));
        let (mut prod, _cons) = HeapRb::<i16>::new(16).split();
        prod.try_push(1).unwrap();
        let waiter = {
            let (stop, ended) = (stop.clone(), ended.clone());
            std::thread::spawn(move || end_of_track(&prod, &stop, &ended))
        };
        std::thread::sleep(Duration::from_millis(50));
        stop.store(true, Ordering::Release);
        waiter.join().unwrap();
        assert!(!ended.load(Ordering::Acquire));

        // Already stopped with nothing buffered (the read error that
        // ended the decoder was the stop itself).
        let (prod, _cons) = HeapRb::<i16>::new(16).split();
        end_of_track(&prod, &stop, &ended);
        assert!(!ended.load(Ordering::Acquire));
    }

    // ── Last call wins ─────────────────────────────────────────────

    #[test]
    fn a_newer_call_supersedes_the_one_in_flight() {
        let gen = Generation::new();
        let first = gen.begin();
        assert!(!first.load(Ordering::Acquire));
        let second = gen.begin();
        assert!(first.load(Ordering::Acquire), "the older call is superseded");
        assert!(!second.load(Ordering::Acquire));

        // A finished call is no longer superseded by later ones: its
        // flag is now the stop flag of the pipeline it installed.
        gen.end(&second);
        let third = gen.begin();
        assert!(!second.load(Ordering::Acquire));

        // A superseded call finishing late doesn't unregister the
        // newer one.
        gen.end(&first);
        let fourth = gen.begin();
        assert!(third.load(Ordering::Acquire));
        assert!(!fourth.load(Ordering::Acquire));
    }

    #[test]
    fn claiming_hands_the_flag_over_to_the_installed_pipeline() {
        let gen = Generation::new();
        let installed = gen.begin();
        assert!(gen.claim(&installed));
        // From now on the flag is the installed pipeline's stop flag: a
        // later call must not set it.
        let later = gen.begin();
        assert!(!installed.load(Ordering::Acquire));
        // A call superseded before it installs doesn't get to.
        let newest = gen.begin();
        assert!(later.load(Ordering::Acquire));
        assert!(!gen.claim(&later));
        assert!(gen.claim(&newest));
    }

    // The engine's own rebuild (a DoP track switching to PCM) must never
    // supersede a play or seek the user started.
    #[test]
    fn engine_work_waits_for_no_call_in_flight() {
        let gen = Generation::new();
        let user = gen.begin();
        assert!(gen.begin_if_idle().is_none(), "a call is in flight");
        assert!(!user.load(Ordering::Acquire));
        assert!(gen.claim(&user));
        let own = gen.begin_if_idle().expect("idle once the call installed");
        // A user call still supersedes the engine's own.
        let later = gen.begin();
        assert!(own.load(Ordering::Acquire));
        gen.end(&later);
        assert!(gen.begin_if_idle().is_some());
    }

    // ── Interruptible stream reads ─────────────────────────────────

    fn read_request_head(conn: &mut std::net::TcpStream) {
        let mut head = Vec::new();
        let mut byte = [0u8; 1];
        while !head.ends_with(b"\r\n\r\n") {
            match conn.read(&mut byte) {
                Ok(1) => head.push(byte[0]),
                _ => return,
            }
        }
    }

    /// Serves one connection: a 200 with `content_length`, then each
    /// chunk after its delay, then holds the socket open (sending
    /// nothing) until `release` is set. The listener closes after the
    /// first connection, so a reader that reconnects gets refused.
    fn serve_once(
        chunks: Vec<(Duration, &'static [u8])>,
        content_length: usize,
    ) -> (String, Arc<AtomicBool>) {
        use std::io::Write;
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/media/stream/test", listener.local_addr().unwrap());
        let release = Arc::new(AtomicBool::new(false));
        let release_srv = release.clone();
        std::thread::spawn(move || {
            let (mut conn, _) = listener.accept().unwrap();
            drop(listener);
            read_request_head(&mut conn);
            let head = format!("HTTP/1.1 200 OK\r\nContent-Length: {content_length}\r\n\r\n");
            conn.write_all(head.as_bytes()).unwrap();
            for (delay, bytes) in chunks {
                std::thread::sleep(delay);
                if conn.write_all(bytes).is_err() {
                    return;
                }
            }
            let deadline = Instant::now() + Duration::from_secs(20);
            while !release_srv.load(Ordering::Acquire) && Instant::now() < deadline {
                std::thread::sleep(Duration::from_millis(10));
            }
        });
        (url, release)
    }

    // Stop/Next must not wait on a stalled server: once the pipeline's
    // stop flag is set, a read blocked on a silent connection returns
    // within a read-poll interval instead of the old 300 s timeout.
    #[test]
    fn a_stalled_read_returns_once_the_pipeline_is_stopped() {
        let (url, release) = serve_once(vec![(Duration::ZERO, b"0123456789")], 1000);
        let stop = Arc::new(AtomicBool::new(false));
        let mut src =
            HttpSeekableSource::open(url, None, stream_agent(Duration::from_millis(100)), stop.clone())
                .unwrap();
        let mut buf = [0u8; 10];
        src.read_exact(&mut buf).unwrap();
        assert_eq!(&buf, b"0123456789");

        let stopper = {
            let stop = stop.clone();
            std::thread::spawn(move || {
                std::thread::sleep(Duration::from_millis(300));
                stop.store(true, Ordering::Release);
            })
        };
        let started = Instant::now();
        let err = src.read(&mut buf).unwrap_err();
        let waited = started.elapsed();
        stopper.join().unwrap();
        release.store(true, Ordering::Release);
        assert_eq!(err.to_string(), "audio: stream stopped");
        assert!(waited < Duration::from_secs(3), "read returned after {waited:?}");
    }

    // A stall shorter than the budget is waited out on the same
    // connection (the server accepts only one), not turned into an error.
    #[test]
    fn a_short_stall_is_waited_out_on_the_same_connection() {
        let (url, release) = serve_once(
            vec![(Duration::ZERO, b"01234"), (Duration::from_millis(400), b"56789")],
            10,
        );
        let mut src = HttpSeekableSource::open(
            url,
            None,
            stream_agent(Duration::from_millis(100)),
            Arc::new(AtomicBool::new(false)),
        )
        .unwrap();
        let mut body = Vec::new();
        src.read_to_end(&mut body).unwrap();
        release.store(true, Ordering::Release);
        assert_eq!(body, b"0123456789");
    }

    // Response headers slower than one read poll (a disk spinning up)
    // are retried rather than failing the play.
    #[test]
    fn slow_response_headers_are_retried() {
        use std::io::Write;
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/media/stream/test", listener.local_addr().unwrap());
        std::thread::spawn(move || {
            for (n, conn) in listener.incoming().take(5).enumerate() {
                let Ok(mut conn) = conn else { return };
                std::thread::spawn(move || {
                    read_request_head(&mut conn);
                    if n == 0 {
                        std::thread::sleep(Duration::from_millis(400));
                    }
                    let _ = conn.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok");
                });
            }
        });
        let stop = AtomicBool::new(false);
        let resp = get_stream(&stream_agent(Duration::from_millis(100)), &url, None, None, &stop)
            .expect("retried after the slow first response");
        assert_eq!(resp.into_string().unwrap(), "ok");
    }

    #[test]
    fn read_timeouts_are_told_apart_from_failures() {
        use std::io::{Error, ErrorKind};
        assert!(is_read_timeout(&Error::new(ErrorKind::TimedOut, "slow").into()));
        assert!(is_read_timeout(&Error::new(ErrorKind::WouldBlock, "slow").into()));
        assert!(!is_read_timeout(&Error::new(ErrorKind::ConnectionRefused, "down").into()));
        assert!(!is_read_timeout(&stopped_error().into()));
    }

    // ── DSD pipeline ───────────────────────────────────────────────

    /// Pops everything a pipeline produces until it reports `ended`.
    fn drain(cons: &mut <HeapRb<i32> as Split>::Cons, ended: &AtomicBool) -> Vec<i32> {
        let mut out = Vec::new();
        let deadline = Instant::now() + Duration::from_secs(20);
        loop {
            while let Some(s) = cons.try_pop() {
                out.push(s);
            }
            if ended.load(Ordering::Acquire) && cons.is_empty() {
                return out;
            }
            assert!(Instant::now() < deadline, "pipeline never ended");
            std::thread::sleep(Duration::from_millis(1));
        }
    }

    fn memory_stream(file: Vec<u8>) -> symphonia::core::io::MediaSourceStream<'static> {
        symphonia::core::io::MediaSourceStream::new(
            Box::new(std::io::Cursor::new(file)),
            Default::default(),
        )
    }

    // A DSD file plays as 24-bit PCM at 88.2 kHz to its last sample and
    // then reports `ended`, like any PCM file. (DoP is off by default,
    // so this is the path every DSD file takes unless the user opts in.)
    #[test]
    fn a_dsf_file_plays_as_pcm_to_the_end() {
        let rate = 2_822_400;
        let bytes = 4096 * 3 + 100;
        let bits = vec![
            crate::dsd::tests::sdm_bytes(rate, bytes * 8, 0.5, 1000.0, 0.0),
            vec![crate::dsd::DSD_IDLE_BYTE; bytes],
        ];
        let file = crate::dsd::tests::build_dsf(rate, &bits, false, None, Some(b"ID3\x04\0\0\0\0\0\x10junkjunkjunkjunk"));
        let stop = Arc::new(AtomicBool::new(false));
        let mut slot = prepare_dsd_pipeline("test", memory_stream(file), 0, stop.clone(), true).unwrap();
        assert!(slot.dsd.is_none(), "DoP is off: converted to PCM, decoding at once");
        assert_eq!((slot.sample_rate_hz, slot.bit_depth, slot.channels), (88_200, 24, 2));
        let Some(PreloadConsumer::I32(mut cons)) = slot.consumer.take() else {
            panic!("DSD goes through the 24-in-32 ring");
        };
        let out = drain(&mut cons, &slot.ended);
        assert_eq!(out.len(), 2 * bytes * 8 / 32, "exactly the track's length");
        assert!(out.iter().all(|s| s & 0xFF == 0), "24 bits in the top of 32");
        let peak = |ch: usize| out.iter().skip(ch).step_by(2).map(|s| (s >> 8).abs()).max().unwrap();
        // 50 % modulation = -6 dBFS: a peak near half of 2^23.
        assert!((peak(0) as f64 / 8_388_608.0 - 0.5).abs() < 0.01, "ch0 peak {}", peak(0));
        assert!(peak(1) <= 8, "the idle channel is silent ({})", peak(1));
        drop(slot);
    }

    // A pipeline that may go out as DoP decodes nothing until the output
    // decides, then produces DoP payloads (markers are the output's job)
    // or, refused, PCM; dropped undecided, its decoder exits.
    #[test]
    fn a_dop_capable_pipeline_waits_for_the_output() {
        let rate = 2_822_400;
        let bits = vec![vec![0xA5u8; 8192], vec![0x0Fu8; 8192]];
        let file = crate::dsd::tests::build_dsf(rate, &bits, true, None, None);
        let start = |mode: Option<DsdMode>| {
            let mut stream = memory_stream(file.clone());
            let info = crate::dsd::parse(&mut stream).unwrap();
            let plan = crate::dsd::seek_plan(&info, 0);
            let (tx, rx) = std::sync::mpsc::sync_channel(1);
            let stop = Arc::new(AtomicBool::new(false));
            let ended = Arc::new(AtomicBool::new(false));
            let (cons, handle) = spawn_dsd_decoder(
                Box::new(stream), info, plan, rx, 1 << 16, stop.clone(), ended.clone(), 0.5,
            )
            .unwrap();
            std::thread::sleep(Duration::from_millis(100));
            assert!(cons.is_empty(), "nothing before the output has decided");
            match mode {
                Some(m) => tx.send(m).unwrap(),
                None => drop(tx),
            }
            (cons, handle, ended)
        };

        let (mut cons, _h, ended) = start(Some(DsdMode::Dop));
        let words = drain(&mut cons, &ended);
        assert_eq!(words.len(), 2 * 8192 / 2);
        assert_eq!(&words[..2], &[0x00A5_A500, 0x000F_0F00], "payloads, gain never applied");

        let (mut cons, _h, ended) = start(Some(DsdMode::Pcm));
        assert_eq!(drain(&mut cons, &ended).len(), 2 * 8192 * 8 / 32);

        let (_cons, handle, ended) = start(None);
        let deadline = Instant::now() + Duration::from_secs(5);
        while !handle.is_finished() {
            assert!(Instant::now() < deadline, "decoder still waiting");
            std::thread::sleep(Duration::from_millis(5));
        }
        assert!(!ended.load(Ordering::Acquire));
    }

    /// Serves `file` to every connection, honouring `Range: bytes=N-`,
    /// and records each request's range start (None = no Range).
    fn serve_file(file: Vec<u8>) -> (String, Arc<Mutex<Vec<Option<u64>>>>) {
        use std::io::Write;
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/media/stream/dsd", listener.local_addr().unwrap());
        let ranges = Arc::new(Mutex::new(Vec::new()));
        let seen = ranges.clone();
        let file = Arc::new(file);
        std::thread::spawn(move || {
            for conn in listener.incoming().take(16) {
                let Ok(mut conn) = conn else { return };
                let (file, seen) = (file.clone(), seen.clone());
                std::thread::spawn(move || {
                    let mut head = Vec::new();
                    let mut byte = [0u8; 1];
                    while !head.ends_with(b"\r\n\r\n") {
                        match conn.read(&mut byte) {
                            Ok(1) => head.push(byte[0]),
                            _ => return,
                        }
                    }
                    let head = String::from_utf8_lossy(&head).to_ascii_lowercase();
                    let from = head
                        .lines()
                        .find_map(|l| l.strip_prefix("range: bytes="))
                        .and_then(|r| r.trim().trim_end_matches('-').parse::<u64>().ok());
                    seen.lock().unwrap().push(from);
                    let start = from.unwrap_or(0) as usize;
                    let body = &file[start.min(file.len())..];
                    let status = if from.is_some() { "206 Partial Content" } else { "200 OK" };
                    let _ = conn.write_all(
                        format!(
                            "HTTP/1.1 {status}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                            body.len()
                        )
                        .as_bytes(),
                    );
                    let _ = conn.write_all(body);
                });
            }
        });
        (url, ranges)
    }

    // Seeking a DSD file is one Range request at the computed block
    // offset, not a read through everything before it; starting from the
    // top needs no second request at all.
    #[test]
    fn a_dsd_seek_is_one_range_request_at_the_block_offset() {
        let rate = 2_822_400;
        let bytes = 4096 * 40;
        let file = crate::dsd::tests::build_dsf(
            rate,
            &[vec![crate::dsd::DSD_IDLE_BYTE; bytes], vec![crate::dsd::DSD_IDLE_BYTE; bytes]],
            false,
            None,
            None,
        );
        let info = crate::dsd::parse(&mut std::io::Cursor::new(&file)).unwrap();

        let (url, ranges) = serve_file(file.clone());
        let stop = Arc::new(AtomicBool::new(false));
        let mut slot = prepare_pipeline(&url, None, 300, stop.clone(), true).unwrap();
        let plan = crate::dsd::seek_plan(&info, 300);
        assert!(plan.byte_offset > 92 && (plan.byte_offset - 92) % (2 * 4096) == 0);
        let Some(PreloadConsumer::I32(mut cons)) = slot.consumer.take() else {
            panic!("i32 ring");
        };
        let out = drain(&mut cons, &slot.ended);
        assert_eq!(*ranges.lock().unwrap(), vec![None, Some(plan.byte_offset)]);
        let remaining = (info.samples - plan.first_sample - plan.skip_samples) / 32;
        assert!((out.len() as u64 / 2).abs_diff(remaining) <= 1, "{} vs {remaining}", out.len() / 2);
        drop(slot);

        // (Dropping the slot above set its stop flag.)
        let (url, ranges) = serve_file(file);
        let mut slot = prepare_pipeline(&url, None, 0, Arc::new(AtomicBool::new(false)), true).unwrap();
        let Some(PreloadConsumer::I32(mut cons)) = slot.consumer.take() else {
            panic!("i32 ring");
        };
        assert_eq!(drain(&mut cons, &slot.ended).len() as u64, 2 * info.samples / 32);
        assert_eq!(*ranges.lock().unwrap(), vec![None]);
    }
}

// ── Test-tone (kept from the foundation commit) ─────────────────────────────

#[tauri::command]
pub async fn play_test_tone(
    device_name: Option<String>,
    frequency_hz: f32,
    duration_ms: u32,
) -> Result<(), String> {
    // Test tone preempts FLAC playback the same way play_url does —
    // single-slot for `current`, last call wins. The preload slot is
    // also cleared so a tone press during a preloaded album doesn't
    // leave a stale next-track sitting around.
    let current_gen = CURRENT_GEN.begin();
    let preload_gen = PRELOAD_GEN.begin();
    off_main_thread(move || {
        let result = test_tone_now(&current_gen, &preload_gen, device_name, frequency_hz, duration_ms);
        CURRENT_GEN.end(&current_gen);
        PRELOAD_GEN.end(&preload_gen);
        result
    })
    .await
}

fn test_tone_now(
    current_gen: &AtomicBool,
    preload_gen: &AtomicBool,
    device_name: Option<String>,
    frequency_hz: f32,
    duration_ms: u32,
) -> Result<(), String> {
    // Like install: the old output is released before the tone opens
    // the device, and no other swap runs in between.
    let _swap = SWAP_LOCK.lock().unwrap_or_else(|e| e.into_inner());
    // Clears both slots the way a stop does (a slot a newer call has
    // taken over is left to it).
    stop_now(current_gen, preload_gen)?;

    let device = pick_output_device(device_name.as_deref())?;

    let config = device
        .default_output_config()
        .map_err(|e| format!("audio: default config: {e}"))?;
    let sample_format = config.sample_format();
    let stream_config: cpal::StreamConfig = config.into();

    let freq = frequency_hz.clamp(50.0, 5000.0);
    // Diagnostic tone: cap the duration so a single IPC call can't pin a sleeper
    // thread emitting a continuous tone for up to ~49 days (unclamped u32 ms).
    let duration_ms = duration_ms.min(10_000);
    // I32 / I24: cpal 0.18's default config ranks them above I16, so a
    // device without F32 that 0.16 opened as I16 now reports one of
    // them; without these arms the tone would fail on that device.
    let stream = match sample_format {
        SampleFormat::F32 => build_tone_stream::<f32>(&device, &stream_config, freq),
        SampleFormat::I32 => build_tone_stream::<i32>(&device, &stream_config, freq),
        SampleFormat::I24 => build_tone_stream::<cpal::I24>(&device, &stream_config, freq),
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
        sample_rate_hz: stream_config.sample_rate,
        bit_depth: 16,
        channels: stream_config.channels,
        dop: None,
    };
    let superseded = {
        let mut engine = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        if current_gen.load(Ordering::Acquire) {
            Some(active) // a newer play/seek/stop came in meanwhile
        } else {
            engine.current = Some(active);
            None
        }
    };
    drop(superseded);

    // Auto-stop on a worker thread — only the tone itself: anything
    // that replaced it in the meantime stays.
    let dur = std::time::Duration::from_millis(duration_ms as u64);
    std::thread::spawn(move || {
        std::thread::sleep(dur);
        let tone = {
            let Ok(mut engine) = ENGINE.lock() else { return };
            let still_ours = engine
                .current
                .as_ref()
                .is_some_and(|p| Arc::ptr_eq(&p.stop_flag, &stop_flag));
            if still_ours {
                engine.current.take()
            } else {
                None
            }
        };
        drop(tone);
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
    let sample_rate = config.sample_rate as f32;
    let channels = config.channels as usize;
    let mut sample_clock = 0f32;
    let stream = device
        .build_output_stream(
            *config,
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
