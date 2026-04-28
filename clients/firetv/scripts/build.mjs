// Build the Fire TV APK by invoking the Android Gradle build in
// clients/android/ and copying the resulting APK into
// clients/firetv/dist/ for clarity. Same APK works on Fire OS
// because Fire is an Android fork; we keep a copy here to make
// the firetv/ folder self-contained for sideload + Amazon
// Appstore submission flows.
//
// JAVA_HOME requirement matches clients/android/. Inherits whatever
// the user has set; if missing, points at Android Studio's bundled
// JBR as a fallback (matches the path used by the Daemon JVM
// criteria in clients/android/gradle/gradle-daemon-jvm.properties).

import { copyFileSync, existsSync, mkdirSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const __dirname = dirname(fileURLToPath(import.meta.url));
const here = resolve(__dirname, '..');
const androidRoot = resolve(here, '..', 'android');
const dist = join(here, 'dist');

if (!existsSync(androidRoot)) {
  console.error(`expected Android client at ${androidRoot}`);
  process.exit(1);
}
if (!existsSync(dist)) mkdirSync(dist, { recursive: true });

if (!process.env.JAVA_HOME) {
  const fallback = 'C:/Program Files/Android/Android Studio/jbr';
  if (existsSync(fallback)) {
    process.env.JAVA_HOME = fallback;
    console.log(`JAVA_HOME unset — using Android Studio JBR at ${fallback}`);
  }
}

// Use the absolute path to the gradlew wrapper so the spawned
// shell doesn't need it on PATH (Windows cmd.exe won't resolve
// `gradlew.bat` from a `cwd:` directory unless it's also `.\`-
// prefixed, which the absolute path sidesteps).
const gradleCmd = join(androidRoot, process.platform === 'win32' ? 'gradlew.bat' : 'gradlew');
console.log(`gradle assembleDebug in ${androidRoot}`);
const r = spawnSync(gradleCmd, ['assembleDebug'], {
  cwd: androidRoot,
  stdio: 'inherit',
  shell: process.platform === 'win32'
});
if (r.status !== 0) {
  console.error(`gradle exited with status ${r.status}`);
  process.exit(r.status ?? 1);
}

// Pick the most recent debug APK Gradle emitted under
// app/build/outputs/apk/debug/. AGP names it after the
// applicationId + variant; we copy it to a Fire-friendly name.
const apkDir = join(androidRoot, 'app', 'build', 'outputs', 'apk', 'debug');
if (!existsSync(apkDir)) {
  console.error(`no APK dir at ${apkDir} — did the Gradle build fail silently?`);
  process.exit(1);
}
const apks = readdirSync(apkDir)
  .filter((f) => f.endsWith('.apk'))
  .map((f) => ({ f, mtime: statSync(join(apkDir, f)).mtimeMs }))
  .sort((a, b) => b.mtime - a.mtime);
if (apks.length === 0) {
  console.error('no .apk in app/build/outputs/apk/debug/');
  process.exit(1);
}

const src = join(apkDir, apks[0].f);
const dst = join(dist, 'onscreen-firetv-debug.apk');
copyFileSync(src, dst);
console.log(`copied ${apks[0].f} → ${dst}`);
