// OnScreen desktop — entry point and IPC commands.
//
// The webview hosts the existing SvelteKit frontend (web/dist).
// Rust handles anything the browser can't:
// - Persistent server-URL + credential storage (tauri-plugin-store)
// - Bit-perfect audio output (future: cpal + WASAPI exclusive /
//   CoreAudio HOG / ALSA hw:)
// - System integration (tray, notifications, media keys)

use serde::Serialize;
use tauri::{AppHandle, Manager};
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
// Tokens get their own keys on the same store rather than a separate
// file: tauri-plugin-store writes the whole file on Save, so co-locating
// reduces fsync churn when both change in the same flow (e.g. setup
// screen → login → both server URL and tokens land back-to-back).
//
// SECURITY NOTE: tauri-plugin-store is not encryption-at-rest. Tokens
// here sit in the platform's appdata dir as plain JSON, readable by
// any process running as the same user. Acceptable for v2.1
// scaffolding because (a) the access token is short-lived (1 h) so
// the blast radius of a leaked file is bounded and (b) the refresh
// token is server-revocable via the existing session-epoch path.
// Follow-up: swap to tauri-plugin-keychain (macOS Keychain / Windows
// Credential Vault / libsecret) so the tokens move out of plaintext.
const KEY_ACCESS_TOKEN: &str = "access_token";
const KEY_REFRESH_TOKEN: &str = "refresh_token";

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

#[tauri::command]
fn get_tokens(app: AppHandle) -> Result<StoredTokens, String> {
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    Ok(StoredTokens {
        access_token: store
            .get(KEY_ACCESS_TOKEN)
            .and_then(|v| v.as_str().map(String::from)),
        refresh_token: store
            .get(KEY_REFRESH_TOKEN)
            .and_then(|v| v.as_str().map(String::from)),
    })
}

#[tauri::command]
fn set_tokens(app: AppHandle, access: String, refresh: String) -> Result<(), String> {
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.set(KEY_ACCESS_TOKEN, access);
    store.set(KEY_REFRESH_TOKEN, refresh);
    store.save().map_err(|e| e.to_string())?;
    Ok(())
}

#[tauri::command]
fn clear_tokens(app: AppHandle) -> Result<(), String> {
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.delete(KEY_ACCESS_TOKEN);
    store.delete(KEY_REFRESH_TOKEN);
    store.save().map_err(|e| e.to_string())?;
    Ok(())
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
        .invoke_handler(tauri::generate_handler![
            get_app_version,
            get_server_url,
            set_server_url,
            get_tokens,
            set_tokens,
            clear_tokens,
        ])
        .run(tauri::generate_context!())
        .expect("error while running OnScreen desktop");
}
