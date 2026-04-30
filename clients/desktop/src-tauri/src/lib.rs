// OnScreen desktop — entry point and IPC commands.
//
// The webview hosts the existing SvelteKit frontend (web/dist).
// Rust handles anything the browser can't:
// - Persistent server-URL + credential storage (tauri-plugin-store)
// - Bit-perfect audio output (future: cpal + WASAPI exclusive /
//   CoreAudio HOG / ALSA hw:)
// - System integration (tray, notifications, media keys)

mod audio;
mod now_playing;
#[cfg(target_os = "windows")]
mod windows_exclusive;

use serde::Serialize;
use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Emitter, Manager,
};
use tauri_plugin_global_shortcut::{Code, GlobalShortcutExt, Shortcut, ShortcutState};
use tauri_plugin_store::StoreExt;

/// Returned by `get_app_version` so the frontend can branch on
/// `window.__TAURI__` and show a "Native build" badge or surface a
/// version mismatch warning when the wrapper is older than the
/// embedded web bundle.
#[derive(Serialize)]
pub struct AppVersion {
    pub version: &'static str,
    pub tauri: &'static str,
    pub target_os: &'static str,
}

#[tauri::command]
fn get_app_version() -> AppVersion {
    AppVersion {
        version: env!("CARGO_PKG_VERSION"),
        // Bumped via the tauri crate's own version each build — gives
        // ops a quick way to check which Tauri runtime is installed
        // without spelunking the bundle.
        tauri: "2.x",
        target_os: std::env::consts::OS,
    }
}

// Single JSON store file under the platform appdata dir
// (~/AppData/Roaming/com.onscreen.desktop/ on Windows,
// ~/Library/Application Support/com.onscreen.desktop/ on macOS,
// ~/.local/share/com.onscreen.desktop/ on Linux).
//
// One file rather than one per setting because tauri-plugin-store
// writes the whole file on Save — fewer files = fewer writes when
// settings change in bursts.
const STORE_FILE: &str = "settings.json";
const KEY_SERVER_URL: &str = "server_url";
// Tokens were originally co-located with server_url in the JSON
// store (a single file kept fsync churn low when both changed
// back-to-back in the setup flow). They now live in the OS
// keychain (Windows Credential Manager / macOS Keychain / Linux
// Secret Service) — the JSON store keys are retained read-only
// for one release as a migration fallback so a user upgrading
// from a previous build doesn't have to re-login.
const KEY_ACCESS_TOKEN: &str = "access_token";
const KEY_REFRESH_TOKEN: &str = "refresh_token";

// Keychain entry identifiers. Service is the bundle identifier so
// "OnScreen" doesn't collide with another app named OnScreen on a
// shared workstation; account is the credential name (the same
// keys as the legacy store). Linux Secret Service maps these to a
// schema entry's "service" + "username" attributes.
const KEYCHAIN_SERVICE: &str = "com.onscreen.desktop";

/// Read a credential from the OS keychain. Returns None when the
/// entry doesn't exist (NoEntry), which is the normal "first run"
/// path. Other errors (libsecret unavailable, locked keychain) are
/// turned into None too so the caller falls back to the legacy
/// store rather than refusing to launch.
fn keychain_get(account: &str) -> Option<String> {
    let entry = keyring::Entry::new(KEYCHAIN_SERVICE, account).ok()?;
    match entry.get_password() {
        Ok(s) => Some(s),
        Err(keyring::Error::NoEntry) => None,
        Err(e) => {
            eprintln!("keychain: get {account}: {e}");
            None
        }
    }
}

/// Write a credential to the OS keychain. Logs and returns false on
/// failure (e.g. headless Linux without secret-service); the caller
/// then keeps the value in the legacy plaintext store so login
/// still works on degraded platforms.
fn keychain_set(account: &str, value: &str) -> bool {
    match keyring::Entry::new(KEYCHAIN_SERVICE, account) {
        Ok(entry) => match entry.set_password(value) {
            Ok(()) => true,
            Err(e) => {
                eprintln!("keychain: set {account}: {e}");
                false
            }
        },
        Err(e) => {
            eprintln!("keychain: open {account}: {e}");
            false
        }
    }
}

/// Delete a credential. Treats NoEntry as success — the goal is
/// "after this returns, no entry exists for this account."
fn keychain_clear(account: &str) {
    if let Ok(entry) = keyring::Entry::new(KEYCHAIN_SERVICE, account) {
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => {}
            Err(e) => eprintln!("keychain: delete {account}: {e}"),
        }
    }
}

/// Returns the configured OnScreen server URL, or None when the user
/// hasn't completed the first-run setup. The frontend uses None to
/// gate the URL-picker UI.
#[tauri::command]
fn get_server_url(app: AppHandle) -> Result<Option<String>, String> {
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    Ok(store
        .get(KEY_SERVER_URL)
        .and_then(|v| v.as_str().map(String::from)))
}

/// Validates and persists the OnScreen server URL the user picked.
///
/// Validation here is intentionally minimal — the URL must parse and
/// use http or https — because the *real* validation (does this URL
/// host a healthy OnScreen server?) is a network round-trip the
/// frontend should do explicitly so the user gets a clear "couldn't
/// reach the server" error rather than a silent persist that breaks
/// at first request. Per the same logic, we don't probe `/health/live`
/// from Rust on save: the frontend is the right place to surface
/// "checking…" UX and capture the response shape mismatch path.
/// Removes the stored server URL so the layout's first-run gate
/// kicks in on the next reload. Symmetric with clear_tokens — the
/// /native/server "Sign out + clear server URL" button uses both
/// to fully reset the client without the user having to delete
/// the appdata file by hand.
#[tauri::command]
fn clear_server_url(app: AppHandle) -> Result<(), String> {
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.delete(KEY_SERVER_URL);
    store.save().map_err(|e| e.to_string())?;
    Ok(())
}

#[tauri::command]
fn set_server_url(app: AppHandle, url: String) -> Result<(), String> {
    let trimmed = url.trim().trim_end_matches('/').to_string();
    if trimmed.is_empty() {
        return Err("server URL cannot be empty".into());
    }
    let parsed = url::Url::parse(&trimmed).map_err(|e| format!("invalid URL: {e}"))?;
    match parsed.scheme() {
        "http" | "https" => {}
        other => {
            return Err(format!(
                "unsupported scheme {other:?} — server URL must be http:// or https://"
            ))
        }
    }
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.set(KEY_SERVER_URL, trimmed);
    store.save().map_err(|e| e.to_string())?;
    Ok(())
}

/// Tokens stored together so the frontend can hydrate the bearer
/// header + the refresh path in a single IPC round-trip on startup.
/// Both fields are Option so a partially-completed setup (URL set,
/// not yet logged in) doesn't trip a deserialise error.
#[derive(Serialize, Default)]
pub struct StoredTokens {
    pub access_token: Option<String>,
    pub refresh_token: Option<String>,
}

/// Reads tokens from the OS keychain. Falls through to the legacy
/// JSON store on first launch after upgrading (one-shot migration)
/// or when the keychain is unavailable (headless Linux without
/// secret-service). On a successful migration, the values move into
/// the keychain and the store entries are wiped — subsequent reads
/// hit the keychain directly.
#[tauri::command]
fn get_tokens(app: AppHandle) -> Result<StoredTokens, String> {
    let access_kc = keychain_get(KEY_ACCESS_TOKEN);
    let refresh_kc = keychain_get(KEY_REFRESH_TOKEN);
    if access_kc.is_some() || refresh_kc.is_some() {
        return Ok(StoredTokens {
            access_token: access_kc,
            refresh_token: refresh_kc,
        });
    }
    // Legacy fallback: tokens may be in the plaintext store from a
    // pre-keychain install. Read them, migrate to the keychain, and
    // wipe the store entries. Failures during migration leave the
    // store entries intact so the next run tries again.
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    let access_store = store
        .get(KEY_ACCESS_TOKEN)
        .and_then(|v| v.as_str().map(String::from));
    let refresh_store = store
        .get(KEY_REFRESH_TOKEN)
        .and_then(|v| v.as_str().map(String::from));
    if access_store.is_none() && refresh_store.is_none() {
        return Ok(StoredTokens::default());
    }
    let migrated_access = match access_store.as_deref() {
        Some(a) => keychain_set(KEY_ACCESS_TOKEN, a),
        None => true, // nothing to migrate counts as success
    };
    let migrated_refresh = match refresh_store.as_deref() {
        Some(r) => keychain_set(KEY_REFRESH_TOKEN, r),
        None => true,
    };
    if migrated_access && migrated_refresh {
        store.delete(KEY_ACCESS_TOKEN);
        store.delete(KEY_REFRESH_TOKEN);
        let _ = store.save();
    }
    Ok(StoredTokens {
        access_token: access_store,
        refresh_token: refresh_store,
    })
}

/// Writes tokens to the OS keychain, with the legacy JSON store as
/// a degraded-platform fallback. On platforms where the keychain
/// works, the store is wiped on every set so we never have a stale
/// plaintext copy of a rotated refresh token sitting on disk.
#[tauri::command]
fn set_tokens(app: AppHandle, access: String, refresh: String) -> Result<(), String> {
    let kc_access_ok = keychain_set(KEY_ACCESS_TOKEN, &access);
    let kc_refresh_ok = keychain_set(KEY_REFRESH_TOKEN, &refresh);
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    if kc_access_ok && kc_refresh_ok {
        // Both landed in the keychain — strip any legacy entries.
        store.delete(KEY_ACCESS_TOKEN);
        store.delete(KEY_REFRESH_TOKEN);
        store.save().map_err(|e| e.to_string())?;
    } else {
        // Keychain partially or fully unavailable; persist whichever
        // values didn't make it so the user stays signed in.
        if !kc_access_ok {
            store.set(KEY_ACCESS_TOKEN, access);
        }
        if !kc_refresh_ok {
            store.set(KEY_REFRESH_TOKEN, refresh);
        }
        store.save().map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Wipes tokens from both the keychain and the legacy store so a
/// logout doesn't leave a stranded copy in either place.
#[tauri::command]
fn clear_tokens(app: AppHandle) -> Result<(), String> {
    keychain_clear(KEY_ACCESS_TOKEN);
    keychain_clear(KEY_REFRESH_TOKEN);
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.delete(KEY_ACCESS_TOKEN);
    store.delete(KEY_REFRESH_TOKEN);
    store.save().map_err(|e| e.to_string())?;
    Ok(())
}

/// Brings the main window forward — used by both the tray icon's
/// left-click and the "Show OnScreen" menu item. Unminimises before
/// focusing so a tray click recovers from a minimized state too.
/// Errors are logged but ignored: the worst case is the user has to
/// click their dock icon instead.
fn focus_main_window(app: &AppHandle) {
    if let Some(win) = app.get_webview_window("main") {
        let _ = win.unminimize();
        let _ = win.show();
        let _ = win.set_focus();
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        // Persistent key/value store — used to hold the OnScreen
        // server URL the user picked at first launch and (later)
        // any locally-cached preferences. Backed by JSON in the
        // platform appdata dir, so it survives reinstalls and is
        // backupable like any other config file.
        .plugin(tauri_plugin_store::Builder::new().build())
        // OS notifications — surfaces "Now playing X by Y" to the
        // notification shell when a new track starts. Frontend
        // gates this behind a user pref so it doesn't spam during
        // album playback.
        .plugin(tauri_plugin_notification::init())
        // Global keyboard shortcuts — registers the OS media keys
        // (Play/Pause, Next, Previous, Stop) so transport works
        // when OnScreen isn't focused. The handler emits a
        // `media-key` event the AudioPlayer in the webview listens
        // for and dispatches into the audio store. Same-shape UX
        // as Spotify/Plexamp without having to integrate per-OS
        // media-control APIs (SMTC/MPRIS/MediaPlayer) — those
        // remain a follow-up for OS now-playing widgets.
        .plugin(
            tauri_plugin_global_shortcut::Builder::new()
                .with_handler(|app, shortcut, event| {
                    // Only fire on press, not release — a media-key
                    // tap emits both states and we'd otherwise
                    // double-fire every action.
                    if event.state() != ShortcutState::Pressed {
                        return;
                    }
                    let action = match shortcut.key {
                        Code::MediaPlayPause => "play-pause",
                        Code::MediaTrackNext => "next",
                        Code::MediaTrackPrevious => "previous",
                        Code::MediaStop => "stop",
                        _ => return,
                    };
                    let _ = app.emit("media-key", action);
                })
                .build(),
        )
        .setup(|app| {
            // Register the media-key shortcuts at startup. Failures
            // here are non-fatal (another app may have grabbed the
            // shortcut first) — log and keep running rather than
            // refusing to launch.
            let gs = app.global_shortcut();
            for code in [
                Code::MediaPlayPause,
                Code::MediaTrackNext,
                Code::MediaTrackPrevious,
                Code::MediaStop,
            ] {
                if let Err(e) = gs.register(Shortcut::new(None, code)) {
                    eprintln!("media-key {code:?}: register failed: {e}");
                }
            }

            // System tray: keeps the app reachable while the window
            // is closed (X just hides on Windows/Linux per Tauri 2's
            // default close-behavior, so the tray is the recovery
            // path). Menu items emit the same `media-key` event the
            // global-shortcut handler does, so the AudioPlayer's
            // listener handles both paths uniformly.
            let show_item = MenuItem::with_id(app, "show", "Show OnScreen", true, None::<&str>)?;
            let play_item = MenuItem::with_id(app, "play-pause", "Play / Pause", true, None::<&str>)?;
            let next_item = MenuItem::with_id(app, "next", "Next", true, None::<&str>)?;
            let prev_item = MenuItem::with_id(app, "previous", "Previous", true, None::<&str>)?;
            let sep = PredefinedMenuItem::separator(app)?;
            let quit_item = MenuItem::with_id(app, "quit", "Quit OnScreen", true, None::<&str>)?;
            let menu = Menu::with_items(
                app,
                &[&show_item, &sep, &play_item, &next_item, &prev_item, &sep, &quit_item],
            )?;
            let _tray = TrayIconBuilder::with_id("main")
                .tooltip("OnScreen")
                .icon(app.default_window_icon().cloned().unwrap_or_else(|| {
                    // No app icon configured — Tauri builds a 1x1
                    // transparent PNG by default. The tray will
                    // still be present, just blank.
                    tauri::image::Image::new_owned(vec![0u8; 4], 1, 1)
                }))
                .menu(&menu)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => focus_main_window(app),
                    "play-pause" | "next" | "previous" => {
                        let _ = app.emit("media-key", event.id.as_ref());
                    }
                    "quit" => app.exit(0),
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    // Left-click brings the window to the front.
                    // Right-click is reserved for the OS menu (the
                    // tray plugin handles that automatically).
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        focus_main_window(tray.app_handle());
                    }
                })
                .build(app)?;

            // OS now-playing widget. Builds once at setup-time, after
            // the main window exists (Windows needs its HWND). Failure
            // is non-fatal — the manage() call still installs the
            // empty NowPlayingState so the frontend's commands no-op
            // gracefully on a headless container or a Linux box
            // without a session bus.
            app.manage(now_playing::NowPlayingState::default());
            if let Err(e) = now_playing::init(app.handle()) {
                eprintln!("now-playing widget: init failed: {e}");
            }
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            get_app_version,
            get_server_url,
            set_server_url,
            clear_server_url,
            get_tokens,
            set_tokens,
            clear_tokens,
            audio::list_audio_devices,
            audio::play_test_tone,
            audio::stop_audio,
            audio::audio_play_url,
            audio::audio_preload_url,
            audio::audio_seek,
            audio::audio_state,
            audio::audio_pause,
            audio::audio_resume,
            audio::replay_gain_set_mode,
            audio::replay_gain_set_preamp,
            audio::audio_set_exclusive_mode,
            audio::audio_get_exclusive_mode,
            audio::audio_get_active_backend,
            now_playing::now_playing_set_metadata,
            now_playing::now_playing_set_playback,
            now_playing::now_playing_clear,
        ])
        .run(tauri::generate_context!())
        .expect("error while running OnScreen desktop");
}
