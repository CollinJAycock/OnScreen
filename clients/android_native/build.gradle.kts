plugins {
    id("com.android.application") version "9.5.1" apply false
    // Kotlin compilation is AGP 9's built-in Kotlin support; :app does NOT
    // apply this plugin. Declaring it here with `apply false` only puts this
    // Kotlin Gradle Plugin version on the build classpath, which is how AGP's
    // built-in Kotlin picks a newer KGP than the one it depends on by default.
    // Keep it and the Compose compiler plugin on the same version.
    id("org.jetbrains.kotlin.android") version "2.4.21" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.4.21" apply false
    id("com.google.devtools.ksp") version "2.3.13" apply false
    id("com.google.dagger.hilt.android") version "2.60.1" apply false
}
