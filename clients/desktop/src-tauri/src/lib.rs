// OnScreen desktop — entry point and IPC commands.
//
// The webview hosts the existing SvelteKit frontend (web/dist).
// Rust handles anything the browser can't:
// - Persistent server-URL + credential storage (tauri-plugin-store)
// - Bit-perfect audio output (future: cpal + WASAPI exclusive /
//   CoreAudio HOG / ALSA hw:)
// - System integration (tray, notifications, media keys)
//
// For now the only command exposed is `get_app_version`, used as a
// smoke test to prove the IPC bridge is reachable from the
// SvelteKit side once the user opts into the native build.

use serde::Serialize;

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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        // Persistent key/value store — used to hold the OnScreen
        // server URL the user picked at first launch and (later)
        // any locally-cached preferences. Backed by JSON in the
        // platform appdata dir, so it survives reinstalls and is
        // backupable like any other config file.
        .plugin(tauri_plugin_store::Builder::new().build())
        .invoke_handler(tauri::generate_handler![get_app_version])
        .run(tauri::generate_context!())
        .expect("error while running OnScreen desktop");
}
