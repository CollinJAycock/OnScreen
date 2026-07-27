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
    // compileSdk + targetSdk pinned to 35 per Play Console's Aug 2025
    // requirement for app updates. Android TV / Leanback compatibility
    // with API 35 is documented (no breaking changes for the
    // BrowseSupportFragment / DetailsSupportFragment stacks we use).
    compileSdk = 35

    defaultConfig {
        applicationId = "tv.onscreen.android"
        // minSdk 23 — the codebase already uses API-23+ APIs (Context.getColor,
        // Resources.getColor(int, Theme), View.setForeground) directly, without
        // Build.VERSION.SDK_INT guards, so an install on API 21–22 would crash
        // the moment a card presenter painted. Android TV API 21–22 (Lollipop)
        // is effectively dead as a target — the active boxes are Chromecast w/
        // Google TV, Fire TV, Shield, all on API 23+. Bumping the floor matches
        // what the code already requires and silences the 31 NewApi lint errors
        // those calls trigger.
        minSdk = 23
        targetSdk = 35
        versionCode = 10
        versionName = "1.0.7"
    }

    // Per-store flavor split. Both stores ship the same app and code; they
    // differ only in the Watch Next / EPG permissions. Requesting
    // WRITE_EPG_DATA makes the Amazon Appstore require an EPG-capable Fire
    // device and filters the app off most Fire TV hardware, so the `firetv`
    // flavor strips those permissions (src/firetv/AndroidManifest.xml) while
    // `googletv` keeps them for the Google TV Continue-Watching row.
    // googletv is the default — it's the Play / direct-build variant, so
    // unflavored habits map to it (assembleGoogletvRelease, etc.).
    flavorDimensions += "store"
    productFlavors {
        create("googletv") {
            dimension = "store"
            isDefault = true
        }
        create("firetv") {
            dimension = "store"
        }
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
            //
            // The fallback is now OPT-IN (-PallowDebugSigning=true). It used
            // to be silent, and a silent fallback emits a normally-named,
            // minified, non-debuggable app-<flavor>-release.apk signed with
            // AGP's throwaway debug key — indistinguishable from a real
            // upload artifact by filename, and the Fire TV packaging script's
            // only guard tests for an "unsigned" substring that never
            // matches. Failing loudly here is the difference between a caught
            // mistake and a rejected (or hijackable) store upload.
            val releaseSigning = signingConfigs.findByName("release")
            signingConfig = releaseSigning ?: signingConfigs.getByName("debug")
            if (releaseSigning == null) {
                val allowDebugSigning =
                    project.findProperty("allowDebugSigning")?.toString().toBoolean()
                tasks.matching { it.name.matches(Regex("assemble.*Release|bundle.*Release")) }
                    .configureEach {
                        doFirst {
                            if (!allowDebugSigning) {
                                throw GradleException(
                                    "Release build has no signing config: 'release.keystore' is " +
                                        "missing from local.properties, so this APK/AAB would be " +
                                        "signed with the DEBUG key and is not distributable.\n" +
                                        "  - To produce a real upload artifact: add release.keystore, " +
                                        "release.keystorePassword, release.keyAlias and " +
                                        "release.keyPassword to local.properties.\n" +
                                        "  - To build a debug-signed release variant anyway (local " +
                                        "testing only): re-run with -PallowDebugSigning=true",
                                )
                            }
                            logger.warn(
                                "WARNING: release variant is DEBUG-SIGNED (-PallowDebugSigning=true). " +
                                    "Do not upload this artifact to any store.",
                            )
                        }
                    }
            }
        }
    }

    buildFeatures {
        // BuildConfig generation is opt-in on modern AGP. Needed for
        // the BuildConfig.DEBUG gate that silences the HTTP logging
        // interceptor in release builds (NetworkModule).
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    testOptions {
        // Return defaults (0/null/false) for android.jar stubs in JVM unit tests
        // instead of throwing "Method … not mocked". PlaybackViewModel.prepare()
        // logs the play decision via android.util.Log.i, which otherwise throws and
        // fails every prepare()-based test.
        unitTests.isReturnDefaultValues = true
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
    // Drives the OkHttp stack (interceptor + authenticator + SSE) against a
    // real local server. The token-scoping and SSE-reconnect guards are only
    // meaningful end-to-end — mocking the client out would assert the mock.
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
}
