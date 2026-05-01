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

/// Track metadata pushed by the frontend on every track change.
///
/// The frontend passes the bearer separately (rather than baking
/// `?token=<paseto>` into the URL) so the access token never reaches
/// the OS shell — Windows SMTC, macOS NowPlayingInfoCenter, and the
/// MPRIS bus all cache cover URLs in places that survive the
/// process. With token-in-URL, an attacker with later read access to
/// any of those caches recovers a 1 h general-purpose bearer.
///
/// We instead fetch the art on the Rust side with `Authorization:
/// Bearer <token>`, write it to a per-app temp file, and hand
/// souvlaki a `file://` URI. The OS shell sees only a local path.
#[derive(Deserialize)]
pub struct NowPlayingMetadata {
    pub title: String,
    pub artist: Option<String>,
    pub album: Option<String>,
    /// Absolute server-asset URL **without** any `?token=` query
    /// parameter. Rust fetches with the bearer header below.
    pub art_url: Option<String>,
    /// Bearer token for the art fetch. Held in memory only — never
    /// written to disk, never logged.
    pub art_bearer: Option<String>,
    pub duration_ms: Option<u64>,
}

#[tauri::command]
pub fn now_playing_set_metadata<R: Runtime>(
    app: AppHandle<R>,
    state: State<'_, NowPlayingState>,
    meta: NowPlayingMetadata,
) -> Result<(), String> {
    // Resolve cover_url to a local file:// URI when an art URL is
    // provided. Failures here are non-fatal — the widget shows
    // metadata without art rather than skipping the metadata update.
    let cover_uri = meta.art_url.as_deref().and_then(|u| {
        cache_art_to_temp(&app, u, meta.art_bearer.as_deref()).ok()
    });

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
            cover_url: cover_uri.as_deref(),
            duration: meta.duration_ms.map(Duration::from_millis),
        })
        .map_err(|e| format!("souvlaki: set_metadata: {e:?}"))
}

/// Fetch art with bearer header, write to `<app_cache>/now-playing.jpg`,
/// return the `file://` URI souvlaki should display. The cache file is
/// overwritten on every track change — only one image is on disk at a
/// time, no growth, and replacing it under the OS shell is fine because
/// the shell has already read the previous content into its own buffer
/// before we overwrite.
fn cache_art_to_temp<R: Runtime>(
    app: &AppHandle<R>,
    url: &str,
    bearer: Option<&str>,
) -> Result<String, String> {
    use std::io::{Read, Write};
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(Duration::from_secs(5))
        .timeout_read(Duration::from_secs(15))
        // No redirects — same reasoning as the audio engine. The art
        // URL was constructed by the frontend against the configured
        // server origin; a 30x to anywhere else is suspicious and
        // we'd rather fail fast than silently follow.
        .redirects(0)
        .build();
    let mut req = agent.get(url);
    if let Some(t) = bearer {
        req = req.set("Authorization", &format!("Bearer {t}"));
    }
    let resp = req.call().map_err(|e| format!("art fetch: {e}"))?;
    if resp.status() < 200 || resp.status() >= 300 {
        return Err(format!("art fetch: HTTP {}", resp.status()));
    }
    let mut bytes = Vec::with_capacity(64 * 1024);
    resp.into_reader()
        .read_to_end(&mut bytes)
        .map_err(|e| format!("art fetch read: {e}"))?;

    let cache_dir = app
        .path()
        .app_cache_dir()
        .map_err(|e| format!("art cache dir: {e}"))?;
    std::fs::create_dir_all(&cache_dir)
        .map_err(|e| format!("art cache mkdir: {e}"))?;
    let path = cache_dir.join("now-playing.jpg");
    let mut f = std::fs::File::create(&path).map_err(|e| format!("art write: {e}"))?;
    f.write_all(&bytes).map_err(|e| format!("art write: {e}"))?;
    drop(f);

    // souvlaki wants the URI form. Path::to_string_lossy is fine for
    // the local cache dir (no non-UTF-8 segments under the app's own
    // bundle id).
    Ok(format!("file://{}", path.to_string_lossy().replace('\\', "/")))
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
