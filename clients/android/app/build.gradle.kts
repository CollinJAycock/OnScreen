import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("com.google.devtools.ksp")
    id("com.google.dagger.hilt.android")
}

// Release-signing config sourced from local.properties (gitignored).
// Add `release.keystore`, `release.keystorePassword`, `release.keyAlias`,
// `release.keyPassword` entries to local.properties to enable signed
// builds for Play Console upload. When the file is missing or any
// field is unset (CI / fresh checkout) the release variant falls back
// to the debug-signing config so it still builds locally.
val keystoreProperties = Properties().apply {
    val f = rootProject.file("local.properties")
    if (f.exists()) f.inputStream().use { load(it) }
}

android {
    namespace = "tv.onscreen.android"
    compileSdk = 34

    defaultConfig {
        applicationId = "tv.onscreen.android"
        minSdk = 21
        targetSdk = 34
        versionCode = 2
        versionName = "1.0.1"
    }

    signingConfigs {
        val storePath = keystoreProperties["release.keystore"] as String?
        if (storePath != null && rootProject.file(storePath).exists()) {
            create("release") {
                storeFile = rootProject.file(storePath)
                storePassword = keystoreProperties["release.keystorePassword"] as String?
                keyAlias = keystoreProperties["release.keyAlias"] as String?
                keyPassword = keystoreProperties["release.keyPassword"] as String?
            }
        }
    }

    buildTypes {
        release {
            // Minification + resource shrinking on. The previous
            // soak failure (blank MainActivity window) was a
            // missing keep rule — fixed in proguard-rules.pro
            // (Hilt entry points without `allowobfuscation`,
            // explicit DataStore + ServerPrefs keeps). See the
            // header comments in that file for the failure mode.
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
            // Use the configured release-signing config when one was
            // built above; otherwise fall back to debug so a fresh
            // checkout (no keystore on disk) can still produce a
            // working APK for testing. Play uploads require the real
            // release keystore — the fallback is a developer escape
            // hatch, never the upload artifact.
            signingConfig = signingConfigs.findByName("release")
                ?: signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    testOptions {
        unitTests.all {
            it.maxHeapSize = "2g"
            it.jvmArgs = listOf(
                "-XX:+UseParallelGC",
                "-XX:MaxMetaspaceSize=1g",
                "-XX:ReservedCodeCacheSize=256m",
                "-XX:+HeapDumpOnOutOfMemoryError",
            )
            it.forkEvery = 50
        }
    }
}

dependencies {
    // Leanback (TV UI framework)
    implementation("androidx.leanback:leanback:1.0.0")
    implementation("androidx.recyclerview:recyclerview:1.3.2")
    // TV provider — drives the system's "Watch Next" row that shows
    // resumable items across Google TV / Android TV launchers,
    // independent of any one app's home screen. Required for TV-PN
    // quality compliance.
    implementation("androidx.tvprovider:tvprovider:1.0.0")

    // Media3 / ExoPlayer
    implementation("androidx.media3:media3-exoplayer:1.3.1")
    implementation("androidx.media3:media3-exoplayer-hls:1.3.1")
    implementation("androidx.media3:media3-ui-leanback:1.3.1")
    // media3-ui (non-Leanback PlayerView) is used by the Live TV
    // channel player — its Leanback counterpart is bundled with the
    // detail-page playback machinery and doesn't fit a fullscreen
    // channel surface.
    implementation("androidx.media3:media3-ui:1.3.1")
    implementation("androidx.media3:media3-session:1.3.1")

    // Networking
    implementation("com.squareup.retrofit2:retrofit:2.11.0")
    implementation("com.squareup.retrofit2:converter-moshi:2.11.0")
    implementation("com.squareup.moshi:moshi:1.15.2")
    ksp("com.squareup.moshi:moshi-kotlin-codegen:1.15.2")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.squareup.okhttp3:logging-interceptor:4.12.0")
    implementation("com.squareup.okhttp3:okhttp-sse:4.12.0")

    // Image loading
    implementation("io.coil-kt:coil:2.6.0")

    // Dependency injection
    implementation("com.google.dagger:hilt-android:2.56.2")
    ksp("com.google.dagger:hilt-android-compiler:2.56.2")

    // AndroidX
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.8.4")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.4")
    implementation("androidx.fragment:fragment-ktx:1.8.2")
    implementation("androidx.datastore:datastore-preferences:1.1.1")

    // Coroutines
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // Unit testing
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
    testImplementation("io.mockk:mockk:1.13.11")
    testImplementation("com.google.truth:truth:1.4.4")
}
