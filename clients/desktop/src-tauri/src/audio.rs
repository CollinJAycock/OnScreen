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
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;

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

struct ActivePlayback {
    // The cpal::Stream owns the realtime callback. Drop = stream
    // stops + device released. Stays alive as long as this struct
    // does — we hold it in the engine's Mutex.
    _stream: cpal::Stream,
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
}

fn detect_format(url: &str) -> AudioFormat {
    // Strip query string before the extension check so a `?token=`
    // suffix doesn't make every URL look like ".jpg?token=xyz".
    let path = url.split('?').next().unwrap_or(url);
    let lower = path.to_ascii_lowercase();
    if lower.ends_with(".m4a")
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
                    let mut converted = convert_sample(*sample);
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
    // support it. The exclusive-mode toggle (future commit) is
    // what enforces "no resampling, ever."
    let stream_config = cpal::StreamConfig {
        channels: prepared.channels,
        sample_rate: cpal::SampleRate(prepared.sample_rate_hz),
        buffer_size: cpal::BufferSize::Default,
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

    Ok(ActivePlayback {
        _stream: stream,
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
) -> Result<(<HeapRb<T> as Split>::Cons, JoinHandle<()>), String>
where
    T: Send + 'static + Default + Copy,
{
    let rb = HeapRb::<T>::new(capacity.max(8192));
    let (mut producer, consumer) = rb.split();

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
                        let mut sample = convert_sample(s);
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
        _stream: stream,
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
