// Whether a server's first-run setup is known to be done, remembered on this
// device per server (the browser's own origin, or the desktop app's saved
// server). The root layout renders at once when it is, instead of holding
// the splash for GET /setup/status on every page load; the check still runs
// in the background and sends the page to /setup if the server now says
// setup is needed (a reset database). Storage that throws or is empty just
// means waiting for the check, as before.

const KEY = 'onscreen_setup_done';

export function setupKnownDone(server: string): boolean {
  if (!server) return false;
  try {
    return localStorage.getItem(KEY) === server;
  } catch {
    return false;
  }
}

export function rememberSetupDone(server: string, done: boolean): void {
  try {
    if (done && server) localStorage.setItem(KEY, server);
    else localStorage.removeItem(KEY);
  } catch { /* storage unavailable: the next load waits for the check */ }
}
