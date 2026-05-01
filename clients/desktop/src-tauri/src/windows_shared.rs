// Windows WASAPI shared-mode backend with AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM.
//
// cpal's WASAPI shared backend doesn't pass AUTOCONVERTPCM, so the
// IAudioClient init refuses any rate that doesn't match the device's
// engine mix-format ("stream configuration not supported"). That's
// what bites us when a 192 kHz file lands on a 48 kHz-default device.
// The fix is the well-known WASAPI flag pair AUTOCONVERTPCM +
// SRC_DEFAULT_QUALITY, which makes the OS audio engine insert a
// sample-rate converter + channel matrixer on the app's behalf.
//
// The wasapi crate (HEnquist) exposes both flags via
// StreamMode::EventsShared { autoconvert: true, .. }. We mirror the
// exclusive backend's shape: dedicated render thread, MTA COM,
// event-driven, drains the same SPSC ringbuf as the cpal/exclusive
// paths. Bit-perfect this is *not* — the OS still resamples — but
// it always plays, regardless of file rate vs. device default.
//
// On Windows this becomes the default shared-mode path; cpal stays
// the macOS/Linux fallback.

use ringbuf::traits::*;
use ringbuf::HeapRb;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::thread::JoinHandle;

use wasapi::{
    initialize_mta, AudioClient, Direction, SampleType, StreamMode, WaveFormat,
};

pub struct WasapiSharedStream {
    stop_flag: Arc<AtomicBool>,
    handle: Option<JoinHandle<()>>,
}

impl WasapiSharedStream {
    pub fn open(
        consumer: SharedConsumer,
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
            .name("onscreen-wasapi-shared".into())
            .spawn(move || {
                if let Err(e) = run_shared_loop(
                    consumer,
                    sample_rate_hz,
                    channels,
                    bit_depth,
                    paused_clone,
                    frames_written_clone,
                    stop_flag_clone,
                ) {
                    eprintln!("audio: WASAPI shared loop exited with error: {e}");
                }
            })
            .map_err(|e| format!("audio: spawn WASAPI shared thread: {e}"))?;

        Ok(WasapiSharedStream {
            stop_flag,
            handle: Some(handle),
        })
    }
}

impl Drop for WasapiSharedStream {
    fn drop(&mut self) {
        self.stop_flag.store(true, Ordering::Release);
        if let Some(h) = self.handle.take() {
            let _ = h.join();
        }
    }
}

pub enum SharedConsumer {
    I16(<HeapRb<i16> as Split>::Cons),
    I32(<HeapRb<i32> as Split>::Cons),
}

fn run_shared_loop(
    mut consumer: SharedConsumer,
    sample_rate_hz: u32,
    channels: u16,
    bit_depth: u32,
    paused: Arc<AtomicBool>,
    frames_written: Arc<AtomicU64>,
    stop_flag: Arc<AtomicBool>,
) -> Result<(), String> {
    let hr = initialize_mta();
    if hr.is_err() {
        return Err(format!("audio: CoInitializeEx (MTA): {hr:?}"));
    }

    let device = wasapi::get_default_device(&Direction::Render)
        .map_err(|e| format!("audio: get default render device: {e:?}"))?;

    // BT detection: the badge needs to soften "bit-perfect" to
    // "Bluetooth · lossy codec" because the OS BT audio service
    // re-encodes samples regardless of WASAPI mode. See OUTPUT_IS_BLUETOOTH.
    let is_bt = crate::audio::device_appears_to_be_bluetooth(&device);
    crate::audio::OUTPUT_IS_BLUETOOTH.store(is_bt, Ordering::Release);

    let mut audio_client: AudioClient = device
        .get_iaudioclient()
        .map_err(|e| format!("audio: activate IAudioClient: {e:?}"))?;

    // The format we *submit* to WASAPI matches the file. With
    // AUTOCONVERTPCM the engine will resample/remix to its own
    // mix-format under the hood — we never see it.
    let (storebits, validbits, sample_type, bytes_per_sample): (usize, usize, SampleType, usize) =
        match (&consumer, bit_depth) {
            (SharedConsumer::I16(_), _) => (16, 16, SampleType::Int, 2),
            (SharedConsumer::I32(_), 24) => (32, 24, SampleType::Int, 4),
            (SharedConsumer::I32(_), _) => (32, 32, SampleType::Int, 4),
        };

    let desired = WaveFormat::new(
        storebits,
        validbits,
        &sample_type,
        sample_rate_hz as usize,
        channels as usize,
        None,
    );

    // ~20 ms buffer hint. The engine picks the actual period; this is
    // the buffer-duration request. Generous enough to ride out a
    // decoder-thread stall, tight enough that pause/seek feel snappy.
    let buffer_duration_hns: i64 = 200_000;

    audio_client
        .initialize_client(
            &desired,
            &Direction::Render,
            &StreamMode::EventsShared {
                autoconvert: true,
                buffer_duration_hns,
            },
        )
        .map_err(|e| format!("audio: IAudioClient::Initialize (shared+autoconvert): {e:?}"))?;

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
        "audio: WASAPI shared+autoconvert opened — {} Hz / {} ch / {} bits / buffer {} frames",
        sample_rate_hz, channels, bit_depth, buffer_frames,
    );

    let max_bytes = (buffer_frames as usize) * (channels as usize) * bytes_per_sample;
    let mut copy_buf = vec![0u8; max_bytes];

    // Prefill silence. Same reasoning as the exclusive path: event-
    // driven mode expects the buffer primed before Start.
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

    eprintln!("audio: WASAPI shared stream started");

    // Diagnostics: per-event logging is debug-only — release builds
    // skip the formatting + write entirely (cfg!(debug_assertions) is
    // a compile-time constant the optimiser drops the dead branch on).
    // Keeps the realtime render thread quiet on shipped builds while
    // still giving full visibility during local dev.
    let mut event_count: u64 = 0;
    let mut total_underruns: u64 = 0;
    let log_per_event = cfg!(debug_assertions);

    loop {
        if stop_flag.load(Ordering::Acquire) {
            break;
        }

        if let Err(e) = event_handle.wait_for_event(2000) {
            eprintln!("audio: WASAPI(shared) wait_for_event: {e:?}");
            continue;
        }

        // In shared event-driven mode the engine drains an arbitrary
        // amount per period — query free space rather than assuming
        // a full buffer like the exclusive path does.
        let frames = match audio_client.get_available_space_in_frames() {
            Ok(f) => f as usize,
            Err(e) => {
                eprintln!("audio: WASAPI(shared) get_available_space: {e:?}");
                break;
            }
        };
        event_count += 1;
        if log_per_event && (event_count <= 5 || event_count % 500 == 0) {
            eprintln!(
                "audio: WASAPI(shared) event #{} — {} frames available (underruns so far: {})",
                event_count, frames, total_underruns
            );
        }
        if frames == 0 {
            continue;
        }
        let needed_bytes = frames * (channels as usize) * bytes_per_sample;

        if paused.load(Ordering::Acquire) {
            for b in copy_buf[..needed_bytes].iter_mut() {
                *b = 0;
            }
            if let Err(e) =
                render_client.write_to_device(frames, &copy_buf[..needed_bytes], None)
            {
                eprintln!("audio: WASAPI(shared) write (silence): {e:?}");
                break;
            }
            continue;
        }

        // Per-event volume snapshot. Reading the atomic once per
        // event is cheap (relaxed load) and gives audible-zipper-
        // free updates as the slider moves: ~20 ms granularity.
        // Skipping the multiply at unity preserves bit-perfect
        // output when the user hasn't touched the slider — the
        // sample bytes pass straight through unchanged.
        let vol = crate::audio::output_volume();
        let apply_vol = (vol - 1.0).abs() > 1e-6;

        let mut underrun_samples: usize = 0;
        match &mut consumer {
            SharedConsumer::I16(cons) => {
                let total_samples = frames * (channels as usize);
                let mut byte_off = 0usize;
                for _ in 0..total_samples {
                    let s = match cons.try_pop() {
                        Some(v) => v,
                        None => {
                            underrun_samples += 1;
                            0
                        }
                    };
                    let s = if apply_vol {
                        ((s as f32) * vol) as i16
                    } else {
                        s
                    };
                    copy_buf[byte_off..byte_off + 2].copy_from_slice(&s.to_le_bytes());
                    byte_off += 2;
                }
            }
            SharedConsumer::I32(cons) => {
                let total_samples = frames * (channels as usize);
                let mut byte_off = 0usize;
                for _ in 0..total_samples {
                    let s = match cons.try_pop() {
                        Some(v) => v,
                        None => {
                            underrun_samples += 1;
                            0
                        }
                    };
                    let s = if apply_vol {
                        ((s as f32) * vol) as i32
                    } else {
                        s
                    };
                    copy_buf[byte_off..byte_off + 4].copy_from_slice(&s.to_le_bytes());
                    byte_off += 4;
                }
            }
        }
        if underrun_samples > 0 {
            total_underruns += underrun_samples as u64;
            if log_per_event && (event_count <= 5 || event_count % 500 == 0) {
                eprintln!(
                    "audio: WASAPI(shared) event #{} — underran {}/{} samples",
                    event_count,
                    underrun_samples,
                    frames * (channels as usize)
                );
            }
        }

        if let Err(e) =
            render_client.write_to_device(frames, &copy_buf[..needed_bytes], None)
        {
            eprintln!("audio: WASAPI(shared) write_to_device error (loop exits): {e:?}");
            break;
        }

        frames_written.fetch_add(frames as u64, Ordering::AcqRel);
    }

    eprintln!("audio: WASAPI shared render loop exiting (stop_flag or error)");
    let _ = audio_client.stop_stream();
    Ok(())
}
