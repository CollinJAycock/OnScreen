// The TV's screensaver, kept off while video plays or a slideshow runs.
//
// Samsung's TV app checklist (items 9 and 195): the screensaver must not come
// up over playing video or a running slideshow; an audio app may let it. The
// switch is webapis.appcommon.setScreenSaver, from Samsung's webapis.js, which
// app.html loads on a TV. Each screen that needs the picture to stay holds the
// screen awake under its own name while it does; the screensaver is allowed
// again once nothing holds it. With no appcommon (a browser, vite dev) this
// does nothing.

interface AppCommon {
  AppCommonScreenSaverState: { SCREEN_SAVER_OFF: number; SCREEN_SAVER_ON: number };
  setScreenSaver(state: number, onSuccess?: () => void, onError?: (e: unknown) => void): void;
}

const holders = new Set<string>();
// What the TV was last told: true = screensaver allowed. null = nothing sent
// yet, so the first change is always sent.
let sent: boolean | null = null;

function appCommon(): AppCommon | null {
  const w = globalThis as unknown as { webapis?: { appcommon?: AppCommon } };
  const ac = w.webapis?.appcommon;
  return ac && typeof ac.setScreenSaver === 'function' && ac.AppCommonScreenSaverState ? ac : null;
}

function apply(): void {
  const allow = holders.size === 0;
  if (sent === allow) return;
  const ac = appCommon();
  if (!ac) return;
  const state = allow
    ? ac.AppCommonScreenSaverState.SCREEN_SAVER_ON
    : ac.AppCommonScreenSaverState.SCREEN_SAVER_OFF;
  // A refusal forgets what was sent, so the next change tries again.
  sent = allow;
  try {
    ac.setScreenSaver(state, undefined, () => {
      if (sent === allow) sent = null;
    });
  } catch {
    sent = null; // an older firmware without the call
  }
}

/** Hold (true) or release (false) the screen awake for `holder`. */
export function holdScreenAwake(holder: string, hold: boolean): void {
  if (hold) holders.add(holder);
  else holders.delete(holder);
  apply();
}

/** Tests only: forget every holder and what was sent. */
export function resetScreenSaverForTests(): void {
  holders.clear();
  sent = null;
}
