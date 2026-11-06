plugins {
    id("com.android.application") version "9.5.0" apply false
    // Kotlin compilation is AGP 9's built-in Kotlin support; :app does NOT
    // apply this plugin. Declaring it here with `apply false` only puts this
    // Kotlin Gradle Plugin version on the build classpath, which is how AGP's
    // built-in Kotlin picks a newer KGP than the one it depends on by default.
    // Bump it to move the Kotlin compiler.
    id("org.jetbrains.kotlin.android") version "2.4.21" apply false
    // KSP 2.3+ is versioned independently of Kotlin (no more
    // <kotlin>-<ksp-iter> pairs) and is KSP2-only. It supports AGP's
    // built-in Kotlin.
    id("com.google.devtools.ksp") version "2.3.13" apply false
    // Hilt 2.51.1 bundled an older kotlinx-metadata that can't read
    // Kotlin 2.2's metadata version 2.2.0 (raises "maximum supported
    // version is 2.1.0" during hiltJavaCompileDebug). 2.56+ ships
    // updated metadata support; 2.59+ supports AGP 9's built-in Kotlin.
    id("com.google.dagger.hilt.android") version "2.60.1" apply false
}
