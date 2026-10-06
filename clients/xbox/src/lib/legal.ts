// What Settings > About and the Licence & terms screen (routes/settings/
// legal) show: the privacy policy's address, the end-user terms, the app's
// own licence, and the notices of the open-source code the build bundles.
// Pure data (plus the LICENSE file, read in at build time), so the tests read
// it without a DOM. THIRD_PARTY_NOTICES.md carries the same list for the
// repository; a test keeps the two in step.
//
// The Microsoft Store wants the privacy policy reachable from the app; the
// end-user terms are the app's own (the Store has no clause it requires).

import licenceFile from '../../LICENSE?raw';

/** The privacy policy (web/src/routes/privacy), shown as text and as a QR
 *  code: the TV has no browser to open it in. */
export const PRIVACY_POLICY_URL = 'https://onscreen.wolverscreen.com/privacy';

/** The privacy page's contact address, which the store listing gives too. */
export const SUPPORT_EMAIL = 'collin.j.aycock@gmail.com';

/** The end-user terms, a paragraph each. Plain and short on purpose. */
export const TERMS: string[] = [
  'OnScreen for Xbox ("the app") is provided by its developer, Collin Aycock, and not by Microsoft. These terms apply to your use of the app.',
  'The app connects to your own OnScreen server, at the address you enter, and loads the images that server points it to, such as film posters from TMDB in Discover and channel logos in Live TV and Recordings. It includes no films, shows, music or other content: everything it plays comes from that server. You are responsible for the media you add to your server and for having the right to use it.',
  'The app is provided "as is", without warranty of any kind, under the Apache License 2.0 below. To the extent the law allows, its developer is not liable for any loss or damage arising from its use.',
  'The app may not be available in every country.',
  `Questions and support: ${SUPPORT_EMAIL}. Privacy policy: ${PRIVACY_POLICY_URL}.`,
];

/** The app's own licence notice (the Apache License's own boilerplate). */
export const APP_LICENCE: string[] = [
  'Copyright 2026 Collin Aycock',
  'Licensed under the Apache License, Version 2.0 (Apache-2.0); you may not use this app except in compliance with the License. The full text of the License is at the end of this screen, and at http://www.apache.org/licenses/LICENSE-2.0.',
  'Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.',
];

export type LicenceId = 'Apache-2.0' | 'MIT' | 'BSD-3-Clause' | 'other';

export interface Notice {
  /** Shown name. */
  name: string;
  /** The npm package the build takes it from; null for code that comes
   *  inside another package's bundle (hls.js carries eight), or code
   *  ported into the app's own src/. */
  pkg: string | null;
  licence: LicenceId;
  /** The copyright line as the package gives it (a Markdown link written
   *  out as "text (url)"), or its author when it has none. */
  notice: string;
  url: string;
  /** What the app uses it for. */
  role: string;
  /** For licence 'other': its terms, as the code states them. */
  terms?: string;
  /** Code bundled inside this package's own build. */
  includes?: Notice[];
}

/**
 * The open-source code in the app's bundle: the client build's modules
 * outside src/ come from these packages and nowhere else (SvelteKit also
 * writes its generated entry files from its own templates). Build tools that
 * leave nothing in the bundle (TypeScript, svelte-check, Vite itself bar its
 * preload helper) aren't listed.
 */
export const NOTICES: Notice[] = [
  {
    name: 'hls.js',
    pkg: 'hls.js',
    licence: 'Apache-2.0',
    notice: 'Copyright (c) 2017 Dailymotion (http://www.dailymotion.com)',
    url: 'https://github.com/video-dev/hls.js',
    role: 'HLS video playback',
    includes: [
      {
        name: 'eventemitter3',
        pkg: null,
        licence: 'MIT',
        notice: 'Copyright (c) 2014 Arnout Kazemier',
        url: 'https://github.com/primus/eventemitter3',
        role: 'events inside hls.js',
      },
      {
        name: 'url-toolkit',
        pkg: null,
        licence: 'Apache-2.0',
        notice: 'Author: Tom Jenkinson',
        url: 'https://github.com/tjenkinson/url-toolkit',
        role: 'URL resolution inside hls.js',
      },
      {
        name: 'utf.js',
        pkg: null,
        licence: 'other',
        notice: 'Copyright (C) 1999 Masanao Izumo <iz@onicos.co.jp>',
        url: 'http://www.onicos.com/staff/iz/amuse/javascript/expert/utf.txt',
        role: 'UTF-8 decoding inside hls.js',
        terms: 'This library is free. You can redistribute it and/or modify it.',
      },
      {
        // Not a package: hls.js's own MP4 generator and Exp-Golomb reader
        // derive from it, and its LICENSE carries this copyright for them.
        name: 'videojs-contrib-hls',
        pkg: null,
        licence: 'Apache-2.0',
        notice: 'Copyright (c) 2013-2015 Brightcove',
        url: 'https://github.com/videojs/videojs-contrib-hls',
        role: 'MP4 remuxing and Exp-Golomb parsing inside hls.js',
      },
      {
        // hls.js's CEA-608 caption parser, ported from dash.js: the BSD
        // notice sits in a comment in dist/hls.mjs.
        name: 'dash.js (CEA-608 parser)',
        pkg: null,
        licence: 'BSD-3-Clause',
        notice: 'Copyright (c) 2015-2016, DASH Industry Forum',
        url: 'https://github.com/Dash-Industry-Forum/dash.js',
        role: 'CEA-608 closed captions inside hls.js',
      },
      {
        name: 'vtt.js',
        pkg: null,
        licence: 'Apache-2.0',
        notice: 'Copyright 2013 vtt.js Contributors',
        url: 'https://github.com/mozilla/vtt.js',
        role: 'WebVTT subtitle cues inside hls.js',
      },
      {
        // A devDependency of hls.js that its build inlines (CMCD and ID3);
        // the line is its NOTICE file's.
        name: 'Common Media Library',
        pkg: null,
        licence: 'Apache-2.0',
        notice: 'Copyright (c) 2023 Streaming Video Technology Alliance',
        url: 'https://github.com/streaming-video-technology-alliance/common-media-library',
        role: 'CMCD and ID3 handling inside hls.js',
      },
      {
        // The Common Media Library's structured field code derives from it,
        // as that library's NOTICE file says.
        name: 'structured-field-values',
        pkg: null,
        licence: 'MIT',
        notice: 'Copyright (c) 2020 Jxck',
        url: 'https://github.com/Jxck/structured-field-values',
        role: 'structured header encoding inside hls.js',
      },
    ],
  },
  {
    name: 'Svelte',
    pkg: 'svelte',
    licence: 'MIT',
    notice: 'Copyright (c) 2016-2025 Svelte Contributors (https://github.com/sveltejs/svelte/graphs/contributors)',
    url: 'https://github.com/sveltejs/svelte',
    role: 'user interface runtime',
  },
  {
    name: 'SvelteKit',
    pkg: '@sveltejs/kit',
    licence: 'MIT',
    notice: 'Copyright (c) 2020 these people (https://github.com/sveltejs/kit/graphs/contributors)',
    url: 'https://github.com/sveltejs/kit',
    role: 'app framework and router',
  },
  {
    name: 'esm-env',
    pkg: 'esm-env',
    licence: 'MIT',
    notice: 'Copyright 2022 Benjamin McCann',
    url: 'https://github.com/benmccann/esm-env',
    role: 'build environment flags',
  },
  {
    name: 'Vite (module preload helper)',
    pkg: 'vite',
    licence: 'MIT',
    notice: 'Copyright (c) 2019-present, VoidZero Inc. and Vite contributors',
    url: 'https://github.com/vitejs/vite',
    role: 'build tool; a few lines of it ship in the bundle',
  },
  {
    // Not a package: lib/qr.ts is ported from it.
    name: 'QR Code generator library',
    pkg: null,
    licence: 'MIT',
    notice: 'Copyright (c) Project Nayuki',
    url: 'https://www.nayuki.io/page/qr-code-generator-library',
    role: 'QR codes for links (ported into the app)',
  },
];

export const LICENCE_NAMES: Record<LicenceId, string> = {
  'Apache-2.0': 'Apache License 2.0 (Apache-2.0)',
  MIT: 'MIT License (MIT)',
  'BSD-3-Clause': 'BSD 3-Clause License (BSD-3-Clause)',
  other: 'Its own terms',
};

/** Every notice, hls.js's bundled code included. */
export function allNotices(list: Notice[] = NOTICES): Notice[] {
  return list.flatMap((n) => [n, ...(n.includes ?? [])]);
}

/** The names of the notices under `licence`, for a licence text's heading. */
export function usedUnder(licence: LicenceId, list: Notice[] = NOTICES): string[] {
  return allNotices(list)
    .filter((n) => n.licence === licence)
    .map((n) => n.name);
}

/** The MIT License, whose copyright lines are the notices above. */
export const MIT_LICENCE: string[] = [
  'Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:',
  'The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.',
  'THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.',
];

/** The BSD 3-Clause License as the dash.js CEA-608 parser in hls.js gives
 *  it (its copyright line is the notice above), a paragraph each. */
export const BSD_3_LICENCE: string[] = [
  'The copyright in this software is being made available under the BSD License, included below. This software may be subject to other third party and contributor rights, including patent rights, and no such rights are granted under this license.',
  'All rights reserved.',
  'Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:',
  '1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.',
  '* Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.',
  '2. Neither the name of Dash Industry Forum nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.',
  'THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS AS IS AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.',
];

/** clients/webos/LICENSE as shipped: the copyright line, then the Apache
 *  License 2.0 verbatim. */
export const LICENCE_FILE: string = licenceFile;

/**
 * The Apache License's terms out of the LICENSE file, a paragraph each, for
 * the screen: from its title to "END OF TERMS AND CONDITIONS" (the appendix
 * after it says how to apply the License to a project, and the copyright
 * line before it is in APP_LICENCE). The file's hard-wrapped lines are
 * joined, so the TV wraps the text to its own width.
 */
export function apacheTerms(file: string = LICENCE_FILE): string[] {
  const paras = file
    .replace(/\r\n?/g, '\n')
    .split(/\n[ \t]*\n/)
    .map((p) => p.replace(/\s*\n\s*/g, ' ').trim())
    .filter((p) => p !== '');
  const start = paras.findIndex((p) => p.startsWith('Apache License'));
  const end = paras.indexOf('END OF TERMS AND CONDITIONS');
  if (start < 0 || end < start) return paras;
  return paras.slice(start, end + 1);
}
