// Back (the controller's B) with nothing in the app left to close.
//
// Every screen's own Back comes first (the focus manager's back stack and key
// handlers); only a Back none of them took lands here, and only the app's
// first screens may end the app:
//
//   - Home when signed in, Sign in and Setup when not (and the splash that
//     picks between them): the Exit / Cancel popup. Its Exit asks the shell
//     to close the app (lib/shell): a page can't close a UWP app, and Xbox
//     has no platform Back that would. Outside the shell (a browser) Exit
//     tries window.close(), which a tab the user opened ignores.
//   - Anywhere else, the previous page (nav.goBack, the hub when the stack is
//     empty): never out of the app, never to an earlier entry screen through
//     the webview's history.

import { postToShell } from './shell';

/** Routes where Back offers to leave the app (SvelteKit route ids). */
export const ENTRY_ROUTES: readonly string[] = ['/', '/hub', '/login', '/setup'];

export function isEntryRoute(routeId: string | null | undefined): boolean {
  return !!routeId && ENTRY_ROUTES.includes(routeId);
}

/** What a Back nobody else took does on this route. */
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
 *  key, so nothing else (the webview's history) acts on it. */
export function rootBackHandler(deps: RootBackDeps): () => boolean {
  return () => {
    if (rootBack(deps.routeId()) === 'previous') deps.goBack();
    else deps.openExitPopup();
    return true;
  };
}

/** The popup's Exit: the shell closes the app; a browser tab may not. */
export function closeApp(g: { close?: () => void } = globalThis as { close?: () => void }): void {
  if (postToShell('exit')) return;
  try {
    g.close?.();
  } catch {
    // Nothing else to try: the popup closes (the layout's exitApp), the
    // app stays.
  }
}
