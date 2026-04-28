// Launch the installed channel on the connected Tizen TV. Useful
// for the dev loop: `npm run package && npm run install-tv && npm
// run launch-tv` reloads the channel without picking up the
// remote and navigating to it manually.
//
// App ID matches the `tizen:application id="..."` value in
// config.xml. If you change that, change this too.

import { spawnSync } from 'node:child_process';

const APP_ID = 'OnScreenTV.OnScreen';
const device = process.env.TIZEN_DEVICE;
const target = device ? ['-t', device] : [];

const r = spawnSync('tizen', ['run', '-p', APP_ID, ...target], {
  stdio: 'inherit',
  shell: process.platform === 'win32'
});

if (r.status !== 0) {
  console.error(`\ntizen run exited with status ${r.status}`);
  process.exit(r.status ?? 1);
}
