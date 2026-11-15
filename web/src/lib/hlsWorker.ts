// hls.js's transmuxer worker as a same-origin file, for the `workerPath`
// option. The ESM build Vite bundles (hls.mjs) has no inline worker, and a
// blob: worker would be blocked by the server's CSP anyway (script-src has
// no blob:), so without a path hls.js demuxes and remuxes every MPEG-TS
// segment on the main thread. The ?url import makes Vite copy the file out
// of node_modules into the build (hashed, so it always matches the bundled
// hls.js); nothing is committed. A worker that fails to load or run makes
// hls.js fall back to the main thread, as before.
import hlsWorkerUrl from 'hls.js/dist/hls.worker.js?url';

export { hlsWorkerUrl };
