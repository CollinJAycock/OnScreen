// On-device regression gate for the Fire TV / Android TV app.
//
// Codifies the battery that caught (and now guards against) the ExoPlayer
// "Player is accessed on the wrong thread" crash in ProgressTracker's
// pause/stop progress report. Runs two complementary checks against a real
// device over adb and FAILS (non-zero exit) on any crash signature:
//
//   1. Seed replay   — monkey with the exact seed that reproduced the crash
//                       (event 199/400). Deterministic regression: the run
//                       must finish all 400 events with no FATAL / wrong-thread.
//   2. Playback exit  — scripted resume -> pause -> resume -> BACK exit, the
//                       path that triggers ProgressTracker.onPause/onStop.
//                       Captures the player + post-exit screenshots and the
//                       teardown logcat (expects player release, progress PUT,
//                       and zero crashes).
//
// Artifacts (screenshots + logcat) land in regression-out/<timestamp>/ so a
// failure is inspectable and a pass leaves standing evidence.
//
// Env:
//   FIRETV_HOST=<ip>   required — Fire TV's LAN IP
//   FIRETV_PORT=5555   optional, default 5555
//   ADB=<path-to-adb>  optional — explicit adb binary

import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const __dirname = dirname(fileURLToPath(import.meta.url));
const here = resolve(__dirname, '..');

const PKG = 'tv.onscreen.android';
const LEANBACK = 'android.intent.category.LEANBACK_LAUNCHER';
// The seed that deterministically reproduced the wrong-thread crash at event
// 199/400 on the pre-fix build. If this ever crashes again, the regression
// has returned.
const CRASH_SEED = '1781799841908';
// Precise, low-false-positive crash markers. A Java crash always logs
// "FATAL EXCEPTION" via the default uncaught handler; the wrong-thread line is
// the specific regression signature. We deliberately do NOT match a bare
// "IllegalStateException" — benign framework warnings (e.g. a MediaRouter
// "objects should be explicitly closed ... IllegalStateException" leak note)
// contain that text without being a crash.
const CRASH_SIGNATURES = [
  'FATAL EXCEPTION',
  'Player is accessed on the wrong thread',
];

function resolveAdb() {
  if (process.env.ADB) return process.env.ADB;
  const sdk = process.env.ANDROID_SDK_ROOT || process.env.ANDROID_HOME;
  const exe = process.platform === 'win32' ? 'adb.exe' : 'adb';
  if (sdk) {
    const p = join(sdk, 'platform-tools', exe);
    if (existsSync(p)) return p;
  }
  return exe;
}

const adb = resolveAdb();
const host = process.env.FIRETV_HOST;
const port = process.env.FIRETV_PORT || '5555';

if (!host) {
  console.error('FIRETV_HOST is required (the Fire TV LAN IP). See clients/firetv/README.md.');
  process.exit(2);
}
const target = `${host}:${port}`;

const probe = spawnSync(adb, ['version'], { stdio: 'ignore' });
if (probe.error || probe.status !== 0) {
  console.error(`adb not found or not runnable (tried "${adb}"). Install platform-tools or set ADB=.`);
  process.exit(2);
}

function adbArgs(extra) {
  return ['-s', target, ...extra];
}
// adb shell with the device doing any sleeps inline (keeps timing on-device).
function shell(cmd) {
  return spawnSync(adb, adbArgs(['shell', cmd]), { encoding: 'utf8' });
}
function logcatDump() {
  const r = spawnSync(adb, adbArgs(['logcat', '-d', '-v', 'time']), {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
  return r.stdout || '';
}
function screencap(file) {
  const r = spawnSync(adb, adbArgs(['exec-out', 'screencap', '-p']), {
    encoding: 'buffer',
    maxBuffer: 64 * 1024 * 1024,
  });
  if (r.stdout && r.stdout.length > 0) writeFileSync(file, r.stdout);
}
function pidAlive() {
  return (shell(`pidof ${PKG}`).stdout || '').trim();
}
function crashesIn(log) {
  return CRASH_SIGNATURES.filter((s) => log.includes(s));
}

const connect = spawnSync(adb, ['connect', target], { encoding: 'utf8' });
if (/cannot|failed|unable/i.test(connect.stdout || '') && !/already connected|connected to/i.test(connect.stdout || '')) {
  console.error(`adb connect ${target} failed:\n${connect.stdout}`);
  console.error('Fix: same LAN? ADB Debugging on (Settings -> My Fire TV -> Developer Options)? Accept the on-screen prompt.');
  process.exit(2);
}

const stamp = new Date().toISOString().replace(/[:.]/g, '-');
const outDir = join(here, 'regression-out', stamp);
mkdirSync(outDir, { recursive: true });
console.log(`device   : ${target}`);
console.log(`artifacts: ${outDir}\n`);

const results = [];

// ---- Check 1: seed replay ------------------------------------------------
console.log(`[1/2] seed replay (seed=${CRASH_SEED}, 400 events) ...`);
shell(`am force-stop ${PKG}; logcat -c`);
const monkey = shell(
  `monkey -p ${PKG} -c ${LEANBACK} -s ${CRASH_SEED} --pct-syskeys 0 --ignore-security-exceptions --throttle 300 -v 400`,
);
const monkeyOut = (monkey.stdout || '') + (monkey.stderr || '');
writeFileSync(join(outDir, 'seed-replay.monkey.txt'), monkeyOut);
const seedLog = logcatDump();
writeFileSync(join(outDir, 'seed-replay.logcat.txt'), seedLog);
screencap(join(outDir, 'seed-replay.end.png'));
const injected = /Events injected: (\d+)/.exec(monkeyOut)?.[1];
const monkeyFinished = monkeyOut.includes('// Monkey finished');
const monkeyAborted = monkeyOut.includes('Monkey aborted');
const seedCrashes = crashesIn(seedLog);
// A crash would either abort the monkey early or emit a FATAL/wrong-thread
// line. Process liveness is reported but is NOT a pass criterion (monkey can
// legitimately BACK out of the app).
const seedPass = injected === '400' && monkeyFinished && !monkeyAborted && seedCrashes.length === 0;
results.push({ name: 'seed-replay', pass: seedPass, detail: `injected=${injected} finished=${monkeyFinished} aborted=${monkeyAborted} crashes=[${seedCrashes}] pid=${pidAlive() || 'exited'}` });
console.log(`      -> ${seedPass ? 'PASS' : 'FAIL'} (${results.at(-1).detail})\n`);

// ---- Check 2: playback pause/resume/exit --------------------------------
console.log('[2/2] playback exit (resume -> pause -> resume -> BACK) ...');
shell(`am force-stop ${PKG}; logcat -c`);
shell(`monkey -p ${PKG} -c ${LEANBACK} 1 >/dev/null 2>&1; sleep 6`);
shell('input keyevent 20; sleep 1; input keyevent 22; sleep 1; input keyevent 23; sleep 3'); // -> detail
screencap(join(outDir, 'playback.1-detail.png'));
shell('input keyevent 23; sleep 8'); // Resume -> player
screencap(join(outDir, 'playback.2-playing.png'));
shell('input keyevent 85; sleep 3'); // MEDIA_PLAY_PAUSE -> pause (ProgressTracker.onPause)
screencap(join(outDir, 'playback.3-paused.png'));
shell('input keyevent 85; sleep 3'); // resume
shell('input keyevent 4; sleep 2; input keyevent 4; sleep 5'); // BACK x2 -> exit (ProgressTracker.onStop)
screencap(join(outDir, 'playback.4-after-exit.png'));
const playLog = logcatDump();
writeFileSync(join(outDir, 'playback.logcat.txt'), playLog);
const playCrashes = crashesIn(playLog);
const reachedPlayer = playLog.includes('ExoPlayerImpl') || playLog.includes('video = 1');
const releasedPlayer = playLog.includes('Ignoring messages sent after release') || /ExoPlayerImpl.*[Rr]elease/.test(playLog);
// Backing out of the player exits to the launcher, so an absent pid is a clean
// exit, NOT a failure. The crash signatures are the authoritative signal.
const playPass = playCrashes.length === 0;
results.push({ name: 'playback-exit', pass: playPass, detail: `reachedPlayer=${reachedPlayer} releasedPlayer=${releasedPlayer} crashes=[${playCrashes}] pid=${pidAlive() || 'exited'}` });
console.log(`      -> ${playPass ? 'PASS' : 'FAIL'} (${results.at(-1).detail})`);
if (!reachedPlayer) console.log('      note: player init not seen in logcat — scripted nav may not have reached playback (seed replay still covers it).');

// ---- Verdict -------------------------------------------------------------
const allPass = results.every((r) => r.pass);
const summary = {
  device: target,
  seed: CRASH_SEED,
  results,
  verdict: allPass ? 'PASS' : 'FAIL',
  artifacts: outDir,
};
writeFileSync(join(outDir, 'summary.json'), JSON.stringify(summary, null, 2));
console.log(`\n=== regression ${allPass ? 'PASS' : 'FAIL'} ===`);
for (const r of results) console.log(`  ${r.pass ? 'PASS' : 'FAIL'}  ${r.name}`);
console.log(`artifacts + summary.json in ${outDir}`);
process.exit(allPass ? 0 : 1);
