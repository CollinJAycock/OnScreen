// Run the BrightScript unit tests in tests/ via the `brs` Node-side
// interpreter. brs has no built-in test harness, so we follow the
// idiomatic Roku-channel pattern: each test file prints lines like
//   PASS: <name>
//   FAIL: <name> — <details>
//   DONE: <suite>
// and we grep the output to decide pass/fail.
//
// Each test suite is run with its dependency .brs files loaded in
// the same brs invocation (no include directive in BrightScript;
// the runtime concats every file passed on the CLI into one VM).

import { spawnSync } from 'node:child_process';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = resolve(__dirname, '..');
// Invoke brs through node + the package's cli.js entry rather than
// the .bin/brs.cmd shim. On Windows the shim wraps a `node` call but
// confuses spawnSync with shell=false (cmd files need shell=true,
// which then has its own quoting + path-with-spaces issues). Going
// straight to node + the cli.js path sidesteps both.
const brsCli = join(root, 'node_modules', 'brs', 'bin', 'cli.js');

// Suite definition: the source files the suite depends on (loaded
// into the brs VM alongside it), plus the test file itself. brs
// runs `Main()` from whichever file defines it — here the test
// file; sources contribute the functions under test.
const suites = [
  {
    name: 'UrlEncodePath',
    sources: ['source/api/Endpoints.brs'],
    test: 'tests/UrlEncodePath_test.brs',
  },
  {
    name: 'AssetUrl',
    sources: ['source/api/Endpoints.brs'],
    test: 'tests/AssetUrl_test.brs',
  },
  {
    name: 'Json',
    sources: ['source/util/Json.brs'],
    test: 'tests/Json_test.brs',
  },
  {
    name: 'Strings',
    sources: ['source/util/Strings.brs'],
    test: 'tests/Strings_test.brs',
  },
  {
    name: 'PlaybackDecide',
    sources: ['source/playback/Decide.brs'],
    test: 'tests/PlaybackDecide_test.brs',
  },
];

let totalPass = 0;
let totalFail = 0;

for (const suite of suites) {
  const args = [brsCli, ...suite.sources.map((s) => join(root, s)), join(root, suite.test)];
  const r = spawnSync(process.execPath, args, { encoding: 'utf8' });
  const out = (r.stdout ?? '') + (r.stderr ?? '');

  const passes = (out.match(/^PASS: /gm) ?? []).length;
  const fails = (out.match(/^FAIL: /gm) ?? []).length;
  totalPass += passes;
  totalFail += fails;

  console.log(`\n── ${suite.name} ─────────────`);
  // Echo the brs output verbatim so failures are easy to inspect
  // without scrolling — small test files, low cost.
  process.stdout.write(out);

  if (r.status !== 0 && fails === 0) {
    // brs crashed (parse error, missing function, etc.) without
    // emitting our PASS/FAIL lines. Surface this as a failure.
    totalFail++;
    console.log(`FAIL: ${suite.name} — brs exited with status ${r.status}`);
  }
}

console.log(`\n── Summary ──────────────`);
console.log(`pass: ${totalPass}, fail: ${totalFail}`);
process.exit(totalFail === 0 ? 0 : 1);
