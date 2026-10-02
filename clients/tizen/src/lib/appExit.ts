// Back (the remote's Return) with nothing in the app left to close: what
// Samsung's rules want.
//
// config.xml sets hwkey-event="enable", so Return reaches the app as keyCode
// 10009 and the platform does nothing with it. Every screen's own Back comes
// first (the focus manager's back stack and key handlers); only a Return none
// of them took lands here, and only the app's first screens may end the app:
//
//   - Home when signed in, Setup and Sign in when not (and the splash that
//     picks between them): Samsung's TV app checklist, "In Main(First) page
//     of App, Return key returns to SmartHub", and "at the initial page,
//     close the App and go to Smart Hub". The app closes itself
//     (tizen.application.getCurrentApplication().exit()), with no popup in
//     between. Exit stays the system's: it closes the app from anywhere.
//   - Anywhere else, the previous page (nav.goBack, the hub when the stack is
//     empty): never out of the app.

/** Routes where Return closes the app (SvelteKit route ids). */
export const ENTRY_ROUTES: readonly string[] = ['/', '/hub', '/login', '/setup'];

export function isEntryRoute(routeId: string | null | undefined): boolean {
  return !!routeId && ENTRY_ROUTES.includes(routeId);
}

/** What a Return nobody else took does on this route. */
export type RootBack =
  /** nav.goBack: the screen before this one. */
  | 'previous'
  /** Close the app, back to Smart Hub. */
  | 'exit';

export function rootBack(routeId: string | null | undefined): RootBack {
  return isEntryRoute(routeId) ? 'exit' : 'previous';
}

export interface RootBackDeps {
  /** The current route's id (SvelteKit's page.route.id). */
  routeId(): string | null | undefined;
  /** The previous page (nav.goBack). */
  goBack(): void;
  /** Close the app (closeApp). */
  exit(): void;
}

/** The focus manager's root Back handler (setRootBack): it always takes the
 *  key, so nothing else acts on it. */
export function rootBackHandler(deps: RootBackDeps): () => boolean {
  return () => {
    if (rootBack(deps.routeId()) === 'exit') deps.exit();
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

/** Close the app: Tizen's application exit, which returns to Smart Hub;
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
