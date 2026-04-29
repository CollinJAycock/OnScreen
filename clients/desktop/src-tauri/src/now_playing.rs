// OS now-playing widget — Windows SMTC / macOS MPNowPlayingInfoCenter /
// Linux MPRIS via souvlaki. Exposes track metadata + playback state to
// the system shell so the lockscreen overlay, taskbar pop-out, Bluetooth
// headset display, Apple Watch, etc. all light up with the currently-
// playing OnScreen track.
//
// Two complementary inputs:
//   1. tauri-plugin-global-shortcut already binds the keyboard media
//      keys (Play/Pause, Next, Previous) — that handles a USB keyboard
//      with media keys when the OnScreen window has focus or is
//      backgrounded. Souvlaki picks up the *system-level* control
//      surface (lockscreen, Bluetooth headset buttons, watch
//      complications) which doesn't go through the keyboard layer.
//   2. The frontend pushes track metadata + playback state via the
//      Tauri commands below whenever a track starts, ends, pauses,
//      resumes, or seeks. Souvlaki re-publishes that to the OS.
//
// Both source events flow into the same `media-key` Tauri event the
// frontend already listens for, so the AudioPlayer's existing handler
// covers both paths without a second listener.
//
// Failure mode: souvlaki initialisation can fail (no D-Bus on a
// minimal Linux container, no HWND yet on Windows during a race). All
// commands degrade gracefully — the OS widget just doesn't show, but
// playback itself is unaffected. Keyboard media keys via the global
// shortcut plugin keep working.

use serde::Deserialize;
use souvlaki::{MediaControlEvent, MediaControls, MediaMetadata, MediaPlayback, MediaPosition, PlatformConfig};
use std::sync::Mutex;
use std::time::Duration;
use tauri::{AppHandle, Emitter, Manager, Runtime, State};

/// Process-wide MediaControls instance. Wrapped in Mutex so the
/// frontend-driven commands and the souvlaki callback (which fires on
/// its own thread) can both touch it. The Option layer covers the
/// "souvlaki failed to init" case — commands no-op rather than
/// panicking when controls couldn't be built (rare on Windows/macOS,
/// common on headless Linux without a session bus).
pub struct NowPlayingState(pub Mutex<Option<MediaControls>>);

impl Default for NowPlayingState {
    fn default() -> Self {
        Self(Mutex::new(None))
    }
}

/// Build MediaControls + attach the event handler that re-emits OS
/// transport events into the same `media-key` Tauri event the
/// frontend already listens to. Called once during the Tauri setup
/// hook, after the main window exists (Windows needs its HWND).
pub fn init<R: Runtime>(app: &AppHandle<R>) -> Result<(), String> {
    let hwnd = platform_hwnd(app);
    let config = PlatformConfig {
        // dbus_name doubles as the bus name on Linux. Lowercase
        // alphanumeric per spec.
        dbus_name: "onscreen",
        display_name: "OnScreen",
        hwnd,
    };

    let mut controls = MediaControls::new(config)
        .map_err(|e| format!("souvlaki: build MediaControls: {e:?}"))?;

    // Forward souvlaki control events into the same `media-key` event
    // tauri-plugin-global-shortcut emits. The frontend AudioPlayer
    // already maps that event to play / pause / next / previous /
    // stop, so souvlaki rides for free.
    let app_clone = app.clone();
    controls
        .attach(move |event| {
            if let Some(action) = action_for(event) {
                let _ = app_clone.emit("media-key", action);
            }
        })
        .map_err(|e| format!("souvlaki: attach handler: {e:?}"))?;

    if let Some(state) = app.try_state::<NowPlayingState>() {
        if let Ok(mut slot) = state.0.lock() {
            *slot = Some(controls);
        }
    }
    Ok(())
}

/// Map a souvlaki control event to the same string the keyboard
/// shortcut plugin emits. Unhandled variants drop silently — Stop
/// folds into the same "stop" action the global shortcut uses;
/// Quit / Raise / OpenUri are out of scope.
fn action_for(event: MediaControlEvent) -> Option<&'static str> {
    match event {
        MediaControlEvent::Play => Some("play-pause"),
        MediaControlEvent::Pause => Some("play-pause"),
        MediaControlEvent::Toggle => Some("play-pause"),
        MediaControlEvent::Next => Some("next"),
        MediaControlEvent::Previous => Some("previous"),
        MediaControlEvent::Stop => Some("stop"),
        _ => None,
    }
}

/// Track metadata pushed by the frontend on every track change. art_url
/// is an absolute https:// URL the OS shell can fetch — souvlaki hands
/// the URL straight to the platform widget; the widget decodes off the
/// main thread, so a slow art request doesn't stall playback.
#[derive(Deserialize)]
pub struct NowPlayingMetadata {
    pub title: String,
    pub artist: Option<String>,
    pub album: Option<String>,
    pub art_url: Option<String>,
    pub duration_ms: Option<u64>,
}

#[tauri::command]
pub fn now_playing_set_metadata(
    state: State<'_, NowPlayingState>,
    meta: NowPlayingMetadata,
) -> Result<(), String> {
    let Ok(mut slot) = state.0.lock() else {
        return Ok(());
    };
    let Some(controls) = slot.as_mut() else {
        return Ok(());
    };
    controls
        .set_metadata(MediaMetadata {
            title: Some(&meta.title),
            artist: meta.artist.as_deref(),
            album: meta.album.as_deref(),
            cover_url: meta.art_url.as_deref(),
            duration: meta.duration_ms.map(Duration::from_millis),
        })
        .map_err(|e| format!("souvlaki: set_metadata: {e:?}"))
}

/// Frontend pushes this on every play / pause / seek / stop. souvlaki
/// re-publishes to the OS; the lockscreen widget updates in place.
#[derive(Deserialize)]
pub struct NowPlayingPlayback {
    pub state: String,
    pub position_ms: Option<u64>,
}

#[tauri::command]
pub fn now_playing_set_playback(
    state: State<'_, NowPlayingState>,
    playback: NowPlayingPlayback,
) -> Result<(), String> {
    let Ok(mut slot) = state.0.lock() else {
        return Ok(());
    };
    let Some(controls) = slot.as_mut() else {
        return Ok(());
    };
    let progress = playback
        .position_ms
        .map(|ms| MediaPosition(Duration::from_millis(ms)));
    let media_playback = match playback.state.as_str() {
        "playing" => MediaPlayback::Playing { progress },
        "paused" => MediaPlayback::Paused { progress },
        _ => MediaPlayback::Stopped,
    };
    controls
        .set_playback(media_playback)
        .map_err(|e| format!("souvlaki: set_playback: {e:?}"))
}

/// Clear the OS widget — used when the user logs out or stops
/// playback for the day. Without this the lockscreen keeps showing
/// the last track even though the app is idle.
#[tauri::command]
pub fn now_playing_clear(state: State<'_, NowPlayingState>) -> Result<(), String> {
    let Ok(mut slot) = state.0.lock() else {
        return Ok(());
    };
    let Some(controls) = slot.as_mut() else {
        return Ok(());
    };
    controls
        .set_playback(MediaPlayback::Stopped)
        .map_err(|e| format!("souvlaki: clear playback: {e:?}"))?;
    Ok(())
}

// ── Platform HWND ──────────────────────────────────────────────────

#[cfg(target_os = "windows")]
fn platform_hwnd<R: Runtime>(app: &AppHandle<R>) -> Option<*mut std::ffi::c_void> {
    // souvlaki's PlatformConfig.hwnd is `Option<*mut c_void>` on
    // Windows — the SystemMediaTransportControls API needs a window
    // handle to register against. Tauri's WebviewWindow exposes
    // raw_window_handle; we extract the HWND off the main window.
    let win = app.get_webview_window("main")?;
    let raw = win.hwnd().ok()?;
    Some(raw.0 as *mut std::ffi::c_void)
}

#[cfg(not(target_os = "windows"))]
fn platform_hwnd<R: Runtime>(_app: &AppHandle<R>) -> Option<*mut std::ffi::c_void> {
    None
}
