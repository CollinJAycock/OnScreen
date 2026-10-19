// Back (the remote's Return) with nothing in the app left to close: what
// Samsung's rules want.
//
// config.xml sets hwkey-event="enable", so Return reaches the app as keyCode
// 10009 and the platform does nothing with it. Every screen's own Back comes
// first (the focus manager's back stack and key handlers); only a Return none
// of them took lands here, and only the app's first screens may end the app:
//
//   - Home when signed in, Setup and Sign in when not (and the splash that
//     picks between them): the app's own Exit / Cancel popup, an HTML
//     element (components/ExitPopup), whose Exit closes the app
//     (tizen.application.getCurrentApplication().exit()) and goes back to
//     Smart Hub. Samsung's review of 1.1.0 (2026-10) rejected closing on
//     Return without asking: "You must make the application exit
//     confirmation popup independently, using an HTML element" (Return Key
//     Policy, developer.samsung.com ... terminating-applications). Exit
//     stays the system's: it closes the app from anywhere, no popup.
//   - Anywhere else, the previous page (nav.goBack, the hub when the stack is
//     empty): never out of the app.

/** Routes where Return offers to close the app (SvelteKit route ids). */
export const ENTRY_ROUTES: readonly string[] = ['/', '/hub', '/login', '/setup'];

export function isEntryRoute(routeId: string | null | undefined): boolean {
  return !!routeId && ENTRY_ROUTES.includes(routeId);
}

/** What a Return nobody else took does on this route. */
export type RootBack =
  /** nav.goBack: the screen before this one. */
  | 'previous'
  /** The app's own Exit / Cancel popup. */
  | 'exit-popup';

export function rootBack(routeId: string | null | undefined): RootBack {
  return isEntryRoute(routeId) ? 'exit-popup' : 'previous';
}

export interface RootBackDeps {
  /** The current route's id (SvelteKit's page.route.id). */
  routeId(): string | null | undefined;
  /** The previous page (nav.goBack). */
  goBack(): void;
  /** Show the Exit / Cancel popup. */
  openExitPopup(): void;
}

/** The focus manager's root Back handler (setRootBack): it always takes the
 *  key, so nothing else acts on it. */
export function rootBackHandler(deps: RootBackDeps): () => boolean {
  return () => {
    if (rootBack(deps.routeId()) === 'exit-popup') deps.openExitPopup();
    else deps.goBack();
    return true;
  };
}

// ── Runtime (the live TV) ─────────────────────────────────────────────────

interface TizenGlobals {
  tizen?: {
    application?: {
      getCurrentApplication?: () => { exit?: () => void };
    };
  };
  close?: () => void;
}

/** The popup's Exit: Tizen's application exit, which returns to Smart Hub;
 *  window.close() outside a Tizen webview (the emulator's browser, vite
 *  dev), where it may do nothing at all. */
export function closeApp(g: TizenGlobals = globalThis as TizenGlobals): void {
  try {
    const app = g.tizen?.application?.getCurrentApplication?.();
    if (app?.exit) {
      app.exit();
      return;
    }
  } catch {
    // The runtime refused: try the browser's close below.
  }
  try {
    g.close?.();
  } catch {
    // Nothing else to try: the app stays up.
  }
}
