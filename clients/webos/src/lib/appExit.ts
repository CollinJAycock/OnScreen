// Back with nothing in the app left to close: what LG's rules want.
//
// appinfo.json sets disableBackHistoryAPI, so the remote's Back reaches the
// app as keyCode 461 and the platform does nothing with it (with the flag
// off, webOS ran history.back() instead, and the back stack, pickers and
// dialogs never saw the key). Every screen's own Back comes first (the
// focus manager's back stack and key handlers); only a Back none of them
// took lands here, and only the app's first screens may end the app:
//
//   - Home when signed in, Setup and Sign in when not (and the splash that
//     picks between them): LG's Back-button guide asks for a popup offering
//     to exit on webOS TV 6.0 and later, built by the app with window.close()
//     as its Exit; LG staff: window.close() "will close your app without the
//     dialog on all versions of the webOS TV". From webOS TV 23 the
//     self-checklist wants "A click on the Back button on the entry page of
//     the app must display the Home screen (webOS23 - webOS25 only)", for
//     which the app calls platformBack(). Unverified: LG's Back-button guide
//     says only that platformBack() launches Home on 5.0 and older and shows
//     an exit popup from 6.0, with no word on 23 and later. Check 23, 24 and
//     25 in the webOS Cloud Test Lab; a 6-22 TV taken for 23+ still gets the
//     system's popup, never an exit without asking.
//   - Anywhere else, the previous page (nav.goBack, the hub when the stack is
//     empty): never out of the app, never to an earlier entry screen through
//     the webview's history.
//
// The version comes from the engine: LG's table pairs each webOS TV release
// with one Chromium (6.x 79, 22 87, 23 94, 24 108, 25 120, 26 132), the
// mapping Shaka Player's webOS device uses too. deviceInfo's
// platformVersion (6.x, 7.x = 22, 8.x = 23, ... per LG's SDK version table)
// is the fallback for a user agent without a Chrome version. Not on webOS,
// or not known: the popup, which works on every version.

/** Routes where Back offers to leave the app (SvelteKit route ids). */
export const ENTRY_ROUTES: readonly string[] = ['/', '/hub', '/login', '/setup'];

export function isEntryRoute(routeId: string | null | undefined): boolean {
  return !!routeId && ENTRY_ROUTES.includes(routeId);
}

/** The Chromium major version in a user agent, null without one. */
export function chromeMajor(userAgent: string): number | null {
  const m = /\bChrome\/(\d+)/.exec(userAgent);
  return m ? Number(m[1]) : null;
}

/** webOS TV release by its web engine (LG's "Web API and Web Engine"
 *  table); a newer Chromium than the table knows counts as the latest. */
export function webosFromChrome(major: number | null): number | null {
  if (major === null || !(major > 0)) return null;
  const table: [number, number][] = [
    [132, 26],
    [120, 25],
    [108, 24],
    [94, 23],
    [87, 22],
    [79, 6],
    [68, 5],
    [53, 4],
    [38, 3],
  ];
  for (const [chrome, webos] of table) if (major >= chrome) return webos;
  return null;
}

/** webOS TV release from PalmSystem / webOSSystem.deviceInfo (a JSON
 *  string or an object): its platformVersion is the SDK version, 6.x on
 *  webOS TV 6, then 7.x for 22, 8.x for 23 and so on. */
export function webosFromDeviceInfo(raw: unknown): number | null {
  let info = raw;
  if (typeof info === 'string') {
    try {
      info = JSON.parse(info);
    } catch {
      return null;
    }
  }
  if (!info || typeof info !== 'object') return null;
  const o = info as Record<string, unknown>;
  const major = Number(o.platformVersionMajor ?? String(o.platformVersion ?? '').split('.')[0]);
  if (!Number.isInteger(major) || major < 1 || major > 99) return null;
  return major <= 6 ? major : major + 15;
}

/** The TV's webOS release (6, 22, 23...), or null off webOS / when nothing
 *  says. */
export function webosVersion(env: { userAgent: string; deviceInfo?: unknown }): number | null {
  const onWebos = /\bWeb0S\b|\bwebOS\b/i.test(env.userAgent) || env.deviceInfo != null;
  if (!onWebos) return null;
  return webosFromChrome(chromeMajor(env.userAgent)) ?? webosFromDeviceInfo(env.deviceInfo);
}

/** What a Back nobody else took does on this route. */
export type RootBack =
  /** nav.goBack: the screen before this one. */
  | 'previous'
  /** The app's own Exit / Cancel popup. */
  | 'exit-popup'
  /** The platform's Back: the TV's Home screen on 5.0 and older, and, it
   *  is assumed (unverified, see above), on webOS TV 23+. */
  | 'platform-back';

/**
 * `version` from webosVersion; `canPlatformBack` whether the runtime has a
 * platformBack. Without one, a webOS TV 23+ entry screen still gets the
 * popup: an app that can leave beats one stuck on Back.
 */
export function rootBack(routeId: string | null | undefined, version: number | null, canPlatformBack: boolean): RootBack {
  if (!isEntryRoute(routeId)) return 'previous';
  const home = version !== null && (version >= 23 || version <= 5);
  return home && canPlatformBack ? 'platform-back' : 'exit-popup';
}

export interface RootBackDeps {
  /** The current route's id (SvelteKit's page.route.id). */
  routeId(): string | null | undefined;
  /** This TV's webOS release (liveWebosVersion). */
  version(): number | null;
  /** The runtime's platformBack (platformBackFn). */
  platformBack(): (() => void) | null;
  /** The previous page (nav.goBack). */
  goBack(): void;
  /** Show the Exit / Cancel popup. */
  openExitPopup(): void;
}

/** The focus manager's root Back handler (setRootBack): it always takes the
 *  key, so nothing else (the webview's history) acts on it. */
export function rootBackHandler(deps: RootBackDeps): () => boolean {
  return () => {
    const back = deps.platformBack();
    switch (rootBack(deps.routeId(), deps.version(), !!back)) {
      case 'previous':
        deps.goBack();
        break;
      case 'platform-back':
        back?.();
        break;
      default:
        deps.openExitPopup();
    }
    return true;
  };
}

// ── Runtime (the live TV) ─────────────────────────────────────────────────

interface SystemObject {
  platformBack?: () => void;
  deviceInfo?: unknown;
}

interface WebosGlobals {
  webOSSystem?: SystemObject;
  PalmSystem?: SystemObject;
  /** LG's webOSTV.js, should it ever be bundled. */
  webOS?: { platformBack?: () => void };
  navigator?: { userAgent?: string };
  close?: () => void;
}

/** The runtime's platformBack, bound; null when it has none (a desktop
 *  browser). webOSSystem is the newer name of PalmSystem, which stays as an
 *  alias; webOSTV.js's webOS.platformBack() calls PalmSystem's. */
export function platformBackFn(g: WebosGlobals = globalThis as WebosGlobals): (() => void) | null {
  for (const o of [g.webOSSystem, g.PalmSystem, g.webOS]) {
    try {
      if (o && typeof o.platformBack === 'function') {
        const fn = o.platformBack;
        return () => fn.call(o);
      }
    } catch {
      // A host object that throws on access: try the next.
    }
  }
  return null;
}

let cachedVersion: number | null | undefined;

/** This TV's webOS release, worked out once. */
export function liveWebosVersion(g: WebosGlobals = globalThis as WebosGlobals): number | null {
  if (cachedVersion === undefined) {
    let deviceInfo: unknown;
    try {
      deviceInfo = g.webOSSystem?.deviceInfo ?? g.PalmSystem?.deviceInfo;
    } catch {
      deviceInfo = undefined;
    }
    cachedVersion = webosVersion({ userAgent: g.navigator?.userAgent ?? '', deviceInfo });
  }
  return cachedVersion;
}

/** The popup's Exit: LG's documented way for an app to close itself. */
export function closeApp(g: WebosGlobals = globalThis as WebosGlobals): void {
  try {
    g.close?.();
  } catch {
    // Nothing else to try: the popup closes (the layout's exitApp), the
    // app stays.
  }
}
