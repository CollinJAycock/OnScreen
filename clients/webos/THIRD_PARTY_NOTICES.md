# Third-party notices

OnScreen for LG webOS TV (`clients/webos`, app id `com.onscreen.tv`) is
licensed under the Apache License 2.0: see [LICENSE](LICENSE). Its package
(the IPK) includes the open-source software below, each used under the
licence named. The app shows the same list under Settings > About >
Licence & terms (`src/lib/legal.ts`; `src/lib/legal.test.ts` checks that the
two agree, and that each entry matches the installed package's licence and
copyright line).

How the list was made: every module in the production client build
(`npm run build`) that isn't the app's own `src/` comes from one of these
packages. SvelteKit also writes the build's generated entry files from its
own templates. Tools that leave nothing in the bundle (TypeScript,
svelte-check, Vitest, the static adapter, Vite itself apart from its preload
helper) are not listed. Check the list again when a runtime dependency is
added.

## hls.js

- Licence: Apache-2.0
- Copyright (c) 2017 Dailymotion (http://www.dailymotion.com)
- Source: https://github.com/video-dev/hls.js
- Used for: HLS video playback

The hls.js build (`dist/hls.mjs`) bundles the entries below: code from
other projects, inlined or ported into hls.js. `src/lib/legal.test.ts`
fails on any copyright line in that build that isn't listed here.

### eventemitter3

- Licence: MIT
- Copyright (c) 2014 Arnout Kazemier
- Source: https://github.com/primus/eventemitter3
- Used for: events inside hls.js

### url-toolkit

- Licence: Apache-2.0
- Author: Tom Jenkinson
- Source: https://github.com/tjenkinson/url-toolkit
- Used for: URL resolution inside hls.js

### utf.js

- Licence: other ("This library is free. You can redistribute it and/or modify it.")
- Copyright (C) 1999 Masanao Izumo <iz@onicos.co.jp>
- Source: http://www.onicos.com/staff/iz/amuse/javascript/expert/utf.txt
- Used for: UTF-8 decoding inside hls.js

### videojs-contrib-hls

- Licence: Apache-2.0
- Copyright (c) 2013-2015 Brightcove
- Source: https://github.com/videojs/videojs-contrib-hls
- Used for: MP4 remuxing and Exp-Golomb parsing inside hls.js

hls.js's MP4 generator (`src/remux/mp4-generator.ts`) and Exp-Golomb
reader (`src/demux/video/exp-golomb.ts`) are derived from it, and hls.js's
LICENSE carries its copyright line.

### dash.js (CEA-608 parser)

- Licence: BSD-3-Clause
- Copyright (c) 2015-2016, DASH Industry Forum
- Source: https://github.com/Dash-Industry-Forum/dash.js
- Used for: CEA-608 closed captions inside hls.js

hls.js's CEA-608 parser is ported from dash.js's
`externals/cea608-parser.js`; its licence text is under Licence texts below.

### vtt.js

- Licence: Apache-2.0
- Copyright 2013 vtt.js Contributors
- Source: https://github.com/mozilla/vtt.js
- Used for: WebVTT subtitle cues inside hls.js

### Common Media Library

- Licence: Apache-2.0
- Copyright (c) 2023 Streaming Video Technology Alliance
- Source: https://github.com/streaming-video-technology-alliance/common-media-library
- Used for: CMCD and ID3 handling inside hls.js

A devDependency of hls.js (`@svta/common-media-library`) that its build
inlines. Its NOTICE file's lines that bear on the inlined code:
"Streaming Video Technology Alliance Common Media Library Copyright (c)
2023 Streaming Video Technology Alliance", and the structured field code's
derivation from structured-field-values (next entry).

### structured-field-values

- Licence: MIT
- Copyright (c) 2020 Jxck
- Source: https://github.com/Jxck/structured-field-values
- Used for: structured header encoding inside hls.js

The Common Media Library's structured field code (`src/structuredfield.ts`)
is derived from it.

## Svelte

- Licence: MIT
- Copyright (c) 2016-2025 Svelte Contributors (https://github.com/sveltejs/svelte/graphs/contributors)
- Source: https://github.com/sveltejs/svelte
- Used for: user interface runtime

## SvelteKit

- Licence: MIT
- Copyright (c) 2020 these people (https://github.com/sveltejs/kit/graphs/contributors)
- Source: https://github.com/sveltejs/kit
- Used for: app framework and router

## esm-env

- Licence: MIT
- Copyright 2022 Benjamin McCann
- Source: https://github.com/benmccann/esm-env
- Used for: build environment flags

## Vite (module preload helper)

- Licence: MIT
- Copyright (c) 2019-present, VoidZero Inc. and Vite contributors
- Source: https://github.com/vitejs/vite
- Used for: build tool; a few lines of it ship in the bundle

## QR Code generator library

- Licence: MIT
- Copyright (c) Project Nayuki
- Source: https://www.nayuki.io/page/qr-code-generator-library
- Used for: QR codes for links (ported into the app)

The app's QR encoder (`src/lib/qr.ts`) is ported from it; the file's header
carries the notice too.

## Licence texts

### MIT License

Applies to Svelte, SvelteKit, esm-env, Vite, eventemitter3,
structured-field-values and the QR Code generator library, with the
copyright lines above.

```text
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### BSD 3-Clause License

Applies to the dash.js CEA-608 parser in hls.js, as its source gives it,
with the copyright line above.

```text
The copyright in this software is being made available under the BSD License,
included below. This software may be subject to other third party and contributor
rights, including patent rights, and no such rights are granted under this license.

All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
list of conditions and the following disclaimer.

* Redistributions in binary form must reproduce the above copyright notice,
this list of conditions and the following disclaimer in the documentation and/or
other materials provided with the distribution.

2. Neither the name of Dash Industry Forum nor the names of its
contributors may be used to endorse or promote products derived from this software
without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS AS IS AND ANY
EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT,
INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT
NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY,
WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.
```

### Apache License 2.0

Applies to this app, hls.js, url-toolkit, vtt.js, the Common Media Library
and the videojs-contrib-hls code in hls.js. The full text is in [LICENSE](LICENSE), after this app's
copyright line. Neither hls.js nor url-toolkit ships a NOTICE file; the Common Media
Library's is quoted in its entry above.
