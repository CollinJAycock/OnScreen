plugins {
    id("com.android.application") version "9.2.0" apply false
    id("org.jetbrains.kotlin.android") version "2.2.10" apply false
    // KSP versions are pinned to Kotlin: <kotlin>-<ksp-iter>. Pure
    // semver KSP (2.3.x) targets Kotlin 2.3.x and chokes the IDE
    // sync on Kotlin 2.2.x with "Unable to load class
    // com.google.devtools.ksp.gradle.KspTaskJvm" — the class moved
    // packages between KSP iterations and the IDE caches the old
    // reference. Keep these aligned with the Kotlin version above
    // when bumping.
    id("com.google.devtools.ksp") version "2.2.10-2.0.2" apply false
    // Hilt 2.51.1 bundled an older kotlinx-metadata that can't read
    // Kotlin 2.2's metadata version 2.2.0 (raises "maximum supported
    // version is 2.1.0" during hiltJavaCompileDebug). 2.56+ ships
    // updated metadata support.
    id("com.google.dagger.hilt.android") version "2.56.2" apply false
}
