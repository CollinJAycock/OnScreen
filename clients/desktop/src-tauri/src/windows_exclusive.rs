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
// Failure modes that fall back to the WASAPI shared path:
//   - device doesn't support the file's exact format in exclusive
//     mode (rare for FLAC/ALAC at 44.1/48 kHz, common for hi-res or
//     DSD without a high-end DAC)
//   - another exclusive-mode app already holds the device
//   - the device is a virtual / loopback device that doesn't expose
//     exclusive paths (Steam streaming, some VoIP tools)
//
// `open` waits for the render thread to report whether the device
// accepted the stream; on any of those failures the thread hands the
// ringbuf consumer back and the caller re-opens it in shared mode, so
// the user still hears audio. Bit-perfect just isn't engaged this run
// (the active-backend badge says so).
//
// Build-of-the-engine note: the ringbuf is the single contract with
// the decoder thread. The cpal path drains the same ringbuf format
// (i16 vs i32 by file bit-depth); this module dispatches on the
// PreloadConsumer enum the same way.
//
// DoP (`dop: true`, DSD over DoP — only ever opened here, never in
// shared mode): the ring carries DoP payloads and this loop adds the
// alternating 0x05/0xFA markers frame by frame, so the sequence never
// breaks. Every frame without audio — the prefill before the first
// sample, a pause, an underrun, the end of the track, the last period
// before the stream stops — goes out as the DoP idle pattern, never as
// zeros (which a DoP DAC takes for PCM, switching mode with a click).
// DoP words are never scaled: at any volume other than 100 % the loop
// sends idle frames while the engine switches the track to PCM. An open
// at the DoP rate tries 24-bit in a 32-bit container, then packed
// 24-bit, then 32-bit; a refusal hands the ring back for PCM.

use ringbuf::traits::*;
use ringbuf::HeapRb;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::mpsc::{sync_channel, RecvTimeoutError, SyncSender};
use std::sync::Arc;
use std::thread::JoinHandle;
use std::time::Duration;

use wasapi::{
    initialize_mta, AudioClient, AudioRenderClient, Direction, Handle, SampleType, ShareMode,
    StreamMode, WaveFormat,
};

/// How long `open` waits for the render thread to report that the
/// device took the stream. Initialize + Start take milliseconds; this
/// only bounds a driver that hangs.
const OPEN_TIMEOUT: Duration = Duration::from_secs(5);

/// Consecutive 2 s event waits with no event before the render loop
/// gives up on the device. A running stream signals every period
/// (milliseconds), paused or not, so three misses means the device is
/// gone or wedged.
pub(crate) const MAX_MISSED_EVENTS: u32 = 3;

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
    // Set when the render loop stopped on its own (device unplugged or
    // invalidated, a write failed) rather than on stop_flag.
    failed: Arc<AtomicBool>,
    handle: Option<JoinHandle<()>>,
}

/// Why [`WasapiStream::open`] failed. `consumer` is handed back when
/// the render thread gave up before it pulled any samples, so the
/// caller can still play them through another backend.
pub struct OpenError {
    pub reason: String,
    pub consumer: Option<WasapiConsumer>,
}

impl WasapiStream {
    /// Build the exclusive-mode stream. Returns only once the device
    /// has accepted the format and the stream is running; on failure
    /// the caller falls back to shared mode with the returned consumer.
    ///
    /// `consumer` carries the ringbuf reader the decoder thread
    /// produces samples into. The two variants (i16 / i32) match
    /// the bit-depth dispatch the FLAC + symphonia + DSD prepare
    /// pipelines do; we open the WASAPI WaveFormat with the
    /// matching storebits. `dop`: the ring holds DoP payloads (an i32
    /// ring at the DoP rate), see the module note.
    pub fn open(
        consumer: WasapiConsumer,
        sample_rate_hz: u32,
        channels: u16,
        bit_depth: u32,
        paused: Arc<AtomicBool>,
        frames_written: Arc<AtomicU64>,
        dop: bool,
    ) -> Result<Self, OpenError> {
        let stop_flag = Arc::new(AtomicBool::new(false));
        let failed = Arc::new(AtomicBool::new(false));
        let stop_flag_clone = stop_flag.clone();
        let failed_clone = failed.clone();
        let (ready_tx, ready_rx) = sync_channel(1);

        let handle = std::thread::Builder::new()
            .name("onscreen-wasapi-exclusive".into())
            .spawn(move || {
                run_exclusive(
                    consumer,
                    sample_rate_hz,
                    channels,
                    bit_depth,
                    dop,
                    paused,
                    frames_written,
                    stop_flag_clone,
                    failed_clone,
                    ready_tx,
                )
            })
            .map_err(|e| OpenError {
                reason: format!("audio: spawn WASAPI exclusive thread: {e}"),
                consumer: None,
            })?;

        match ready_rx.recv_timeout(OPEN_TIMEOUT) {
            Ok(Ok(())) => Ok(WasapiStream {
                stop_flag,
                failed,
                handle: Some(handle),
            }),
            Ok(Err((reason, consumer))) => {
                let _ = handle.join();
                Err(OpenError {
                    reason,
                    consumer: Some(consumer),
                })
            }
            Err(RecvTimeoutError::Timeout) => {
                // The thread is stuck in a driver call and still owns the
                // consumer. Leave it to exit at its first stop check.
                stop_flag.store(true, Ordering::Release);
                Err(OpenError {
                    reason: "audio: WASAPI exclusive open timed out".into(),
                    consumer: None,
                })
            }
            Err(RecvTimeoutError::Disconnected) => {
                let _ = handle.join();
                Err(OpenError {
                    reason: "audio: WASAPI exclusive thread exited during open".into(),
                    consumer: None,
                })
            }
        }
    }

    /// True once the render loop has stopped on its own (device lost).
    pub fn failed(&self) -> bool {
        self.failed.load(Ordering::Acquire)
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

/// What the render thread holds once the device has accepted the
/// stream. All of it stays on the thread that initialised COM.
struct ExclusiveSession {
    audio_client: AudioClient,
    render_client: AudioRenderClient,
    event_handle: Handle,
    buffer_frames: u32,
    bytes_per_sample: usize,
    // DoP: the marker the first rendered frame gets, carrying on from
    // the prefill.
    dop_marker: crate::dsd::DopMarker,
}

/// Render-thread body: open the device, report the outcome to `open`
/// over `ready` (handing the consumer back on failure), then render
/// until stopped.
#[allow(clippy::too_many_arguments)]
fn run_exclusive(
    consumer: WasapiConsumer,
    sample_rate_hz: u32,
    channels: u16,
    bit_depth: u32,
    dop: bool,
    paused: Arc<AtomicBool>,
    frames_written: Arc<AtomicU64>,
    stop_flag: Arc<AtomicBool>,
    failed: Arc<AtomicBool>,
    ready: SyncSender<Result<(), (String, WasapiConsumer)>>,
) {
    // COM init for this thread. MTA so the WASAPI calls don't pump
    // a hidden message loop the way STA would.
    let hr = initialize_mta();
    if hr.is_err() {
        let _ = ready.send(Err((format!("audio: CoInitializeEx (MTA): {hr:?}"), consumer)));
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

    let is_i16 = matches!(consumer, WasapiConsumer::I16(_));
    let session = match open_exclusive(sample_rate_hz, channels, bit_depth, is_i16, dop) {
        Ok(s) => s,
        Err(e) => {
            let _ = ready.send(Err((e, consumer)));
            return;
        }
    };
    if ready.send(Ok(())).is_err() {
        // `open` already gave up on us (timed out); don't play into a
        // stream nobody owns.
        return;
    }
    drop(ready);

    let rendered = match consumer {
        WasapiConsumer::I32(cons) if dop => {
            render_dop(&session, cons, channels, &paused, &frames_written, &stop_flag)
        }
        consumer => render_exclusive(
            &session,
            consumer,
            channels,
            &paused,
            &frames_written,
            &stop_flag,
        ),
    };
    if let Err(e) = rendered {
        eprintln!("audio: WASAPI exclusive loop exited with error: {e}");
        failed.store(true, Ordering::Release);
    }
    eprintln!("audio: WASAPI exclusive render loop exiting");
    let _ = session.audio_client.stop_stream();
}

/// Open the default render device in exclusive mode at the file's
/// exact format and start the stream. Any Err here means the device
/// never took a sample, so the caller can still fall back.
fn open_exclusive(
    sample_rate_hz: u32,
    channels: u16,
    bit_depth: u32,
    is_i16: bool,
    dop: bool,
) -> Result<ExclusiveSession, String> {
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
    //
    // DoP needs its 24 bits to arrive untouched, in whichever container
    // the device takes: 24-in-32, then packed 24-bit, then 32-bit (the
    // DoP word in the top 24 bits, the low byte zero).
    let candidates: &[(usize, usize, usize)] = match (is_i16, bit_depth, dop) {
        (false, _, true) => &[(32, 24, 4), (24, 24, 3), (32, 32, 4)],
        (true, _, _) => &[(16, 16, 2)],
        (false, 24, false) => &[(32, 24, 4)],
        (false, _, false) => &[(32, 32, 4)],
    };

    // Bail early when the device doesn't support the exact format in
    // exclusive mode. Caller catches this and falls back to shared mode
    // (for DoP: to converting the DSD to PCM).
    let mut chosen = None;
    for &(storebits, validbits, bytes_per_sample) in candidates {
        let format = WaveFormat::new(
            storebits,
            validbits,
            &SampleType::Int,
            sample_rate_hz as usize,
            channels as usize,
            None,
        );
        match audio_client.is_supported(&format, &ShareMode::Exclusive) {
            Ok(None) => {
                chosen = Some((format, bytes_per_sample));
                break;
            }
            Ok(Some(_)) => {}
            Err(_) => return Err("audio: IsFormatSupported failed for exclusive mode".into()),
        }
    }
    let Some((desired, bytes_per_sample)) = chosen else {
        return Err("audio: device doesn't support exact format in exclusive mode".into());
    };

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

    // Pre-fill the IAudioRenderClient buffer with silence before
    // starting the stream. WASAPI exclusive event-driven mode
    // expects the buffer primed before Start; without it, the
    // first event fires while the buffer is undefined and we get
    // either silence-forever or audible glitches on stream start.
    // Silence here is fine — by the time the first event signals
    // "give me more," the decoder thread has produced enough
    // samples that the next write carries real audio.
    //
    // DoP primes with the DoP idle pattern instead (and without the
    // "silent" flag, which would make the engine write zeros), so the
    // DAC locks to DSD silence from the first frame.
    let mut marker = crate::dsd::DopMarker::new();
    if dop {
        let mut words = vec![0i32; (buffer_frames as usize) * (channels as usize)];
        crate::dsd::dop_period(&mut words, channels as usize, &mut marker, |_| false);
        let mut idle = vec![0u8; words.len() * bytes_per_sample];
        write_words(&words, &mut idle, bytes_per_sample);
        if let Err(e) = render_client.write_to_device(buffer_frames as usize, &idle, None) {
            return Err(format!("audio: prefill DoP idle: {e:?}"));
        }
    } else {
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
    }

    audio_client
        .start_stream()
        .map_err(|e| format!("audio: IAudioClient::Start: {e:?}"))?;

    eprintln!("audio: WASAPI exclusive stream started");
    Ok(ExclusiveSession {
        audio_client,
        render_client,
        event_handle,
        buffer_frames,
        bytes_per_sample,
        dop_marker: marker,
    })
}

/// Little-endian device bytes for 24-in-32 words: all four bytes, or
/// for packed 24-bit (3 bytes per sample) the top three.
fn write_words(words: &[i32], out: &mut [u8], bytes_per_sample: usize) {
    for (w, dst) in words.iter().zip(out.chunks_exact_mut(bytes_per_sample)) {
        dst.copy_from_slice(&w.to_le_bytes()[4 - bytes_per_sample..]);
    }
}

/// Waits up to 2 s for the device's next period; true when it came. The
/// same recovery rule as render_exclusive's wait: ride out a brief
/// stall, give up once the device is invalidated or silent.
fn wait_period(
    audio_client: &AudioClient,
    event_handle: &Handle,
    missed_events: &mut u32,
) -> Result<bool, String> {
    if let Err(e) = event_handle.wait_for_event(2000) {
        eprintln!("audio: WASAPI wait_for_event: {e:?}");
        *missed_events += 1;
        if let Err(e) = audio_client.get_current_padding() {
            return Err(format!("audio: WASAPI device lost: {e:?}"));
        }
        if *missed_events >= MAX_MISSED_EVENTS {
            return Err(format!(
                "audio: WASAPI device stopped signalling ({missed_events} missed events)"
            ));
        }
        return Ok(false);
    }
    *missed_events = 0;
    Ok(true)
}

/// The DoP render loop (see the module note): markers added here, idle
/// frames whenever there is no audio to send, words never scaled.
fn render_dop(
    session: &ExclusiveSession,
    mut consumer: <HeapRb<i32> as Split>::Cons,
    channels: u16,
    paused: &AtomicBool,
    frames_written: &AtomicU64,
    stop_flag: &AtomicBool,
) -> Result<(), String> {
    let channels = usize::from(channels.max(1));
    let frames = session.buffer_frames as usize;
    let bytes_per_sample = session.bytes_per_sample;
    let mut marker = session.dop_marker;
    let mut words = vec![0i32; frames * channels];
    let mut bytes = vec![0u8; frames * channels * bytes_per_sample];
    let mut missed_events = 0u32;

    loop {
        let stopping = stop_flag.load(Ordering::Acquire);
        if !wait_period(&session.audio_client, &session.event_handle, &mut missed_events)? {
            if stopping {
                return Ok(());
            }
            continue;
        }
        // Stopping: one last period of idle, so the DAC's last DoP
        // frames are DSD silence rather than the middle of the music.
        let play = !stopping && !paused.load(Ordering::Acquire);
        // DoP words can't be scaled; anything but 100 % is silence
        // until the engine has switched the track to PCM.
        let unity = crate::audio::output_volume() == 1.0;
        let mut played = 0u64;
        crate::dsd::dop_period(&mut words, channels, &mut marker, |frame| {
            // Whole frames only, so channels never shift.
            if !play || consumer.occupied_len() < frame.len() {
                return false;
            }
            for w in frame.iter_mut() {
                *w = consumer.try_pop().unwrap_or(crate::dsd::DOP_IDLE_PAYLOAD);
            }
            played += 1;
            unity
        });
        write_words(&words, &mut bytes, bytes_per_sample);
        session
            .render_client
            .write_to_device(frames, &bytes, None)
            .map_err(|e| format!("audio: WASAPI write_to_device (DoP): {e:?}"))?;
        // Frames taken from the ring advance the position, also while
        // they go out as idle at a non-unity volume.
        frames_written.fetch_add(played, Ordering::AcqRel);
        if stopping {
            return Ok(());
        }
    }
}

/// Pull from the ringbuf into the device until `stop_flag` is set
/// (Ok) or the device goes away (Err).
fn render_exclusive(
    session: &ExclusiveSession,
    mut consumer: WasapiConsumer,
    channels: u16,
    paused: &AtomicBool,
    frames_written: &AtomicU64,
    stop_flag: &AtomicBool,
) -> Result<(), String> {
    let ExclusiveSession {
        audio_client,
        render_client,
        event_handle,
        buffer_frames,
        bytes_per_sample,
        ..
    } = session;
    let bytes_per_sample = *bytes_per_sample;

    // Pre-allocated copy buffer sized for one full WASAPI frame
    // group. Avoids per-event Vec growth.
    let mut copy_buf =
        vec![0u8; (*buffer_frames as usize) * (channels as usize) * bytes_per_sample];
    let mut missed_events = 0u32;

    loop {
        if stop_flag.load(Ordering::Acquire) {
            return Ok(());
        }

        // Block until WASAPI signals it can take more frames, with a
        // 2s timeout so a stuck driver doesn't strand the thread.
        // wasapi::Handle exposes WaitForSingleObject through its
        // wait_for_event helper.
        if let Err(e) = event_handle.wait_for_event(2000) {
            // A brief stall (USB hotplug, device reroute) can recover,
            // so keep going — unless the device is gone (the client
            // reports AUDCLNT_E_DEVICE_INVALIDATED) or it has stopped
            // signalling altogether.
            eprintln!("audio: WASAPI wait_for_event: {e:?}");
            missed_events += 1;
            if let Err(e) = audio_client.get_current_padding() {
                return Err(format!("audio: WASAPI device lost: {e:?}"));
            }
            if missed_events >= MAX_MISSED_EVENTS {
                return Err(format!(
                    "audio: WASAPI device stopped signalling ({missed_events} missed events)"
                ));
            }
            continue;
        }
        missed_events = 0;

        // Number of frames available to write equals the full buffer
        // size in event-driven exclusive mode (the engine drains
        // exactly one buffer per event).
        let frames = *buffer_frames as usize;
        let needed_bytes = frames * (channels as usize) * bytes_per_sample;

        if paused.load(Ordering::Acquire) {
            // Write silence to keep the render clock advancing
            // without consuming ringbuf data — same behaviour as the
            // cpal pause path.
            for b in copy_buf[..needed_bytes].iter_mut() {
                *b = 0;
            }
            render_client
                .write_to_device(frames, &copy_buf[..needed_bytes], None)
                .map_err(|e| format!("audio: WASAPI write (silence): {e:?}"))?;
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
        let total_samples = frames * (channels as usize);
        let mut played_samples = 0usize;
        match &mut consumer {
            WasapiConsumer::I16(cons) => {
                let mut byte_off = 0usize;
                for _ in 0..total_samples {
                    let s = match cons.try_pop() {
                        Some(v) => {
                            played_samples += 1;
                            v
                        }
                        None => 0,
                    };
                    let s = if apply_vol { ((s as f32) * vol) as i16 } else { s };
                    copy_buf[byte_off..byte_off + 2].copy_from_slice(&s.to_le_bytes());
                    byte_off += 2;
                }
            }
            WasapiConsumer::I32(cons) => {
                let mut byte_off = 0usize;
                for _ in 0..total_samples {
                    let s = match cons.try_pop() {
                        Some(v) => {
                            played_samples += 1;
                            v
                        }
                        None => 0,
                    };
                    let s = if apply_vol { ((s as f32) * vol) as i32 } else { s };
                    copy_buf[byte_off..byte_off + 4].copy_from_slice(&s.to_le_bytes());
                    byte_off += 4;
                }
            }
        }

        render_client
            .write_to_device(frames, &copy_buf[..needed_bytes], None)
            .map_err(|e| format!("audio: WASAPI write_to_device: {e:?}"))?;

        // Advance the position counter so audio_state's UI scrubber
        // ticks. Same units and rule as the cpal path: frames that
        // carried real samples, not underrun padding, so the position
        // freezes while the decoder is starved instead of running on.
        frames_written.fetch_add((played_samples / channels.max(1) as usize) as u64, Ordering::AcqRel);
    }
}
