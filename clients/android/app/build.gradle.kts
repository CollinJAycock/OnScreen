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
    // compileSdk + targetSdk track Play Console's target-API floor
    // (API 36 / Android 16 as of Aug 2026 — updates are blocked below
    // it from Aug 31, 2026). TV surface check for 36:
    //
    // - BACK: targeting 36 turns on the OnBackInvokedDispatcher model by
    //   default on Android 16 devices, and that is NOT gesture- or phone-only:
    //   the platform stops dispatching KEYCODE_BACK to views and to
    //   Activity.dispatchKeyEvent entirely, remote BACK key included, so an
    //   Android TV 16 box would bypass every key-listener BACK handler. (An
    //   earlier note here said hardware BACK still flowed through
    //   dispatchKeyEvent on TV — that was wrong.) Handled two ways: the
    //   player's own Skip / Up Next overlays moved to an OnBackPressedCallback
    //   (PlaybackFragment.overlayBackCallback), and the manifest opts out with
    //   android:enableOnBackInvokedCallback="false" to keep Leanback 1.0.0's
    //   internal key-listener BACK handling (hide transport controls, cancel a
    //   scrub) working — see the comment on <application>.
    // - Leanback fragment stacks unchanged, and the mediaPlayback
    //   foreground-service type was already declared.
    compileSdk = 36

    defaultConfig {
        applicationId = "tv.onscreen.android"
        // minSdk 24 — a SECURITY floor, not just an API-availability one.
        //
        // android:networkSecurityConfig is honored from API 24 only. Below
        // that the whole res/xml/network_security_config.xml file is ignored
        // and the platform default applies — and on API 23 that default still
        // TRUSTS THE USER CA STORE. So the deliberate removal of
        // `<certificates src="user" />` bought nothing on Android 6: a planted
        // CA (sideloaded "helper" app, MDM enrolment, a few minutes of ADB —
        // all routine on Fire TV) could still read every HTTPS call, including
        // the login password and the PASETO tokens. The file even documented
        // that gap; this closes it rather than describing it.
        //
        // Cost: drops Android 6 Fire TV hardware. Accepted deliberately — the
        // alternative is shipping a TLS story that silently does not hold on
        // that slice. Everything the client actually targets (Chromecast w/
        // Google TV, current Fire TV, Shield) is API 24+.
        //
        // The previous rationale still applies underneath: the codebase calls
        // API-23+ APIs (Context.getColor, Resources.getColor(int, Theme),
        // View.setForeground) with no SDK_INT guards, so 21–22 would crash on
        // first paint regardless.
        minSdk = 24
        targetSdk = 36
        // 20 (v1.2.2): the Android 16 back-dispatch fix — the
        // enableOnBackInvokedCallback opt-out and the Skip / Up Next
        // OnBackPressedCallback (see the targetSdk note above). Built to
        // replace the API-35 bundles Play flagged against the API 36 floor.
        // 19 may or may not have been uploaded since it was cut; 20 is used
        // either way, because skipping a code is free and reusing a spent
        // one is a rejected upload (below).
        //
        // 19: everything after 17 (v1.2.0) — the client security audit, the
        // pairing-origin fix, the Play device-targeting fix (leanback
        // required), and the minSdk 21 -> 24 floor above. The floor change
        // alters which devices the store offers the app to, so it must not
        // ship under a spent code.
        //
        // Codes burn on UPLOAD, not on release. That is how 13 was lost (an
        // API-35 target the Play floor rejected) and how the phone client
        // lost both 1002 and 1003. 18 was built locally but never uploaded;
        // it is skipped rather than reused, because a code only has to
        // increase — skipping one is free, while guessing wrong about
        // whether it was already spent costs a rejected upload.
        //
        // versionName 1.2.1 went with 19 (18 never shipped, so there was no
        // user-visible difference between them); 1.2.2 marks the behaviour
        // change in 20.
        //
        // 21 / 1.3.0: the v2.5 watch-state catch-up (Next Up / Plan to Watch,
        // mark watched, up-next play button, library watch filter), report a
        // problem, admin-stop handling, and removal of the dead request code.
        // 20 was built for the API-36 re-upload and may or may not have been
        // uploaded — a new code either way.
        //
        // 22 / 1.4.0: the Media3 1.3.1 -> 1.11 upgrade, the Match frame rate
        // setting (the display switches to the video's rate), AV1 claimed
        // only with a hardware decoder, DTS and TrueHD passed through to an
        // output that takes them, HDR and HLG claimed only for a screen that
        // shows them, the background-audio fixes from the Fire TV runs
        // (transcoded tracks, paused tracks, a crash on a quick reopen, deep
        // links from the background), and NVIDIA SHIELD support (no HLG or
        // 10-bit VP9 claims, the app's own mode switch). A minor version:
        // the new claims change what the server sends to every device.
        //
        // 23 / 1.4.0: the same release plus the fixes from its device test
        // (Fire TV Stick 4K Max, Hisense Google TV). Google Play already holds
        // a build with code 22, so the release goes out as 23; the name stays
        // 1.4.0, since no 22 reached users.
        //
        // 24 / 1.4.1: the Fire TV build drops Live TV, Recordings and the
        // player's online subtitle search (ONLINE_SUBTITLE_SEARCH / LIVE_TV
        // below) after Amazon rejected 1.4.0 (23) on 2026-10-01. Both flavors
        // move to 24 so the codes stay in step; the Google TV build is
        // unchanged in behaviour.
        // 25 / 1.4.2: Search's microphone shows only on a device with a
        // speech recognizer, after Amazon rejected 1.4.1 (24) on 2026-10-03
        // because the orb did nothing on its test Fire TV, which has none.
        // Fire TVs that have one keep voice search.
        versionCode = 25
        versionName = "1.4.2"
    }

    // Per-store flavor split. Both stores ship from the same code. They
    // differ in the Watch Next / EPG permissions and, since 1.4.1, in two
    // features the Fire TV build leaves out.
    //
    // Permissions: requesting WRITE_EPG_DATA makes the Amazon Appstore
    // require an EPG-capable Fire device and filters the app off most Fire
    // TV hardware, so the `firetv` flavor strips those permissions
    // (src/firetv/AndroidManifest.xml) while `googletv` keeps them for the
    // Google TV Continue-Watching row.
    //
    // Features: the Amazon Appstore rejected 1.4.0 (and 1.1.0-1.1.2) under its
    // Deceptive and Malicious Behavior policy, citing apps that "save,
    // convert, stream or download media from third-party sources". Two
    // features match that wording: the player's "Find more online…" subtitle
    // search (the server downloads subtitle files from OpenSubtitles.com) and
    // Live TV / Recordings (the server streams and records tuner or IPTV
    // channels). The `firetv` flavor turns both off through two BuildConfig
    // booleans, ONLINE_SUBTITLE_SEARCH and LIVE_TV; `googletv` keeps both.
    // They are compile-time constants, so R8 drops the gated code and the
    // resource shrinker drops its strings and icons from the Fire TV APK
    // (proguard-rules.pro leaves the Live TV screens out of its blanket
    // keeps and lets R8 drop Retrofit API methods nothing calls;
    // googletv's proguard-googletv.pro puts those rules back). Fire
    // TV users can still add subtitles and watch Live TV from the server's
    // web app; recordings saved to a library still play as library items.
    // Keep the two flavors' flags in step with clients/firetv/README.md.
    //
    // googletv is the default — it's the Play / direct-build variant, so
    // unflavored habits map to it (assembleGoogletvRelease, etc.).
    //
    // The flavors also drive `leanbackRequired`, substituted into the single
    // android.software.leanback declaration in src/main/AndroidManifest.xml.
    // A placeholder rather than a flavor manifest overlay: lint evaluates an
    // overlay in isolation, so a leanback declaration living in a flavor
    // manifest reports MissingLeanbackLauncher (the activity is in src/main)
    // and ImpliedTouchscreenHardware (the touchscreen line is in src/main) as
    // false positives. Keeping one declaration keeps lint honest.
    flavorDimensions += "store"
    productFlavors {
        create("googletv") {
            dimension = "store"
            isDefault = true
            // REQUIRED on Play: with leanback optional the AAB is eligible for
            // phones, where the app installs and has nothing to launch —
            // MainActivity declares only LEANBACK_LAUNCHER. Every certified
            // Android TV / Google TV device reports leanback, so this costs no
            // coverage.
            manifestPlaceholders["leanbackRequired"] = "true"
            // Both features stay on Google TV (see the flavor comment above).
            buildConfigField("boolean", "ONLINE_SUBTITLE_SEARCH", "true")
            buildConfigField("boolean", "LIVE_TV", "true")
            // Puts back the keep rules proguard-rules.pro relaxes so R8 can
            // drop firetv's unused code (the Live TV screens, Retrofit
            // service methods nothing calls), so googletv's R8 keep set is
            // unchanged.
            proguardFile("proguard-googletv.pro")
        }
        create("firetv") {
            dimension = "store"
            // OPTIONAL on Amazon: a number of Fire TV / Fire OS devices don't
            // report leanback, so requiring it lets the Appstore filter the app
            // off them and can block sideload.
            manifestPlaceholders["leanbackRequired"] = "false"
            // Off for the Amazon Appstore's "save, convert, stream or download
            // media from third-party sources" policy (see the flavor comment
            // above): no OpenSubtitles search in the player, no Live TV or
            // Recordings screens.
            buildConfigField("boolean", "ONLINE_SUBTITLE_SEARCH", "false")
            buildConfigField("boolean", "LIVE_TV", "false")
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
        // java.time is API 26+; minSdk is 24. LiveTVRepository.parseIso calls
        // OffsetDateTime.parse, and on API 24-25 that resolves to a
        // NoClassDefFoundError — an Error, NOT an Exception, so the
        // `catch (_: Exception)` around it does not catch it and the Live TV
        // guide hard-crashes. Desugaring backports the java.time classes
        // instead of patching the one call site, so the next java.time use is
        // safe by default rather than a latent crash on the same devices.
        isCoreLibraryDesugaringEnabled = true
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
    // Backports java.time (and friends) to the API 24 floor — see
    // isCoreLibraryDesugaringEnabled in compileOptions.
    coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.5")
    // Leanback (TV UI framework)
    implementation("androidx.leanback:leanback:1.0.0")
    implementation("androidx.recyclerview:recyclerview:1.4.0")
    // TV provider — drives the system's "Watch Next" row that shows
    // resumable items across Google TV / Android TV launchers,
    // independent of any one app's home screen. Required for TV-PN
    // quality compliance.
    implementation("androidx.tvprovider:tvprovider:1.1.0")

    // Media3 / ExoPlayer. Every media3 artifact is one release: bump them
    // together through this one value, never individually.
    val media3 = "1.11.1"
    implementation("androidx.media3:media3-exoplayer:$media3")
    implementation("androidx.media3:media3-exoplayer-hls:$media3")
    implementation("androidx.media3:media3-ui-leanback:$media3")
    // media3-ui (non-Leanback PlayerView) is used by the Live TV
    // channel player — its Leanback counterpart is bundled with the
    // detail-page playback machinery and doesn't fit a fullscreen
    // channel surface.
    implementation("androidx.media3:media3-ui:$media3")
    implementation("androidx.media3:media3-session:$media3")

    // Networking
    implementation("com.squareup.retrofit2:retrofit:2.11.0")
    implementation("com.squareup.retrofit2:converter-moshi:2.11.0")
    implementation("com.squareup.moshi:moshi:1.15.2")
    ksp("com.squareup.moshi:moshi-kotlin-codegen:1.15.2")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.squareup.okhttp3:logging-interceptor:4.12.0")
    implementation("com.squareup.okhttp3:okhttp-sse:4.12.0")

    // Image loading
    implementation("io.coil-kt:coil:2.7.0")

    // Dependency injection
    implementation("com.google.dagger:hilt-android:2.60.1")
    ksp("com.google.dagger:hilt-android-compiler:2.60.1")

    // AndroidX
    implementation("androidx.core:core-ktx:1.19.1")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.8.4")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.4")
    implementation("androidx.fragment:fragment-ktx:1.9.1")
    implementation("androidx.datastore:datastore-preferences:1.1.1")

    // Coroutines
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")

    // Unit testing
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
    testImplementation("io.mockk:mockk:1.14.11")
    testImplementation("com.google.truth:truth:1.4.5")
    // Drives the OkHttp stack (interceptor + authenticator + SSE) against a
    // real local server. The token-scoping and SSE-reconnect guards are only
    // meaningful end-to-end — mocking the client out would assert the mock.
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
}
