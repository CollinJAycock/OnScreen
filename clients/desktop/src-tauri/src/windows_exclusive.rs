// Windows WASAPI exclusive-mode backend.
//
// cpal opens IAudioClient in shared mode unconditionally, which means
// the OS audio engine resamples to its mix-format graph rate before
// the bytes reach the DAC. The audiophile pillar's bit-perfect goal
// requires AUDCLNT_SHAREMODE_EXCLUSIVE — the application owns the
// device, the OS mixer is bypassed, samples reach the DAC at the
// file's native bit-depth + rate.
//
// This module is the "raw WASAPI swap" the EXCLUSIVE_MODE flag in
// audio.rs has been plumbed for. It runs the IAudioClient on a
// dedicated thread, event-driven via SetEventHandle, and pulls samples
// from the same SPSC ringbuf the cpal path uses.
//
// Failure modes that fall back to the cpal tight-buffer path:
//   - device doesn't support the file's exact format in exclusive
//     mode (rare for FLAC/ALAC at 44.1/48 kHz, common for hi-res or
//     DSD without a high-end DAC)
//   - another exclusive-mode app already holds the device
//   - the device is a virtual / loopback device that doesn't expose
//     exclusive paths (Steam streaming, some VoIP tools)
//
// On any of those, the caller logs and re-opens through cpal so the
// user still hears audio. Bit-perfect just isn't engaged this run.
//
// Build-of-the-engine note: the ringbuf is the single contract with
// the decoder thread. The cpal path drains the same ringbuf format
// (i16 vs i32 by file bit-depth); this module dispatches on the
// PreloadConsumer enum the same way.

use ringbuf::traits::*;
use ringbuf::HeapRb;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::thread::JoinHandle;

use wasapi::{
    initialize_mta, AudioClient, Direction, SampleType, ShareMode, StreamMode, WaveFormat,
};

/// SAFETY: the wasapi event Handle uses a Windows HANDLE under the
/// hood, which is `!Send` in some bindings but is in fact safe to move
/// across threads — the OS doesn't tie it to a thread the way COM
/// objects are tied to the apartment that activated them. We construct
/// + use the handle on the same thread, so this never actually crosses
/// a boundary in practice.
///
/// The thread we spawn below initialises COM (MTA), opens the audio
/// client, registers the event handle, and runs the render loop. All
/// COM calls stay on that single thread.
pub struct WasapiStream {
    stop_flag: Arc<AtomicBool>,
    handle: Option<JoinHandle<()>>,
}

impl WasapiStream {
    /// Build the exclusive-mode stream. Returns Err on any failure;
    /// callers fall back to cpal on Err and log the reason.
    ///
    /// `consumer` carries the ringbuf reader the decoder thread
    /// produces samples into. The two variants (i16 / i32) match
    /// the bit-depth dispatch the FLAC + symphonia + DSD prepare
    /// pipelines do; we open the WASAPI WaveFormat with the
    /// matching storebits.
    pub fn open(
        consumer: WasapiConsumer,
        sample_rate_hz: u32,
        channels: u16,
        bit_depth: u32,
        paused: Arc<AtomicBool>,
        frames_written: Arc<AtomicU64>,
    ) -> Result<Self, String> {
        let stop_flag = Arc::new(AtomicBool::new(false));
        let stop_flag_clone = stop_flag.clone();
        let paused_clone = paused.clone();
        let frames_written_clone = frames_written.clone();

        let handle = std::thread::Builder::new()
            .name("onscreen-wasapi-exclusive".into())
            .spawn(move || {
                if let Err(e) = run_exclusive_loop(
                    consumer,
                    sample_rate_hz,
                    channels,
                    bit_depth,
                    paused_clone,
                    frames_written_clone,
                    stop_flag_clone,
                ) {
                    eprintln!("audio: WASAPI exclusive loop exited with error: {e}");
                }
            })
            .map_err(|e| format!("audio: spawn WASAPI exclusive thread: {e}"))?;

        Ok(WasapiStream {
            stop_flag,
            handle: Some(handle),
        })
    }
}

impl Drop for WasapiStream {
    fn drop(&mut self) {
        // Signal the render loop to exit; join keeps us blocked
        // until the thread releases the IAudioClient and uninits COM.
        // Without the join the next exclusive open would race the
        // device-busy error.
        self.stop_flag.store(true, Ordering::Release);
        if let Some(h) = self.handle.take() {
            let _ = h.join();
        }
    }
}

/// Same shape as PreloadConsumer in audio.rs but local so we don't
/// have to leak the engine type into this module.
pub enum WasapiConsumer {
    I16(<HeapRb<i16> as Split>::Cons),
    I32(<HeapRb<i32> as Split>::Cons),
}

fn run_exclusive_loop(
    mut consumer: WasapiConsumer,
    sample_rate_hz: u32,
    channels: u16,
    bit_depth: u32,
    paused: Arc<AtomicBool>,
    frames_written: Arc<AtomicU64>,
    stop_flag: Arc<AtomicBool>,
) -> Result<(), String> {
    // COM init for this thread. MTA so the WASAPI calls don't pump
    // a hidden message loop the way STA would.
    let hr = initialize_mta();
    if hr.is_err() {
        return Err(format!("audio: CoInitializeEx (MTA): {hr:?}"));
    }

    let device = wasapi::get_default_device(&Direction::Render)
        .map_err(|e| format!("audio: get default render device: {e:?}"))?;

    // BT detection — the WASAPI exclusive open succeeds against the
    // BT audio service, but the service still re-encodes samples
    // through SBC/AAC/aptX/LDAC before transmission. The badge needs
    // to reflect that bit-perfect is unattainable on this endpoint.
    let is_bt = crate::audio::device_appears_to_be_bluetooth(&device);
    crate::audio::OUTPUT_IS_BLUETOOTH.store(is_bt, Ordering::Release);

    let mut audio_client: AudioClient = device
        .get_iaudioclient()
        .map_err(|e| format!("audio: activate IAudioClient: {e:?}"))?;

    // Match WASAPI WaveFormat to the file's actual bit-depth.
    // - i16 ringbuf → 16-bit Int PCM
    // - i32 ringbuf → 24-bit-in-32 Int PCM (validbits=24, storebits=32)
    //   covers both 24-bit FLAC + 24-bit DoP samples
    let (storebits, validbits, sample_type, bytes_per_sample): (usize, usize, SampleType, usize) =
        match (&consumer, bit_depth) {
            (WasapiConsumer::I16(_), _) => (16, 16, SampleType::Int, 2),
            (WasapiConsumer::I32(_), 24) => (32, 24, SampleType::Int, 4),
            (WasapiConsumer::I32(_), _) => (32, 32, SampleType::Int, 4),
        };

    let desired = WaveFormat::new(
        storebits,
        validbits,
        &sample_type,
        sample_rate_hz as usize,
        channels as usize,
        None,
    );

    // Bail early when the device doesn't support the exact format in
    // exclusive mode. Caller catches this and falls back to cpal.
    if let Ok(maybe_alt) =
        audio_client.is_supported(&desired, &ShareMode::Exclusive)
    {
        if maybe_alt.is_some() {
            return Err(
                "audio: device doesn't support exact format in exclusive mode".into(),
            );
        }
    } else {
        return Err("audio: IsFormatSupported failed for exclusive mode".into());
    }

    // Use the device's default exclusive period as the IAudioClient
    // period — it's the shortest the driver guarantees + the typical
    // sweet spot for exclusive-mode DACs (3-10 ms).
    let (default_period_hns, _min_period_hns) = audio_client
        .get_device_period()
        .map_err(|e| format!("audio: get device period: {e:?}"))?;

    audio_client
        .initialize_client(
            &desired,
            &Direction::Render,
            &StreamMode::EventsExclusive {
                period_hns: default_period_hns,
            },
        )
        .map_err(|e| format!("audio: IAudioClient::Initialize: {e:?}"))?;

    let event_handle = audio_client
        .set_get_eventhandle()
        .map_err(|e| format!("audio: SetEventHandle: {e:?}"))?;

    let buffer_frames = audio_client
        .get_buffer_size()
        .map_err(|e| format!("audio: GetBufferSize: {e:?}"))?;

    let render_client = audio_client
        .get_audiorenderclient()
        .map_err(|e| format!("audio: GetService(IAudioRenderClient): {e:?}"))?;

    eprintln!(
        "audio: WASAPI exclusive opened — {} Hz / {} ch / {} bits / buffer {} frames",
        sample_rate_hz, channels, bit_depth, buffer_frames,
    );

    // Pre-allocated copy buffer sized for one full WASAPI frame
    // group. Avoids per-event Vec growth.
    let mut copy_buf =
        vec![0u8; (buffer_frames as usize) * (channels as usize) * bytes_per_sample];

    // Pre-fill the IAudioRenderClient buffer with silence before
    // starting the stream. WASAPI exclusive event-driven mode
    // expects the buffer primed before Start; without it, the
    // first event fires while the buffer is undefined and we get
    // either silence-forever or audible glitches on stream start.
    // Silence here is fine — by the time the first event signals
    // "give me more," the decoder thread has produced enough
    // samples that the next write carries real audio.
    let silent_flag = wasapi::BufferFlags {
        data_discontinuity: false,
        silent: true,
        timestamp_error: false,
    };
    if let Err(e) =
        render_client.write_to_device(buffer_frames as usize, &copy_buf, Some(silent_flag))
    {
        return Err(format!("audio: prefill silence: {e:?}"));
    }

    audio_client
        .start_stream()
        .map_err(|e| format!("audio: IAudioClient::Start: {e:?}"))?;

    eprintln!("audio: WASAPI exclusive stream started");

    loop {
        if stop_flag.load(Ordering::Acquire) {
            break;
        }

        // Block until WASAPI signals it can take more frames, with a
        // 2s timeout so a stuck driver doesn't strand the thread.
        // wasapi::Handle exposes WaitForSingleObject through its
        // wait_for_event helper.
        if let Err(e) = event_handle.wait_for_event(2000) {
            // Timeout or wait error — log + try again. Don't break:
            // a brief stall (USB hotplug, device reroute) shouldn't
            // tear down the playback if we can recover next tick.
            eprintln!("audio: WASAPI wait_for_event: {e:?}");
            continue;
        }

        // Number of frames available to write equals the full buffer
        // size in event-driven exclusive mode (the engine drains
        // exactly one buffer per event).
        let frames = buffer_frames as usize;
        let needed_bytes = frames * (channels as usize) * bytes_per_sample;

        if paused.load(Ordering::Acquire) {
            // Write silence to keep the render clock advancing
            // without consuming ringbuf data — same behaviour as the
            // cpal pause path.
            for b in copy_buf[..needed_bytes].iter_mut() {
                *b = 0;
            }
            if let Err(e) =
                render_client.write_to_device(frames, &copy_buf[..needed_bytes], None)
            {
                eprintln!("audio: WASAPI write (silence): {e:?}");
                break;
            }
            continue;
        }

        // Per-event volume snapshot. At unity (slider at max) the
        // multiply is skipped so exclusive-mode output stays
        // genuinely bit-perfect — the user's "audiophile" pillar is
        // only honored when the volume is at 100%.
        let vol = crate::audio::output_volume();
        let apply_vol = (vol - 1.0).abs() > 1e-6;

        // Drain the ringbuf into copy_buf one sample at a time.
        // try_pop returns None when empty; we underrun-pad with
        // silence so the DAC doesn't glitch on a transient stall
        // (network blip, decoder thread momentarily starved).
        match &mut consumer {
            WasapiConsumer::I16(cons) => {
                let total_samples = frames * (channels as usize);
                let mut byte_off = 0usize;
                for _ in 0..total_samples {
                    let s = cons.try_pop().unwrap_or(0);
                    let s = if apply_vol { ((s as f32) * vol) as i16 } else { s };
                    copy_buf[byte_off..byte_off + 2].copy_from_slice(&s.to_le_bytes());
                    byte_off += 2;
                }
            }
            WasapiConsumer::I32(cons) => {
                let total_samples = frames * (channels as usize);
                let mut byte_off = 0usize;
                for _ in 0..total_samples {
                    let s = cons.try_pop().unwrap_or(0);
                    let s = if apply_vol { ((s as f32) * vol) as i32 } else { s };
                    copy_buf[byte_off..byte_off + 4].copy_from_slice(&s.to_le_bytes());
                    byte_off += 4;
                }
            }
        }

        if let Err(e) =
            render_client.write_to_device(frames, &copy_buf[..needed_bytes], None)
        {
            eprintln!("audio: WASAPI write_to_device error (loop exits): {e:?}");
            break;
        }

        // Advance the position counter so audio_state's UI scrubber
        // ticks. Same units the cpal path uses (frames per channel).
        frames_written.fetch_add(frames as u64, Ordering::AcqRel);
    }

    eprintln!("audio: WASAPI exclusive render loop exiting (stop_flag or error)");
    let _ = audio_client.stop_stream();
    Ok(())
}
