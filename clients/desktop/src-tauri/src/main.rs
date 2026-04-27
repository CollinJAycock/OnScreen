// Prevents additional console window on Windows in release.
// Removing this would dump a black `cmd.exe` behind the app every
// launch, which looks broken even when nothing's actually wrong.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    onscreen_desktop_lib::run()
}
