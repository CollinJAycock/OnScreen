// TV mode: the 10-foot UI for Steam Deck gaming mode (and any couch PC).
//
// In gaming mode Steam runs the app full screen under gamescope with only a
// controller. The desktop web UI is built for a mouse, so there the window
// shows the OnScreen TV app instead: the controller-driven 1920x1080 UI the
// server serves at <server>/tvapp/ (clients/xbox; the Xbox shell shows the
// same page). It reads the controller through the Gamepad API (WebKitGTK's
// libmanette backend: the Flatpak needs --device=input).
//
// The page is loaded from the server, so it shares the API's origin (as on
// Xbox) and gets no Tauri IPC: the capability file lists only local pages. It
// asks for the two things it can't do itself by navigating to a URL this
// module intercepts before the webview acts on it:
//
//   onscreen-shell://exit          the exit popup's Exit: quit the app
//   onscreen-shell://changeServer  Settings' Forget server: the local
//                                  server screen (/native/server)
//
// When: Steam's gaming mode sets SteamGamepadUI=1, and gamescope sessions set
// XDG_CURRENT_DESKTOP=gamescope (both reach a Flatpak's sandbox). Desktop
// mode on the Deck sets neither, so it keeps the normal desktop UI.
// ONSCREEN_TV_MODE=1 / 0, or --tv-mode / --desktop-mode, overrides either way
// (a Steam launch option, or to test on a PC).

use url::Url;

/// The scheme of the page -> shell requests above.
pub const SHELL_SCHEME: &str = "onscreen-shell";

/// Whether this launch shows the TV app. `env` reads an environment
/// variable; `args` are the process arguments.
pub fn detect(env: &dyn Fn(&str) -> Option<String>, args: &[String]) -> bool {
    for a in args.iter().skip(1) {
        match a.as_str() {
            "--tv-mode" => return true,
            "--desktop-mode" => return false,
            _ => {}
        }
    }
    if let Some(v) = env("ONSCREEN_TV_MODE") {
        match v.trim().to_ascii_lowercase().as_str() {
            "1" | "true" | "yes" | "on" => return true,
            "0" | "false" | "no" | "off" => return false,
            _ => {}
        }
    }
    if env("SteamGamepadUI").is_some_and(|v| v.trim() == "1") {
        return true;
    }
    env("XDG_CURRENT_DESKTOP").is_some_and(|v| {
        v.split(':').any(|d| d.trim().eq_ignore_ascii_case("gamescope"))
    })
}

/// The device name the TV app shows and registers its session under:
/// "Steam Deck" on a Deck (Steam sets SteamDeck=1 there), else none and the
/// app makes its own.
pub fn device_name(env: &dyn Fn(&str) -> Option<String>) -> &'static str {
    if env("SteamDeck").is_some_and(|v| v.trim() == "1") {
        "Steam Deck"
    } else {
        ""
    }
}

/// <server>/tvapp/ with the shell parameters the TV app reads at boot
/// (clients/xbox/src/lib/shell.ts). None for a URL that isn't http(s).
pub fn tvapp_url(server: &str, device: &str) -> Option<Url> {
    let mut base = Url::parse(server.trim()).ok()?;
    if base.scheme() != "http" && base.scheme() != "https" {
        return None;
    }
    // A server under a path prefix (https://host/onscreen) keeps it.
    if !base.path().ends_with('/') {
        let p = format!("{}/", base.path());
        base.set_path(&p);
    }
    base.set_query(None);
    base.set_fragment(None);
    let mut u = base.join("tvapp/").ok()?;
    {
        let mut q = u.query_pairs_mut();
        q.append_pair("shell", "desktop");
        if !device.is_empty() {
            q.append_pair("device", device);
        }
    }
    Some(u)
}

/// What the page asked the shell for.
#[derive(Debug, PartialEq, Eq)]
pub enum ShellRequest {
    Exit,
    ChangeServer,
}

/// The request in a navigation to an onscreen-shell: URL; None for any
/// other URL (and for an onscreen-shell: URL asking for something unknown,
/// which the caller still blocks).
pub fn shell_request(url: &Url) -> Option<ShellRequest> {
    if url.scheme() != SHELL_SCHEME {
        return None;
    }
    // onscreen-shell://exit has "exit" as its host; tolerate the
    // onscreen-shell:exit form too, and any case.
    let what = url
        .host_str()
        .filter(|h| !h.is_empty())
        .unwrap_or_else(|| url.path().trim_matches('/'))
        .to_ascii_lowercase();
    match what.as_str() {
        "exit" => Some(ShellRequest::Exit),
        "changeserver" => Some(ShellRequest::ChangeServer),
        _ => None,
    }
}

/// The local web UI's server screen, which the TV app's "changeServer" opens.
pub const SERVER_SCREEN: &str = "/native/server";

/// Whether a page load should be replaced by the TV app (when a server is
/// saved): any page of the local web UI except its server screen. That is
/// the first load (whose start page routes itself on to /login or the hub
/// before a probe of the server can answer, so the path can't be "/") and
/// where saving a server lands (reloadApp loads "/"). The server screen is
/// where a user changing servers works, so it stays.
pub fn hand_over(page: &Url, local: &Url) -> bool {
    // Not Url::origin(): tauri://localhost has an opaque origin, and no two
    // opaque origins compare equal.
    page.scheme() == local.scheme()
        && page.host_str() == local.host_str()
        && page.port_or_known_default() == local.port_or_known_default()
        && !page.path().starts_with(SERVER_SCREEN)
}

/// <server>/tvapp/ answers, so the TV app is there to show (a server older
/// than the TV app, or one that's down, keeps the desktop UI). Blocking, a
/// few seconds at most: call it off the main thread.
pub fn tvapp_answers(url: &Url) -> bool {
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(3))
        .timeout(std::time::Duration::from_secs(6))
        .build();
    match agent.get(url.as_str()).call() {
        Ok(r) => r.status() == 200,
        Err(_) => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn env_of(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let m: HashMap<String, String> =
            pairs.iter().map(|(k, v)| (k.to_string(), v.to_string())).collect();
        move |k| m.get(k).cloned()
    }

    fn args(a: &[&str]) -> Vec<String> {
        std::iter::once("onscreen-desktop").chain(a.iter().copied()).map(String::from).collect()
    }

    #[test]
    fn steam_gaming_mode_is_tv_mode() {
        assert!(detect(&env_of(&[("SteamGamepadUI", "1")]), &args(&[])));
        assert!(detect(&env_of(&[("XDG_CURRENT_DESKTOP", "gamescope")]), &args(&[])));
        assert!(detect(&env_of(&[("XDG_CURRENT_DESKTOP", "GNOME:gamescope")]), &args(&[])));
    }

    #[test]
    fn desktop_sessions_are_not() {
        assert!(!detect(&env_of(&[]), &args(&[])));
        // The Deck's desktop mode: KDE, and SteamDeck=1 alone means nothing.
        assert!(!detect(&env_of(&[("XDG_CURRENT_DESKTOP", "KDE"), ("SteamDeck", "1")]), &args(&[])));
        assert!(!detect(&env_of(&[("SteamGamepadUI", "0")]), &args(&[])));
    }

    #[test]
    fn overrides_win() {
        let gaming = env_of(&[("SteamGamepadUI", "1")]);
        assert!(!detect(&gaming, &args(&["--desktop-mode"])));
        assert!(!detect(&env_of(&[("SteamGamepadUI", "1"), ("ONSCREEN_TV_MODE", "0")]), &args(&[])));
        assert!(detect(&env_of(&[("ONSCREEN_TV_MODE", "1")]), &args(&[])));
        assert!(detect(&env_of(&[]), &args(&["--tv-mode"])));
        // The argument beats the variable.
        assert!(detect(&env_of(&[("ONSCREEN_TV_MODE", "0")]), &args(&["--tv-mode"])));
        // An unreadable value falls through to detection.
        assert!(detect(&env_of(&[("ONSCREEN_TV_MODE", "maybe"), ("SteamGamepadUI", "1")]), &args(&[])));
    }

    #[test]
    fn device_names() {
        assert_eq!(device_name(&env_of(&[("SteamDeck", "1")])), "Steam Deck");
        assert_eq!(device_name(&env_of(&[])), "");
    }

    #[test]
    fn tvapp_urls() {
        assert_eq!(
            tvapp_url("https://media.example.com", "Steam Deck").unwrap().as_str(),
            "https://media.example.com/tvapp/?shell=desktop&device=Steam+Deck"
        );
        assert_eq!(
            tvapp_url("http://10.0.0.5:7070/", "").unwrap().as_str(),
            "http://10.0.0.5:7070/tvapp/?shell=desktop"
        );
        assert_eq!(
            tvapp_url("https://host/onscreen", "").unwrap().as_str(),
            "https://host/onscreen/tvapp/?shell=desktop"
        );
        assert!(tvapp_url("file:///etc/passwd", "").is_none());
        assert!(tvapp_url("not a url", "").is_none());
    }

    #[test]
    fn shell_requests() {
        let r = |s: &str| shell_request(&Url::parse(s).unwrap());
        assert_eq!(r("onscreen-shell://exit"), Some(ShellRequest::Exit));
        assert_eq!(r("onscreen-shell://changeServer"), Some(ShellRequest::ChangeServer));
        assert_eq!(r("onscreen-shell:exit"), Some(ShellRequest::Exit));
        assert_eq!(r("onscreen-shell://reboot"), None);
        assert_eq!(r("https://exit/"), None);
    }

    #[test]
    fn hands_over_local_pages_but_the_server_screen() {
        let local = Url::parse("tauri://localhost").unwrap();
        let u = |s: &str| Url::parse(s).unwrap();
        assert!(hand_over(&u("tauri://localhost/"), &local));
        assert!(hand_over(&u("tauri://localhost/index.html"), &local));
        assert!(hand_over(&u("tauri://localhost/login"), &local));
        assert!(!hand_over(&u("tauri://localhost/native/server"), &local));
        assert!(!hand_over(&u("https://media.example.com/"), &local));
        let win = Url::parse("http://tauri.localhost/").unwrap();
        assert!(hand_over(&u("http://tauri.localhost/"), &win));
    }
}
