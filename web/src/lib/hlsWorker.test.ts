import { hlsWorkerUrl } from './hlsWorker';

describe('hlsWorkerUrl', () => {
  it("is the bundled hls.js's worker file, served from this origin", () => {
    expect(hlsWorkerUrl).toMatch(/hls\.worker[^/]*\.js$/);
    // The server's CSP allows a same-origin worker, never blob: or data:.
    expect(hlsWorkerUrl).not.toMatch(/^(blob|data):/);
  });
});
