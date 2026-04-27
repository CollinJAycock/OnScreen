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
use std::sync::atomic::{AtomicBool, Ordering};
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
#[derive(Serialize)]
pub struct PlaybackStatus {
    pub playing: bool,
    pub paused: bool,
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

/// Single-slot engine. Subsequent commits add a queue + crossfade;
/// for the streaming foundation, "stop the previous track when a new
/// play() arrives" is the simplest model and matches the existing
/// dual-`<audio>` rotation in the web player conceptually.
static ENGINE: Mutex<Option<ActivePlayback>> = Mutex::new(None);

#[tauri::command]
pub fn audio_state() -> Result<PlaybackStatus, String> {
    let engine = ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())?;
    Ok(match &*engine {
        Some(p) => PlaybackStatus {
            playing: true,
            paused: p.paused.load(Ordering::Acquire),
            source_url: Some(p.source_url.clone()),
            sample_rate_hz: Some(p.sample_rate_hz),
            bit_depth: Some(p.bit_depth),
            channels: Some(p.channels),
        },
        None => PlaybackStatus {
            playing: false,
            paused: false,
            source_url: None,
            sample_rate_hz: None,
            bit_depth: None,
            channels: None,
        },
    })
}

#[tauri::command]
pub fn stop_audio() -> Result<(), String> {
    *ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())? = None;
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
    if let Some(p) = engine.as_ref() {
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
    if let Some(p) = engine.as_ref() {
        p.paused.store(false, Ordering::Release);
    }
    Ok(())
}

// ── FLAC streaming play_url ─────────────────────────────────────────────────

/// Streams a FLAC file from `url` (carrying `Authorization: Bearer
/// <bearer_token>` when supplied) and plays it on the named device.
/// Replaces any currently-playing track — the caller doesn't need
/// to call `stop_audio` first.
///
/// Returns when playback has *started* (the cpal stream is running
/// and the decoder thread is producing samples). Errors out
/// synchronously on the parts that can fail before audio starts:
/// device pick, FLAC header parse, cpal config build.
///
/// FLAC only in this commit. Other formats (MP3, ALAC, raw PCM) and
/// transcoded sources fall through to the existing `<audio>` element
/// in the webview.
#[tauri::command]
pub fn audio_play_url(
    url: String,
    bearer_token: Option<String>,
    device_name: Option<String>,
) -> Result<PlaybackStatus, String> {
    // Stop any existing playback first so the old decoder thread
    // releases its ringbuf producer before we build a new one. The
    // assignment-to-None drops the previous ActivePlayback which
    // signals stop and joins.
    {
        let mut slot = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        *slot = None;
    }

    // ── HTTP fetch ──────────────────────────────────────────────────────────
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout_read(std::time::Duration::from_secs(30))
        .build();
    let mut req = agent.get(&url);
    if let Some(tok) = &bearer_token {
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
    // Box the body to a known type so build_flac_pipeline doesn't
    // need to be generic over ureq's concrete reader type. Send +
    // 'static is enough — only the decoder thread reads, so Sync
    // isn't required.
    let body: Box<dyn Read + Send + 'static> = Box::new(resp.into_reader());

    // ── FLAC header probe ───────────────────────────────────────────────────
    // claxon's FlacReader needs a Read; the streaming body is one.
    // The first read parses the STREAMINFO block (sample rate, bit
    // depth, channels) — exactly the values we need to open cpal at
    // the file's native config.
    let reader = claxon::FlacReader::new(body)
        .map_err(|e| format!("audio: parse FLAC: {e}"))?;
    let info = reader.streaminfo();
    let sample_rate_hz = info.sample_rate;
    let bit_depth = info.bits_per_sample;
    let channels = info.channels as u16;

    // ── Pick output device ──────────────────────────────────────────────────
    let host = cpal::default_host();
    let device = match &device_name {
        Some(name) => host
            .output_devices()
            .map_err(|e| format!("audio: enumerate: {e}"))?
            .find(|d| d.name().map(|n| n == *name).unwrap_or(false))
            .ok_or_else(|| format!("audio: device not found: {name}"))?,
        None => host
            .default_output_device()
            .ok_or_else(|| "audio: no default output device".to_string())?,
    };

    // Find a supported config matching the FLAC's rate + channels.
    // We hold the bit_depth / sample_format choice for cpal: 16-bit
    // → I16, anything ≥17 → I32 (carrying 24-bit-in-32). Default-
    // mode cpal lets us request a specific rate; the OS mixer picks
    // up the slack if the device doesn't natively support it. The
    // exclusive-mode toggle (next commit) is what enforces "no
    // resampling, ever."
    let stream_config = cpal::StreamConfig {
        channels,
        sample_rate: cpal::SampleRate(sample_rate_hz),
        buffer_size: cpal::BufferSize::Default,
    };

    // Ringbuf sized for ~200 ms of audio at the file's rate. Tight
    // enough that latency from "play" to first sample is sub-second;
    // loose enough that brief disk/network hiccups don't underrun.
    let ring_capacity = (sample_rate_hz as usize)
        .saturating_mul(channels as usize)
        / 5; // 0.2 s
    let stop_flag = Arc::new(AtomicBool::new(false));
    let paused = Arc::new(AtomicBool::new(false));

    // ── Build the stream + decoder thread, dispatched on bit depth ──────────
    let (stream, decoder_handle) = if bit_depth <= 16 {
        build_flac_pipeline::<i16>(
            reader,
            &device,
            &stream_config,
            ring_capacity,
            stop_flag.clone(),
            paused.clone(),
            |s| s as i16,
        )?
    } else {
        build_flac_pipeline::<i32>(
            reader,
            &device,
            &stream_config,
            ring_capacity,
            stop_flag.clone(),
            paused.clone(),
            // 24-bit-in-32: shift left so the high byte holds the MSB.
            // cpal expects "24 bits packed as 32" with the data in the
            // upper 24 bits and the low 8 zero on most hosts.
            |s| s.saturating_mul(256),
        )?
    };

    stream.play().map_err(|e| format!("audio: play: {e}"))?;

    let active = ActivePlayback {
        _stream: stream,
        stop_flag,
        paused,
        decoder_handle: Some(decoder_handle),
        source_url: url,
        sample_rate_hz,
        bit_depth,
        channels,
    };
    *ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())? = Some(active);

    audio_state()
}

/// Wires the decoder thread + cpal stream around a typed sample
/// representation. T is the cpal sample type (I16 for 16-bit FLAC,
/// I32 for ≥17-bit), `convert_sample` maps claxon's i32 sample to
/// the target type (identity-style for I32, narrow for I16 — claxon
/// always emits 32-bit-wide samples regardless of the source bit
/// depth).
fn build_flac_pipeline<T>(
    reader: claxon::FlacReader<Box<dyn Read + Send + 'static>>,
    device: &cpal::Device,
    config: &cpal::StreamConfig,
    capacity: usize,
    stop_flag: Arc<AtomicBool>,
    paused: Arc<AtomicBool>,
    convert_sample: fn(i32) -> T,
) -> Result<(cpal::Stream, JoinHandle<()>), String>
where
    T: SizedSample + Send + 'static,
{
    let rb = HeapRb::<T>::new(capacity.max(8192));
    let (mut producer, mut consumer) = rb.split();

    // Decoder thread. Pushes one FLAC frame's samples at a time;
    // sleeps when the ring is full so the cpal callback can drain.
    // Exits cleanly when the stream ends (claxon iterator returns
    // None) or when stop_flag is raised.
    let stop_flag_dec = stop_flag.clone();
    let decoder_handle = std::thread::Builder::new()
        .name("onscreen-flac-decoder".into())
        .spawn(move || {
            // Re-bind as mut so reader.samples() (which takes &mut
            // self) is allowed — function-param mut would also work
            // but shadowing here keeps the closure's intent local.
            let mut reader = reader;
            let mut samples = reader.samples();
            loop {
                if stop_flag_dec.load(Ordering::Acquire) {
                    return;
                }
                match samples.next() {
                    Some(Ok(s)) => {
                        let mut sample = convert_sample(s);
                        // Push with backoff: ringbuf returns the value back
                        // when full — yield-spin until there's room. A
                        // condvar would be cleaner but adds sync overhead
                        // the callback would also have to honour.
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
                    None => return, // EOS
                }
            }
        })
        .map_err(|e| format!("audio: spawn decoder thread: {e}"))?;

    // cpal output callback. Realtime — must not block, allocate, or
    // call into Tauri/anything that takes a mutex. ringbuf's pop
    // is wait-free; a buffer underrun (decoder behind) writes
    // silence rather than stalling the device. When paused, we
    // skip the ringbuf entirely so the decoder's natural backpressure
    // freezes its output until resume — no extra CPU during a pause,
    // and no risk of the buffer draining while the user paused for
    // long enough that decoder didn't get scheduled.
    let paused_cb = paused.clone();
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
                for slot in buf.iter_mut() {
                    *slot = consumer
                        .try_pop()
                        .unwrap_or(T::EQUILIBRIUM);
                }
            },
            |err| eprintln!("audio: stream error: {err}"),
            None,
        )
        .map_err(|e| format!("audio: build stream: {e}"))?;
    Ok((stream, decoder_handle))
}

// ── Test-tone (kept from the foundation commit) ─────────────────────────────

#[tauri::command]
pub fn play_test_tone(
    device_name: Option<String>,
    frequency_hz: f32,
    duration_ms: u32,
) -> Result<(), String> {
    // Test tone preempts FLAC playback the same way play_url does —
    // single-slot engine, last call wins. No need to special-case.
    {
        let mut slot = ENGINE
            .lock()
            .map_err(|_| "audio: poisoned engine lock".to_string())?;
        *slot = None;
    }

    let host = cpal::default_host();
    let device = match device_name {
        Some(name) => host
            .output_devices()
            .map_err(|e| format!("audio: enumerate: {e}"))?
            .find(|d| d.name().map(|n| n == name).unwrap_or(false))
            .ok_or_else(|| format!("audio: device not found: {name}"))?,
        None => host
            .default_output_device()
            .ok_or_else(|| "audio: no default output device".to_string())?,
    };

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
    // minimal ActivePlayback with no decoder thread. The paused
    // flag is kept for symmetry with the FLAC path even though the
    // tone generator doesn't honor it — pause on a 2-second test
    // tone makes no sense and the auto-stop fires anyway.
    let stop_flag = Arc::new(AtomicBool::new(false));
    let paused = Arc::new(AtomicBool::new(false));
    let active = ActivePlayback {
        _stream: stream,
        stop_flag: stop_flag.clone(),
        paused,
        decoder_handle: None,
        source_url: format!("tone:{freq}Hz"),
        sample_rate_hz: stream_config.sample_rate.0,
        bit_depth: 16,
        channels: stream_config.channels,
    };
    *ENGINE
        .lock()
        .map_err(|_| "audio: poisoned engine lock".to_string())? = Some(active);

    // Auto-stop on a worker thread.
    let dur = std::time::Duration::from_millis(duration_ms as u64);
    std::thread::spawn(move || {
        std::thread::sleep(dur);
        if stop_flag.load(Ordering::Acquire) {
            return; // already stopped by some other call
        }
        if let Ok(mut slot) = ENGINE.lock() {
            *slot = None;
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
