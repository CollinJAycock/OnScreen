// Native audio engine — v2.1 Track E foundation.
//
// The audiophile pillar requires bit-perfect playback: the FLAC byte
// stream from the server hits the audio driver at its native sample
// rate + bit depth, no OS mixer in the middle. Browsers can't do
// this — `<audio>` always routes through the OS mixer (which
// resamples to the system rate). cpal exposes the platform's
// exclusive-mode backend on every desktop OS: WASAPI exclusive on
// Windows, CoreAudio on macOS (per-stream nominal-rate switching),
// ALSA `hw:` on Linux.
//
// **Scope of this commit:** the engine *foundation* — device
// enumeration + a test-tone player. Both prove cpal links and runs
// on the user's hardware before we commit to the FLAC streaming
// pipeline (claxon decoder + ringbuf between decoder thread and
// cpal's realtime callback). That pipeline lands in the next
// commit; this one keeps the surface small enough to actually test
// against real audio devices on Windows / macOS / Linux.

use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{Sample, SampleFormat, SizedSample};
use serde::Serialize;
use std::sync::Mutex;

/// Public-safe device descriptor returned by [`list_audio_devices`].
/// The Svelte settings page renders one row per entry; per-config
/// detail (sample rate range, bit depth) helps users pick an output
/// matching their library's native rates so the engine doesn't have
/// to resample on the cpal side either.
#[derive(Serialize)]
pub struct AudioDevice {
    pub name: String,
    pub is_default: bool,
    /// Human-readable summary of the device's max output config.
    /// `None` when the device exposes no output configs (an input-
    /// only device — we still surface them so the user knows why
    /// their USB DAC isn't appearing in the output list).
    pub default_output_summary: Option<String>,
}

/// Lists every audio output device the platform exposes. Marks the
/// host's default device so the UI can preselect it.
///
/// Errors are returned as String because Tauri serializes Result
/// payloads — a typed cpal error would lose its variant info on the
/// way through IPC. Callers see `"audio: ..."`-prefixed messages.
#[tauri::command]
pub fn list_audio_devices() -> Result<Vec<AudioDevice>, String> {
    let host = cpal::default_host();
    let default_name = host
        .default_output_device()
        .and_then(|d| d.name().ok());

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

// Single-stream slot. cpal streams aren't Send, so the Mutex holds
// `Option<Stream>` and the play/stop commands swap it out. A real
// transport (pause/resume/queue) goes into a separate engine struct
// in the next commit; for the foundation, "stop the current test
// tone before starting another" is the only state the UI needs.
static CURRENT_STREAM: Mutex<Option<cpal::Stream>> = Mutex::new(None);

/// Plays a sine wave on the named device for the requested duration.
/// Used by the desktop client's audio diagnostic page to verify the
/// cpal output path works end-to-end before users trust the engine
/// with their actual library.
///
/// Pass `device_name=None` (i.e. a JS `null`) to use the host's
/// default output. Frequency is clamped to [50, 5000] Hz —
/// inaudible-low wastes the tester's time, ear-piercing-high earns
/// support tickets.
#[tauri::command]
pub fn play_test_tone(
    device_name: Option<String>,
    frequency_hz: f32,
    duration_ms: u32,
) -> Result<(), String> {
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
        SampleFormat::U16 => build_tone_stream::<u16>(&device, &stream_format_u16_compat(&stream_config), freq),
        other => Err(format!("audio: unsupported sample format {other:?}")),
    }?;
    stream.play().map_err(|e| format!("audio: play: {e}"))?;

    // Hold the stream in the static so it doesn't drop (and silence)
    // the moment this function returns. The stop command and the
    // duration timer below race for the slot — last-write-wins is
    // fine here because both end states are "stream stopped."
    *CURRENT_STREAM
        .lock()
        .map_err(|_| "audio: poisoned lock".to_string())? = Some(stream);

    // Schedule auto-stop on a worker thread so the IPC call returns
    // immediately and the JS side stays responsive.
    let dur = std::time::Duration::from_millis(duration_ms as u64);
    std::thread::spawn(move || {
        std::thread::sleep(dur);
        if let Ok(mut slot) = CURRENT_STREAM.lock() {
            // Drop the stream — cpal stops the device on Drop.
            *slot = None;
        }
    });
    Ok(())
}

/// Stops any currently-playing tone (or, eventually, the live FLAC
/// stream) by dropping the held cpal::Stream.
#[tauri::command]
pub fn stop_audio() -> Result<(), String> {
    *CURRENT_STREAM
        .lock()
        .map_err(|_| "audio: poisoned lock".to_string())? = None;
    Ok(())
}

// stream_format_u16_compat exists so the U16 SampleFormat branch
// compiles on cpal 0.16 — the StreamConfig is the same type, but
// keeping the branch routed through its own helper means swapping
// in a per-format buffer-size override later (e.g. for low-latency
// configs) doesn't require touching the dispatch.
fn stream_format_u16_compat(c: &cpal::StreamConfig) -> cpal::StreamConfig {
    c.clone()
}

/// Build a sine-wave stream parameterised by sample type. Generic
/// over T:Sample so we can dispatch on the device's native format
/// without three near-identical copies.
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
                        * 0.2; // -14 dBFS — loud enough to hear, quiet enough to not blow speakers
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
