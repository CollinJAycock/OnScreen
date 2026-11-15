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
use std::sync::mpsc::{sync_channel, RecvTimeoutError, SyncSender};
use std::sync::Arc;
use std::thread::JoinHandle;
use std::time::Duration;

use wasapi::{
    initialize_mta, AudioClient, AudioRenderClient, Direction, Handle, SampleType, StreamMode,
    WaveFormat,
};

/// How long `open` waits for the render thread to report that the
/// device took the stream (see the exclusive backend).
const OPEN_TIMEOUT: Duration = Duration::from_secs(5);

pub struct WasapiSharedStream {
    stop_flag: Arc<AtomicBool>,
    // Set when the render loop stopped on its own (device lost).
    failed: Arc<AtomicBool>,
    handle: Option<JoinHandle<()>>,
}

impl WasapiSharedStream {
    /// Build the shared-mode stream. Returns only once the device has
    /// accepted the stream and it is running, so a refusal surfaces as
    /// an Err the frontend can fall back on instead of silence at 0:00.
    pub fn open(
        consumer: SharedConsumer,
        sample_rate_hz: u32,
        channels: u16,
        bit_depth: u32,
        paused: Arc<AtomicBool>,
        frames_written: Arc<AtomicU64>,
    ) -> Result<Self, String> {
        let stop_flag = Arc::new(AtomicBool::new(false));
        let failed = Arc::new(AtomicBool::new(false));
        let stop_flag_clone = stop_flag.clone();
        let failed_clone = failed.clone();
        let (ready_tx, ready_rx) = sync_channel(1);

        let handle = std::thread::Builder::new()
            .name("onscreen-wasapi-shared".into())
            .spawn(move || {
                run_shared(
                    consumer,
                    sample_rate_hz,
                    channels,
                    bit_depth,
                    paused,
                    frames_written,
                    stop_flag_clone,
                    failed_clone,
                    ready_tx,
                )
            })
            .map_err(|e| format!("audio: spawn WASAPI shared thread: {e}"))?;

        match ready_rx.recv_timeout(OPEN_TIMEOUT) {
            Ok(Ok(())) => Ok(WasapiSharedStream {
                stop_flag,
                failed,
                handle: Some(handle),
            }),
            Ok(Err(reason)) => {
                let _ = handle.join();
                Err(reason)
            }
            Err(RecvTimeoutError::Timeout) => {
                // Stuck in a driver call; leave the thread to exit at its
                // first stop check.
                stop_flag.store(true, Ordering::Release);
                Err("audio: WASAPI shared open timed out".into())
            }
            Err(RecvTimeoutError::Disconnected) => {
                let _ = handle.join();
                Err("audio: WASAPI shared thread exited during open".into())
            }
        }
    }

    /// True once the render loop has stopped on its own (device lost).
    pub fn failed(&self) -> bool {
        self.failed.load(Ordering::Acquire)
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

/// What the render thread holds once the device has accepted the
/// stream. All of it stays on the thread that initialised COM.
struct SharedSession {
    audio_client: AudioClient,
    render_client: AudioRenderClient,
    event_handle: Handle,
    buffer_frames: u32,
    bytes_per_sample: usize,
}

/// Render-thread body: open the device, report the outcome to `open`
/// over `ready`, then render until stopped.
#[allow(clippy::too_many_arguments)]
fn run_shared(
    consumer: SharedConsumer,
    sample_rate_hz: u32,
    channels: u16,
    bit_depth: u32,
    paused: Arc<AtomicBool>,
    frames_written: Arc<AtomicU64>,
    stop_flag: Arc<AtomicBool>,
    failed: Arc<AtomicBool>,
    ready: SyncSender<Result<(), String>>,
) {
    let hr = initialize_mta();
    if hr.is_err() {
        let _ = ready.send(Err(format!("audio: CoInitializeEx (MTA): {hr:?}")));
        return;
    }
    // Balance the per-thread COM init on EVERY exit path so
    // CoInitializeEx isn't left unmatched. Declared before the session
    // so it drops after it: the COM objects are released first.
    struct ComGuard;
    impl Drop for ComGuard {
        fn drop(&mut self) {
            wasapi::deinitialize();
        }
    }
    let _com_guard = ComGuard;

    let is_i16 = matches!(consumer, SharedConsumer::I16(_));
    let session = match open_shared(sample_rate_hz, channels, bit_depth, is_i16) {
        Ok(s) => s,
        Err(e) => {
            let _ = ready.send(Err(e));
            return;
        }
    };
    if ready.send(Ok(())).is_err() {
        // `open` already gave up on us (timed out).
        return;
    }
    drop(ready);

    if let Err(e) = render_shared(
        &session,
        consumer,
        channels,
        &paused,
        &frames_written,
        &stop_flag,
    ) {
        eprintln!("audio: WASAPI shared loop exited with error: {e}");
        failed.store(true, Ordering::Release);
    }
    eprintln!("audio: WASAPI shared render loop exiting");
    let _ = session.audio_client.stop_stream();
}

/// Open the default render device in shared mode with AUTOCONVERTPCM
/// at the file's format and start the stream.
fn open_shared(
    sample_rate_hz: u32,
    channels: u16,
    bit_depth: u32,
    is_i16: bool,
) -> Result<SharedSession, String> {
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
        match (is_i16, bit_depth) {
            (true, _) => (16, 16, SampleType::Int, 2),
            (false, 24) => (32, 24, SampleType::Int, 4),
            (false, _) => (32, 32, SampleType::Int, 4),
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

    // Prefill silence. Same reasoning as the exclusive path: event-
    // driven mode expects the buffer primed before Start.
    let silence = vec![0u8; (buffer_frames as usize) * (channels as usize) * bytes_per_sample];
    let silent_flag = wasapi::BufferFlags {
        data_discontinuity: false,
        silent: true,
        timestamp_error: false,
    };
    if let Err(e) =
        render_client.write_to_device(buffer_frames as usize, &silence, Some(silent_flag))
    {
        return Err(format!("audio: prefill silence: {e:?}"));
    }

    audio_client
        .start_stream()
        .map_err(|e| format!("audio: IAudioClient::Start: {e:?}"))?;

    eprintln!("audio: WASAPI shared stream started");
    Ok(SharedSession {
        audio_client,
        render_client,
        event_handle,
        buffer_frames,
        bytes_per_sample,
    })
}

/// Pull from the ringbuf into the device until `stop_flag` is set
/// (Ok) or the device goes away (Err).
fn render_shared(
    session: &SharedSession,
    mut consumer: SharedConsumer,
    channels: u16,
    paused: &AtomicBool,
    frames_written: &AtomicU64,
    stop_flag: &AtomicBool,
) -> Result<(), String> {
    let SharedSession {
        audio_client,
        render_client,
        event_handle,
        buffer_frames,
        bytes_per_sample,
    } = session;
    let bytes_per_sample = *bytes_per_sample;

    let max_bytes = (*buffer_frames as usize) * (channels as usize) * bytes_per_sample;
    let mut copy_buf = vec![0u8; max_bytes];

    // Diagnostics: per-event logging is debug-only — release builds
    // skip the formatting + write entirely (cfg!(debug_assertions) is
    // a compile-time constant the optimiser drops the dead branch on).
    // Keeps the realtime render thread quiet on shipped builds while
    // still giving full visibility during local dev.
    let mut event_count: u64 = 0;
    let mut total_underruns: u64 = 0;
    let log_per_event = cfg!(debug_assertions);
    let mut missed_events = 0u32;

    loop {
        if stop_flag.load(Ordering::Acquire) {
            return Ok(());
        }

        if let Err(e) = event_handle.wait_for_event(2000) {
            // Same recovery rule as the exclusive loop: ride out a brief
            // stall, give up once the device is invalidated or silent.
            eprintln!("audio: WASAPI(shared) wait_for_event: {e:?}");
            missed_events += 1;
            if let Err(e) = audio_client.get_current_padding() {
                return Err(format!("audio: WASAPI(shared) device lost: {e:?}"));
            }
            if missed_events >= crate::windows_exclusive::MAX_MISSED_EVENTS {
                return Err(format!(
                    "audio: WASAPI(shared) device stopped signalling ({missed_events} missed events)"
                ));
            }
            continue;
        }
        missed_events = 0;

        // In shared event-driven mode the engine drains an arbitrary
        // amount per period — query free space rather than assuming
        // a full buffer like the exclusive path does.
        let frames = audio_client
            .get_available_space_in_frames()
            .map_err(|e| format!("audio: WASAPI(shared) get_available_space: {e:?}"))?
            as usize;
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
            render_client
                .write_to_device(frames, &copy_buf[..needed_bytes], None)
                .map_err(|e| format!("audio: WASAPI(shared) write (silence): {e:?}"))?;
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

        // Whole frames only (audio::whole_frame_samples): padding half a
        // frame with silence shifted every later frame onto the wrong
        // channel.
        let total_samples = frames * (channels as usize);
        let mut underrun_samples: usize = 0;
        match &mut consumer {
            SharedConsumer::I16(cons) => {
                let take = crate::audio::whole_frame_samples(cons.occupied_len(), channels as usize, total_samples);
                let mut byte_off = 0usize;
                for i in 0..total_samples {
                    let popped = if i < take { cons.try_pop() } else { None };
                    let s = match popped {
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
                let take = crate::audio::whole_frame_samples(cons.occupied_len(), channels as usize, total_samples);
                let mut byte_off = 0usize;
                for i in 0..total_samples {
                    let popped = if i < take { cons.try_pop() } else { None };
                    let s = match popped {
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
                    event_count, underrun_samples, total_samples
                );
            }
        }

        render_client
            .write_to_device(frames, &copy_buf[..needed_bytes], None)
            .map_err(|e| format!("audio: WASAPI(shared) write_to_device: {e:?}"))?;

        // Frames that carried real samples, not underrun padding — the
        // cpal path's rule, so the position freezes while the decoder is
        // starved instead of running on.
        let played = (total_samples - underrun_samples) / channels.max(1) as usize;
        frames_written.fetch_add(played as u64, Ordering::AcqRel);
    }
}
