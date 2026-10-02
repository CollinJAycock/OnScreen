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

The hls.js build (`dist/hls.mjs`) bundles the first three entries below.
The fourth is code inside hls.js itself, derived from another project,
whose copyright line hls.js's LICENSE carries.

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
reader (`src/demux/video/exp-golomb.ts`) are derived from it.

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

## Licence texts

### MIT License

Applies to Svelte, SvelteKit, esm-env, Vite and eventemitter3, with the
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

### Apache License 2.0

Applies to this app, hls.js, url-toolkit and the videojs-contrib-hls code
in hls.js. The full text is in [LICENSE](LICENSE), after this app's
copyright line. Neither hls.js nor url-toolkit ships a NOTICE file.
