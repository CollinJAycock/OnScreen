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
    // it from Aug 30, 2026). TV surface check for 36: no predictive-back
    // gesture on TV (hardware KEYCODE_BACK still flows through
    // dispatchKeyEvent), Leanback fragment stacks unchanged, and the
    // mediaPlayback foreground-service type was already declared.
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
        // 14: versionCode 13 was uploaded against the API-35 target and
        // blocked by the Play floor — codes are burned on upload, not
        // release, so the re-target gets a fresh one.
        versionCode = 17
        versionName = "1.2.0"
    }

    // Per-store flavor split. Both stores ship the same app and code; they
    // differ only in the Watch Next / EPG permissions. Requesting
    // WRITE_EPG_DATA makes the Amazon Appstore require an EPG-capable Fire
    // device and filters the app off most Fire TV hardware, so the `firetv`
    // flavor strips those permissions (src/firetv/AndroidManifest.xml) while
    // `googletv` keeps them for the Google TV Continue-Watching row.
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
        }
        create("firetv") {
            dimension = "store"
            // OPTIONAL on Amazon: a number of Fire TV / Fire OS devices don't
            // report leanback, so requiring it lets the Appstore filter the app
            // off them and can block sideload.
            manifestPlaceholders["leanbackRequired"] = "false"
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
