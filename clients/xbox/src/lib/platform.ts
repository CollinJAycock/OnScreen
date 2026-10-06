// What the console says about its display, for the capability header
// (lib/api/capabilities.ts): the resolution the video can be shown at and
// whether it shows HDR.
//
// A page can't ask the HDMI output; the shell can (UWP's
// HdmiDisplayInformation: the current mode's resolution, and whether the TV
// takes HDR10) and passes the answer on the query string (lib/shell: uhd=,
// hdr=). Without a shell answer (a desktop browser, or a shell that couldn't
// read the display) the size is the long-standing 3840x2160 claim and HDR is
// the media query, which WebView2's current Chromium answers.

import { shellParams } from './shell';

export interface PanelSize {
  width: number;
  height: number;
}

export const UHD_PANEL: PanelSize = { width: 3840, height: 2160 };
export const FHD_PANEL: PanelSize = { width: 1920, height: 1080 };

/** The size the header claims: the shell's answer when it gave one (an
 *  original Xbox One, or a console on a 1080p TV, is 1080p), else 4K. */
export function resolvePanelSize(uhd: boolean | null): PanelSize {
  return uhd === false ? FHD_PANEL : UHD_PANEL;
}

/** Nothing to start: the shell's answer is on the query string. Kept so the
 *  capability header reads the same on every TV app. */
export function startPanelProbe(): void {}

/** The panel size the capability header claims (see resolvePanelSize). */
export function readPanelSize(): PanelSize {
  return resolvePanelSize(shellParams().uhd);
}

/** The display's HDR answer from the shell: true / false, null when unknown
 *  (the header then uses the media query). */
export function panelHdr(): boolean | null {
  return shellParams().hdr;
}
