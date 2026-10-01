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
#[cfg(target_os = "windows")]
mod windows_shared;

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
pub const STORE_FILE: &str = "settings.json";
pub const KEY_SERVER_URL: &str = "server_url";
// Tokens were originally co-located with server_url in the JSON
// store (a single file kept fsync churn low when both changed
// back-to-back in the setup flow). They now live in the OS
// keychain (Windows Credential Manager / macOS Keychain / Linux
// Secret Service) — the JSON store keys are retained read-only
// for one release as a migration fallback so a user upgrading
// from a previous build doesn't have to re-login.
const KEY_ACCESS_TOKEN: &str = "access_token";
const KEY_REFRESH_TOKEN: &str = "refresh_token";
// purpose=asset token. Read-only, 24 h, used in `?token=` on asset
// URLs (artwork / trickplay / subtitles / SSE) where the webview
// can't send an Authorization header. Persisted so a cold start can
// render posters before the first /auth/refresh mints a new one.
const KEY_ASSET_TOKEN: &str = "asset_token";

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

/// True when `host` can only be a local-network (or loopback) address:
/// RFC 1918 / CGNAT (Tailscale) / link-local / loopback IPv4, ULA /
/// link-local / loopback IPv6, single-label names and the conventional
/// local suffixes. The same rules as the TV apps' `isLocalNetworkHost`
/// (clients/webos/src/lib/api/client.ts), so every first-party client
/// agrees on which servers plain http:// is acceptable for.
fn is_local_network_host(host: &url::Host<&str>) -> bool {
    match host {
        url::Host::Ipv4(ip) => is_local_ipv4(ip),
        url::Host::Ipv6(ip) => {
            if let Some(v4) = ip.to_ipv4_mapped() {
                return is_local_ipv4(&v4);
            }
            let first = ip.segments()[0];
            ip.is_loopback()
                || (first & 0xfe00) == 0xfc00 // fc00::/7 unique local
                || (first & 0xffc0) == 0xfe80 // fe80::/10 link-local
        }
        url::Host::Domain(d) => {
            let h = d.trim_end_matches('.').to_ascii_lowercase();
            if h.is_empty() {
                return false;
            }
            if h == "localhost" || h.ends_with(".localhost") {
                return true;
            }
            if [".local", ".lan", ".home.arpa", ".internal"]
                .iter()
                .any(|suffix| h.ends_with(suffix))
            {
                return true;
            }
            // Single-label names ("nas", "plexbox") only resolve via local DNS.
            !h.contains('.')
        }
    }
}

fn is_local_ipv4(ip: &std::net::Ipv4Addr) -> bool {
    let [a, b, _, _] = ip.octets();
    a == 10
        || a == 127
        || (a == 172 && (16..=31).contains(&b))
        || (a == 192 && b == 168)
        || (a == 169 && b == 254)
        || (a == 100 && (64..=127).contains(&b))
}

/// "https://…" / "HTTP://…" / "ftp://…": the input names a scheme.
fn has_scheme(s: &str) -> bool {
    match s.find("://") {
        Some(i) if i > 0 => {
            let scheme = &s[..i];
            scheme.starts_with(|c: char| c.is_ascii_alphabetic())
                && scheme
                    .chars()
                    .all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '-' | '.'))
        }
        _ => false,
    }
}

/// The acceptance rules for a server URL with an explicit scheme. Shared by
/// `set_server_url`, `resolve_server_input` and `probe_server_url`, so what
/// the setup screen tries can never disagree with what the save accepts.
/// Returns the normalised string that gets persisted (lower-case scheme and
/// host, default port and trailing slash dropped) plus its parsed form.
///
/// Plain http:// is accepted for local-network hosts only — the usual
/// self-hosted `http://192.168.1.50:7070` — in release and debug builds
/// alike. To anything else it is refused: the password and the 30-day
/// refresh token ride this URL, and over cleartext across the internet
/// they're readable on every hop.
fn parse_server_url(url: &str) -> Result<(String, url::Url), String> {
    let trimmed = url.trim();
    if trimmed.is_empty() {
        return Err("Enter your OnScreen server's address.".into());
    }
    if !has_scheme(trimmed) {
        return Err(format!(
            "The server address must start with https:// or http:// (got {trimmed:?})."
        ));
    }
    let parsed = url::Url::parse(trimmed)
        .map_err(|e| format!("{trimmed:?} isn't a valid server address ({e})."))?;
    let host = match parsed.host() {
        Some(h) if !matches!(h, url::Host::Domain("")) => h,
        _ => return Err(format!("{trimmed:?} has no host name or IP address.")),
    };
    match parsed.scheme() {
        "https" => {}
        "http" => {
            if !is_local_network_host(&host) {
                return Err(format!(
                    "OnScreen only connects over plain http:// to servers on your local network, \
                     and {host} isn't one — your password would cross the internet unencrypted. \
                     Use the server's https:// address, or its local IP address \
                     (for example http://192.168.1.50:7070)."
                ));
            }
        }
        other => {
            return Err(format!(
                "Unsupported address type {other}:// — the server address must start with \
                 https:// or http://."
            ))
        }
    }
    if !parsed.username().is_empty() || parsed.password().is_some() {
        return Err(
            "Leave the user name and password out of the server address — you sign in on the next screen."
                .into(),
        );
    }
    if parsed.query().is_some() || parsed.fragment().is_some() {
        return Err("The server address can't contain ? or #.".into());
    }
    let normalised = parsed.as_str().trim_end_matches('/').to_string();
    Ok((normalised, parsed))
}

/// One URL the setup screen should try for what the user typed.
#[derive(Serialize, Debug, PartialEq, Eq)]
pub struct ServerCandidate {
    /// Normalised server URL, as `set_server_url` would store it.
    pub url: String,
    /// Plain http:// — the UI flags the connection as unencrypted.
    pub cleartext: bool,
    /// The host is on the local network (loopback, RFC 1918, .local, …).
    pub local: bool,
}

/// Turns what the user typed into the server URL(s) to try, in order.
///
/// - With a scheme: exactly that URL, under `parse_server_url`'s rules (an
///   explicit https:// is never retried over http://).
/// - Without one ("onscreen.example.com", "10.0.0.66:7070", "nas:7070"):
///   https:// first, then http:// — the http:// fallback only for
///   local-network hosts, the same order as the TV apps' setup screens.
fn server_url_candidates(input: &str) -> Result<Vec<ServerCandidate>, String> {
    let typed = input.trim();
    if typed.is_empty() {
        return Err("Enter your OnScreen server's address.".into());
    }
    let candidate = |s: &str| {
        parse_server_url(s).map(|(url, parsed)| ServerCandidate {
            cleartext: parsed.scheme() == "http",
            local: parsed.host().is_some_and(|h| is_local_network_host(&h)),
            url,
        })
    };
    if has_scheme(typed) {
        return candidate(typed).map(|c| vec![c]);
    }
    // "http:/host", "https:host": a scheme with the slashes mistyped.
    let lower = typed.to_ascii_lowercase();
    if lower.starts_with("http:") || lower.starts_with("https:") {
        return Err(format!(
            "Check the address {typed:?} — it should look like https://onscreen.example.com or 192.168.1.50:7070."
        ));
    }
    let typed = typed.trim_end_matches('/');
    let https = candidate(&format!("https://{typed}")).map_err(|_| {
        format!(
            "{typed:?} isn't a valid server address — it should look like \
             https://onscreen.example.com or 192.168.1.50:7070."
        )
    })?;
    let mut out = vec![https];
    // Refused for public hosts by parse_server_url — https:// only, then.
    if let Ok(http) = candidate(&format!("http://{typed}")) {
        out.push(http);
    }
    Ok(out)
}

/// The URLs the setup screen should try for what the user typed, or a
/// user-facing reason the input can't be a server address. Persists
/// nothing. The webview probes each candidate itself (that is the request
/// path the app will actually use — CORS, certificates and all) and saves
/// the first that answers with `set_server_url`.
#[tauri::command]
fn resolve_server_input(input: String) -> Result<Vec<ServerCandidate>, String> {
    server_url_candidates(&input)
}

/// What a reachable OnScreen server says about itself.
#[derive(Serialize, Debug)]
pub struct ServerProbe {
    /// The URL that answered (an http:// URL that redirected to https://
    /// on the same host comes back as the https:// one).
    pub url: String,
    pub name: String,
    pub version: String,
}

/// Rust-side reachability check, used by the setup screen to explain WHY
/// the webview couldn't reach a server: a webview `fetch` failure is an
/// opaque "Failed to fetch", whether the host is down, the certificate is
/// bad or the server's CORS policy refused the app's origin. This request
/// isn't subject to CORS, so "Rust reached it, the webview didn't" means
/// the server is up but doesn't allow the app's origin.
///
/// Only URLs `parse_server_url` accepts are probed; the request is an
/// unauthenticated GET of the public capabilities endpoint, follows at most
/// one same-host http:// → https:// redirect, and returns nothing but the
/// server's name and version.
#[tauri::command]
async fn probe_server_url(url: String) -> Result<ServerProbe, String> {
    let (base, _) = parse_server_url(&url)?;
    tauri::async_runtime::spawn_blocking(move || probe_server_blocking(&base, true))
        .await
        .map_err(|e| format!("server check failed: {e}"))?
}

fn probe_server_blocking(base: &str, allow_upgrade: bool) -> Result<ServerProbe, String> {
    use std::time::Duration;
    let agent = ureq::AgentBuilder::new()
        .redirects(0)
        .timeout_connect(Duration::from_secs(5))
        .timeout(Duration::from_secs(10))
        .build();
    let endpoint = format!("{base}/api/v1/system/capabilities");
    let resp = match agent.get(&endpoint).call() {
        Ok(r) => r,
        Err(ureq::Error::Status(code, _)) => {
            return Err(format!(
                "{base} answered with HTTP {code} — is this an OnScreen server's address?"
            ))
        }
        Err(ureq::Error::Transport(t)) => return Err(format!("{base}: {}", describe_transport(&t))),
    };
    if (300..400).contains(&resp.status()) {
        let location = resp.header("Location").unwrap_or("").to_string();
        if allow_upgrade {
            if let Some(upgraded) = https_upgrade_of(&endpoint, &location) {
                return probe_server_blocking(&upgraded, false);
            }
        }
        return Err(format!("{base} redirected to {location:?} — enter that address instead."));
    }
    let body = resp
        .into_string()
        .map_err(|e| format!("{base}: reading the reply failed: {e}"))?;
    let json: serde_json::Value = serde_json::from_str(&body).map_err(|_| {
        format!("{base} answered, but not like an OnScreen server — check the address and port.")
    })?;
    let server = json
        .get("data")
        .and_then(|d| d.get("server"))
        .filter(|s| s.is_object())
        .ok_or_else(|| {
            format!("{base} answered, but not like an OnScreen server — check the address and port.")
        })?;
    let field = |k: &str| server.get(k).and_then(|v| v.as_str()).unwrap_or("").to_string();
    Ok(ServerProbe {
        url: base.to_string(),
        name: field("name"),
        version: field("version"),
    })
}

/// The https:// server URL to adopt when probing `base` over http:// was
/// answered with a redirect to https:// on the SAME host (a reverse proxy
/// forcing TLS). Never a downgrade, never another host.
fn https_upgrade_of(endpoint: &str, location: &str) -> Option<String> {
    let from = url::Url::parse(endpoint).ok()?;
    if from.scheme() != "http" {
        return None;
    }
    let to = from.join(location).ok()?;
    if to.scheme() != "https" || to.host_str() != from.host_str() {
        return None;
    }
    // Keep any path prefix the server lives under (reverse-proxy subpath).
    let suffix = "/api/v1/system/capabilities";
    let to_path = to.path().strip_suffix(suffix)?;
    let mut upgraded = to.clone();
    upgraded.set_path(to_path);
    upgraded.set_query(None);
    upgraded.set_fragment(None);
    let (normalised, _) = parse_server_url(upgraded.as_str()).ok()?;
    Some(normalised)
}

fn describe_transport(t: &ureq::Transport) -> String {
    use ureq::ErrorKind;
    let detail = {
        let mut parts = Vec::new();
        if let Some(m) = t.message() {
            parts.push(m.to_string());
        }
        if let Some(src) = std::error::Error::source(t) {
            parts.push(src.to_string());
        }
        parts.join(": ")
    };
    let lower = detail.to_ascii_lowercase();
    match t.kind() {
        ErrorKind::Dns => "the host name couldn't be found (DNS lookup failed).".into(),
        ErrorKind::ConnectionFailed if lower.contains("timed out") => {
            "the connection timed out — is the server running, and is the port right?".into()
        }
        ErrorKind::ConnectionFailed => {
            "the connection was refused — is the server running, and is the port right?".into()
        }
        _ if lower.contains("certificate") || lower.contains("tls") || lower.contains("corrupt message") => {
            format!("a secure (https://) connection couldn't be set up ({detail}).")
        }
        _ if lower.contains("timed out") => "the server didn't answer in time.".into(),
        _ if detail.is_empty() => t.to_string(),
        _ => detail,
    }
}

/// Persists the server URL the setup screen tested, under the same rules as
/// `resolve_server_input` (this is the authoritative check: the webview has
/// no store permission, so it can't write `settings.json` around it).
#[tauri::command]
fn set_server_url(app: AppHandle, url: String) -> Result<(), String> {
    let (normalised, parsed) = parse_server_url(&url)?;
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    // Tokens are bound to the server that issued them. When the origin
    // changes, drop them BEFORE persisting the new URL so the old
    // server's access/refresh tokens are never sent to the new host (a
    // mistyped or malicious server would otherwise harvest a 30-day
    // refresh token for the old one on the first /auth/refresh). Same
    // origin (e.g. only a trailing path/slash changed) keeps the session.
    let prev_origin = store
        .get(KEY_SERVER_URL)
        .and_then(|v| v.as_str().and_then(|s| url::Url::parse(s).ok()))
        .map(|u| u.origin());
    if prev_origin.as_ref() != Some(&parsed.origin()) {
        wipe_tokens(&app)?;
    }
    store.set(KEY_SERVER_URL, normalised);
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
    pub asset_token: Option<String>,
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
    let asset_kc = keychain_get(KEY_ASSET_TOKEN);
    if access_kc.is_some() || refresh_kc.is_some() {
        return Ok(StoredTokens {
            access_token: access_kc,
            refresh_token: refresh_kc,
            asset_token: asset_kc,
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
    let asset_store = store
        .get(KEY_ASSET_TOKEN)
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
    let migrated_asset = match asset_store.as_deref() {
        Some(a) => keychain_set(KEY_ASSET_TOKEN, a),
        None => true,
    };
    // Migrated, or keychain unavailable and the user has NOT opted into
    // plaintext storage: either way the on-disk plaintext copy goes.
    // The values are returned below, so this session still works.
    if (migrated_access && migrated_refresh && migrated_asset) || !plaintext_tokens_opted_in() {
        store.delete(KEY_ACCESS_TOKEN);
        store.delete(KEY_REFRESH_TOKEN);
        store.delete(KEY_ASSET_TOKEN);
        let _ = store.save();
    }
    Ok(StoredTokens {
        access_token: access_store,
        refresh_token: refresh_store,
        asset_token: asset_store,
    })
}

/// Writes tokens to the OS keychain, with the legacy JSON store as
/// a degraded-platform fallback. On platforms where the keychain
/// works, the store is wiped on every set so we never have a stale
/// plaintext copy of a rotated refresh token sitting on disk.
#[tauri::command]
fn set_tokens(app: AppHandle, access: String, refresh: String, asset: String) -> Result<(), String> {
    let kc_access_ok = keychain_set(KEY_ACCESS_TOKEN, &access);
    let kc_refresh_ok = keychain_set(KEY_REFRESH_TOKEN, &refresh);
    // The asset token is optional (empty when talking to a pre-asset-
    // token server). Treat empty as "nothing to store, success" so a
    // missing asset token doesn't trip the keychain-degraded path.
    let kc_asset_ok = asset.is_empty() || keychain_set(KEY_ASSET_TOKEN, &asset);
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    if kc_access_ok && kc_refresh_ok && kc_asset_ok {
        // All landed in the keychain — strip any legacy entries.
        store.delete(KEY_ACCESS_TOKEN);
        store.delete(KEY_REFRESH_TOKEN);
        store.delete(KEY_ASSET_TOKEN);
        store.save().map_err(|e| e.to_string())?;
    } else {
        // Keychain partially or fully unavailable (headless Linux
        // without Secret Service, sandboxed mac builds without keychain
        // entitlements). Previously the missing values were written
        // SILENTLY in plaintext to `appdata/settings.json` — a 30-day
        // refresh token in a file that backups / sync pick up, and
        // nothing in the UI ever told the user. Now the default is to
        // NOT persist them: the in-memory session keeps working, the
        // user signs in again next launch. Operators who accept the
        // on-disk risk can opt back in with
        // ONSCREEN_ALLOW_PLAINTEXT_TOKENS=1 (file is chmod 0600 on Unix).
        let allow_plaintext = plaintext_tokens_opted_in();
        for (ok, key, value) in [
            (kc_access_ok, KEY_ACCESS_TOKEN, access),
            (kc_refresh_ok, KEY_REFRESH_TOKEN, refresh),
            (kc_asset_ok, KEY_ASSET_TOKEN, asset),
        ] {
            if !ok && allow_plaintext {
                store.set(key, value);
            } else {
                // Never leave a stale plaintext copy of a rotated token.
                store.delete(key);
                if !ok {
                    // Drop any older keychain copy too: it's a rotated
                    // (server-revoked) token and would hydrate a dead
                    // session next launch.
                    keychain_clear(key);
                }
            }
        }
        store.save().map_err(|e| e.to_string())?;
        if allow_plaintext {
            restrict_store_permissions(&app);
        }
        eprintln!(
            "auth: keychain unavailable (access_ok={kc_access_ok} refresh_ok={kc_refresh_ok} asset_ok={kc_asset_ok}) — {}",
            if allow_plaintext {
                "credentials cached PLAINTEXT in settings store (ONSCREEN_ALLOW_PLAINTEXT_TOKENS=1)"
            } else {
                "credentials NOT persisted; sign-in will be required next launch"
            }
        );
        let _ = app.emit(
            "auth:keychain-degraded",
            serde_json::json!({
                "access_ok": kc_access_ok,
                "refresh_ok": kc_refresh_ok,
                "asset_ok": kc_asset_ok,
                "plaintext": allow_plaintext,
            }),
        );
        notify_keychain_degraded(&app, allow_plaintext);
    }
    Ok(())
}

/// Opt-in for the old behaviour of caching tokens in the plaintext
/// settings store when the OS keychain is unavailable.
fn plaintext_tokens_opted_in() -> bool {
    matches!(
        std::env::var("ONSCREEN_ALLOW_PLAINTEXT_TOKENS").as_deref(),
        Ok("1") | Ok("true")
    )
}

/// Best-effort chmod 0600 on the settings store when it holds
/// plaintext credentials, so other local users can't read them.
/// tauri-plugin-store resolves relative store paths against the app
/// data dir. No-op on Windows (per-user profile ACLs already apply).
fn restrict_store_permissions(app: &AppHandle) {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if let Ok(dir) = app.path().app_data_dir() {
            let p = dir.join(STORE_FILE);
            if let Err(e) = std::fs::set_permissions(&p, std::fs::Permissions::from_mode(0o600)) {
                eprintln!("auth: chmod 0600 {}: {e}", p.display());
            }
        }
    }
    #[cfg(not(unix))]
    let _ = app;
}

/// Surfaces keychain degradation to the user instead of failing
/// silently: a one-shot OS notification per process. The
/// `auth:keychain-degraded` event alone was invisible — nothing in the
/// web bundle listens for it — whereas a notification needs no
/// frontend change.
fn notify_keychain_degraded(app: &AppHandle, plaintext: bool) {
    use std::sync::atomic::{AtomicBool, Ordering};
    use tauri_plugin_notification::NotificationExt;
    static NOTIFIED: AtomicBool = AtomicBool::new(false);

    if NOTIFIED.swap(true, Ordering::SeqCst) {
        return;
    }
    let body = if plaintext {
        "The system keychain is unavailable, so your sign-in is stored UNENCRYPTED in the OnScreen settings file."
    } else {
        "The system keychain is unavailable, so OnScreen can't remember your sign-in. You'll need to sign in again next time you open the app."
    };
    if let Err(e) = app
        .notification()
        .builder()
        .title("OnScreen: secure credential storage unavailable")
        .body(body)
        .show()
    {
        eprintln!("auth: keychain-degraded notification failed: {e}");
    }
}

/// Wipes tokens from both the keychain and the legacy store so a
/// logout doesn't leave a stranded copy in either place.
#[tauri::command]
fn clear_tokens(app: AppHandle) -> Result<(), String> {
    wipe_tokens(&app)
}

fn wipe_tokens(app: &AppHandle) -> Result<(), String> {
    keychain_clear(KEY_ACCESS_TOKEN);
    keychain_clear(KEY_REFRESH_TOKEN);
    keychain_clear(KEY_ASSET_TOKEN);
    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.delete(KEY_ACCESS_TOKEN);
    store.delete(KEY_REFRESH_TOKEN);
    store.delete(KEY_ASSET_TOKEN);
    store.save().map_err(|e| e.to_string())?;
    Ok(())
}

/// Downloads a URL to a user-chosen file path. Shows a native save-as
/// dialog (default filename = `suggested_filename`); on confirm,
/// streams the HTTP response body to disk via ureq and returns the
/// chosen path. User-cancelled dialogs return `Ok(None)` so the
/// frontend can distinguish between "they backed out" and a real
/// error.
///
/// `bearer_token` is optional: web downloads on the OnScreen server
/// authenticate via the per-file stream token in the URL query
/// string, so callers leave it empty. The argument exists for the
/// future case where a download URL needs an Authorization header
/// instead.
#[tauri::command]
async fn download_to_file(
    app: AppHandle,
    url: String,
    suggested_filename: String,
    bearer_token: Option<String>,
) -> Result<Option<String>, String> {
    use std::io::{BufWriter, Read, Write};
    use tauri_plugin_dialog::DialogExt;

    // Same-origin gate as the audio engine: a download URL + bearer come from the
    // webview, so a compromised/MITM'd frontend could otherwise drive this host
    // HTTP client at any host with an attacker-chosen Authorization header (SSRF +
    // token exfil). Reject anything that isn't the configured server BEFORE we
    // prompt or fetch.
    crate::audio::enforce_url_origin(&app, &url)?;

    // The dialog API is callback-based; bridge to async with a
    // oneshot so we can `await` the user's choice. Cancel arrives as
    // None. Tauri re-exports tokio's async primitives so we don't
    // need to pull tokio in as a direct dependency.
    let (tx, mut rx) = tauri::async_runtime::channel::<Option<tauri_plugin_dialog::FilePath>>(1);
    let dialog_tx = tx.clone();
    app.dialog()
        .file()
        .set_file_name(&suggested_filename)
        .save_file(move |path| {
            // The async closure can't await — fire-and-forget the
            // send; the receiver below treats a closed channel as
            // user-cancelled.
            let _ = dialog_tx.blocking_send(path);
        });
    let chosen = match rx.recv().await {
        Some(Some(p)) => p,
        Some(None) | None => return Ok(None),
    };

    // FilePath -> std::path::PathBuf. Reject non-FS paths (e.g.
    // mobile content:// URIs) — the desktop wrapper only ever sees
    // a filesystem path on the platforms we ship.
    let dst_path = chosen
        .into_path()
        .map_err(|e| format!("save dialog returned non-filesystem path: {e}"))?;

    // ureq is blocking, so do the download on a worker thread to
    // avoid pinning the Tauri main async runtime while a multi-GB
    // movie streams. spawn_blocking is the standard idiom for
    // "blocking I/O inside an async command".
    let dst_clone = dst_path.clone();
    tauri::async_runtime::spawn_blocking(move || -> Result<(), String> {
        // No redirects: the URL was origin-checked above, but a 30x could bounce
        // the request (and its bearer) to another host. Fail fast instead.
        let agent = ureq::AgentBuilder::new().redirects(0).build();
        let mut req = agent.get(&url);
        if let Some(token) = bearer_token.as_ref() {
            if !token.is_empty() {
                req = req.set("Authorization", &format!("Bearer {token}"));
            }
        }
        let resp = req.call().map_err(|e| format!("download request failed: {e}"))?;
        if resp.status() != 200 {
            return Err(format!("download HTTP status {}", resp.status()));
        }
        let mut reader = resp.into_reader();
        let f = std::fs::File::create(&dst_clone)
            .map_err(|e| format!("create {}: {e}", dst_clone.display()))?;
        let mut writer = BufWriter::new(f);
        // 1 MiB copy buffer — large enough that fread/fwrite syscall
        // overhead is amortised on multi-GB downloads, small enough
        // that a torn write never holds more than a frame's worth.
        let mut buf = vec![0u8; 1 << 20];
        loop {
            let n = reader.read(&mut buf).map_err(|e| format!("read: {e}"))?;
            if n == 0 {
                break;
            }
            writer.write_all(&buf[..n]).map_err(|e| format!("write: {e}"))?;
        }
        writer.flush().map_err(|e| format!("flush: {e}"))?;
        Ok(())
    })
    .await
    .map_err(|e| format!("download task panicked: {e}"))??;

    Ok(Some(dst_path.to_string_lossy().into_owned()))
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
        // Native save-as dialog. Used by the Download button on the
        // watch page so the user picks where to drop the media file
        // on their disk; the webview's <a download> doesn't fire the
        // OS save flow on its own.
        .plugin(tauri_plugin_dialog::init())
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
            // Escape hatch to the server setting from any screen (the setup
            // wizard and error pages have no sidebar link to it).
            let server_item =
                MenuItem::with_id(app, "server", "Change server…", true, None::<&str>)?;
            let play_item = MenuItem::with_id(app, "play-pause", "Play / Pause", true, None::<&str>)?;
            let next_item = MenuItem::with_id(app, "next", "Next", true, None::<&str>)?;
            let prev_item = MenuItem::with_id(app, "previous", "Previous", true, None::<&str>)?;
            let sep = PredefinedMenuItem::separator(app)?;
            let quit_item = MenuItem::with_id(app, "quit", "Quit OnScreen", true, None::<&str>)?;
            let menu = Menu::with_items(
                app,
                &[
                    &show_item,
                    &server_item,
                    &sep,
                    &play_item,
                    &next_item,
                    &prev_item,
                    &sep,
                    &quit_item,
                ],
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
                    "server" => {
                        focus_main_window(app);
                        let _ = app.emit("open-server-settings", ());
                    }
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
            resolve_server_input,
            probe_server_url,
            set_server_url,
            clear_server_url,
            get_tokens,
            set_tokens,
            clear_tokens,
            download_to_file,
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
            audio::audio_set_volume,
            audio::audio_get_exclusive_mode,
            audio::audio_get_active_backend,
            audio::audio_get_output_is_bluetooth,
            audio::audio_set_bluetooth_override,
            now_playing::now_playing_set_metadata,
            now_playing::now_playing_set_playback,
            now_playing::now_playing_clear,
        ])
        .run(tauri::generate_context!())
        .expect("error while running OnScreen desktop");
}


#[cfg(test)]
mod server_url_tests {
    use super::*;

    fn ok(input: &str) -> String {
        parse_server_url(input).unwrap_or_else(|e| panic!("{input:?} rejected: {e}")).0
    }

    fn err(input: &str) -> String {
        match parse_server_url(input) {
            Ok((u, _)) => panic!("{input:?} accepted as {u:?}"),
            Err(e) => e,
        }
    }

    fn urls(input: &str) -> Vec<String> {
        server_url_candidates(input)
            .unwrap_or_else(|e| panic!("{input:?} rejected: {e}"))
            .into_iter()
            .map(|c| c.url)
            .collect()
    }

    #[test]
    fn https_is_accepted_for_any_host() {
        assert_eq!(ok("https://onscreen.wolverscreen.com"), "https://onscreen.wolverscreen.com");
        assert_eq!(ok("https://8.8.8.8:7070"), "https://8.8.8.8:7070");
        assert_eq!(ok("https://10.0.0.66:7070"), "https://10.0.0.66:7070");
    }

    #[test]
    fn plaintext_http_is_accepted_for_local_network_hosts() {
        // The examples the setup screen prints, the NAS, and every range the
        // TV apps' isLocalNetworkHost treats as local.
        for u in [
            "http://192.168.1.50:7070",
            "http://10.0.0.66:7070",
            "http://172.16.0.1:7070",
            "http://172.31.255.254",
            "http://100.64.0.1:7070",
            "http://100.127.255.1",
            "http://169.254.10.20:7070",
            "http://127.0.0.1:7070",
            "http://127.0.0.2:7070",
            "http://localhost:7070",
            "http://onscreen.localhost:7070",
            "http://nas.local:7070",
            "http://nas.lan",
            "http://media.home.arpa",
            "http://onscreen.internal",
            "http://nas:7070",
            "http://NAS.LOCAL.:7070",
            "http://[::1]:7070",
            "http://[fd12:3456::1]:7070",
            "http://[fe80::1]:7070",
            "http://[::ffff:192.168.1.5]:7070",
        ] {
            ok(u);
        }
    }

    #[test]
    fn plaintext_http_is_refused_for_public_hosts() {
        for u in [
            "http://onscreen.wolverscreen.com",
            "http://8.8.8.8:7070",
            "http://172.32.0.1",
            "http://172.15.0.1",
            "http://100.128.0.1",
            "http://192.169.1.1",
            "http://11.0.0.1",
            "http://[2001:db8::1]:7070",
            "http://[::ffff:8.8.8.8]",
            "http://example.local.evil.com",
        ] {
            let e = err(u);
            assert!(e.contains("local network"), "{u}: {e}");
            assert!(e.contains("https://"), "{u}: {e}");
        }
    }

    #[test]
    fn normalises_what_it_stores() {
        assert_eq!(ok("  HTTPS://OnScreen.Example.COM:443/  "), "https://onscreen.example.com");
        assert_eq!(ok("http://192.168.1.50:7070/"), "http://192.168.1.50:7070");
        assert_eq!(ok("http://localhost:80"), "http://localhost");
        // A reverse-proxy subpath is kept (api.ts appends /api/v1 to it).
        assert_eq!(ok("https://example.com/onscreen/"), "https://example.com/onscreen");
    }

    #[test]
    fn rejects_what_cannot_be_a_server_address() {
        assert!(err("").contains("Enter"));
        assert!(err("   ").contains("Enter"));
        assert!(err("ftp://nas.local").contains("ftp://"));
        assert!(err("onscreen.example.com").contains("https://"));
        assert!(err("https://").contains("valid") || err("https://").contains("host"));
        assert!(err("https://user:secret@example.com").contains("user name and password"));
        assert!(err("https://example.com/?x=1").contains("?"));
        assert!(err("https://example.com/#top").contains("#"));
        assert!(err("https://exa mple.com").contains("valid"));
    }

    #[test]
    fn a_bare_local_host_tries_https_then_http() {
        assert_eq!(urls("10.0.0.66:7070"), ["https://10.0.0.66:7070", "http://10.0.0.66:7070"]);
        assert_eq!(urls("192.168.1.50:7070/"), ["https://192.168.1.50:7070", "http://192.168.1.50:7070"]);
        // "nas:7070" used to parse as scheme "nas" and be rejected.
        assert_eq!(urls("nas:7070"), ["https://nas:7070", "http://nas:7070"]);
        assert_eq!(urls("localhost:7070"), ["https://localhost:7070", "http://localhost:7070"]);
        assert_eq!(urls("[::1]:7070"), ["https://[::1]:7070", "http://[::1]:7070"]);
        let c = server_url_candidates("nas.local:7070").unwrap();
        assert!(!c[0].cleartext && c[0].local);
        assert!(c[1].cleartext && c[1].local);
    }

    #[test]
    fn a_bare_public_host_tries_https_only() {
        assert_eq!(urls("onscreen.wolverscreen.com"), ["https://onscreen.wolverscreen.com"]);
        let c = server_url_candidates("onscreen.wolverscreen.com").unwrap();
        assert_eq!(
            c,
            [ServerCandidate {
                url: "https://onscreen.wolverscreen.com".into(),
                cleartext: false,
                local: false
            }]
        );
    }

    #[test]
    fn an_explicit_scheme_is_used_as_typed() {
        assert_eq!(urls("https://10.0.0.66:7070"), ["https://10.0.0.66:7070"]);
        assert_eq!(urls("http://10.0.0.66:7070"), ["http://10.0.0.66:7070"]);
        assert_eq!(urls("HTTP://localhost:7070"), ["http://localhost:7070"]);
        let e = server_url_candidates("http://onscreen.wolverscreen.com").unwrap_err();
        assert!(e.contains("local network"), "{e}");
    }

    #[test]
    fn malformed_bare_input_gets_a_readable_error() {
        for bad in ["", "http:/10.0.0.66", "https:example.com", "exa mple.com", "ftp://x"] {
            let e = server_url_candidates(bad).unwrap_err();
            assert!(!e.is_empty(), "{bad:?}");
        }
        assert!(server_url_candidates("http:/10.0.0.66").unwrap_err().contains("should look like"));
    }

    #[test]
    fn https_upgrade_is_same_host_only() {
        let ep = "http://nas.local:7070/api/v1/system/capabilities";
        assert_eq!(
            https_upgrade_of(ep, "https://nas.local/api/v1/system/capabilities").as_deref(),
            Some("https://nas.local")
        );
        assert_eq!(
            https_upgrade_of(
                "http://example.lan/onscreen/api/v1/system/capabilities",
                "https://example.lan/onscreen/api/v1/system/capabilities"
            )
            .as_deref(),
            Some("https://example.lan/onscreen")
        );
        // Another host, a downgrade, or an unrelated path: not adopted.
        assert_eq!(https_upgrade_of(ep, "https://evil.example/api/v1/system/capabilities"), None);
        assert_eq!(https_upgrade_of(ep, "http://nas.local:8080/api/v1/system/capabilities"), None);
        assert_eq!(https_upgrade_of(ep, "https://nas.local/login"), None);
        assert_eq!(
            https_upgrade_of(
                "https://nas.local/api/v1/system/capabilities",
                "https://nas.local:8443/api/v1/system/capabilities"
            ),
            None
        );
    }
}
