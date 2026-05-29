import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("com.google.devtools.ksp")
    id("com.google.dagger.hilt.android")
}

// Upload-key signing — driven by keystore.properties at the gradle root
// (which IS clients/android_native/ since this module has its own gradlew).
// File is gitignored; CI sets the values via env vars instead. Both paths
// produce a deterministic AAB suitable for Play App Signing (Google holds
// the distribution key; this just authenticates the upload).
val keystorePropsFile = rootProject.file("keystore.properties")
val keystoreProps = Properties().apply {
    if (keystorePropsFile.exists()) {
        keystorePropsFile.inputStream().use { load(it) }
    }
}

android {
    namespace = "tv.onscreen.mobile"
    compileSdk = 35

    defaultConfig {
        // applicationId is what Play / the OS sees as the package name.
        // The internal Kotlin namespace stays `tv.onscreen.mobile` (R
        // class + BuildConfig location) — Play doesn't care, and renaming
        // every source file's package would churn the entire client for
        // no functional benefit.
        applicationId = "tv.onscreen.android"
        // Phone client targets Android 7+ — narrows the install base
        // vs the TV client (API 21) but in exchange the Compose +
        // Material3 baseline doesn't need backward compat shims for
        // window-insets, predictive-back, and dynamic colors.
        minSdk = 24
        // Android 15 (API 35) — Play Console requires targetSdk 35 for
        // new submissions from Aug 2025. Bumping ahead of submission
        // keeps the OldTargetApi lint quiet and avoids surprises.
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }

    signingConfigs {
        create("release") {
            // Skip wiring when the props file is absent so debug builds
            // and CI checkouts that haven't been provisioned with a key
            // still pass `./gradlew assembleDebug`.
            if (keystoreProps.isNotEmpty()) {
                storeFile = rootProject.file(
                    keystoreProps.getProperty("storeFile") ?: "keystore/upload.jks",
                )
                storePassword = keystoreProps.getProperty("storePassword")
                keyAlias = keystoreProps.getProperty("keyAlias") ?: "upload"
                keyPassword = keystoreProps.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
            // Only attach the signing config when the props file is
            // present — otherwise the AGP fails the configure step with
            // "Keystore file not set for signing config release" even
            // when only debug is being built.
            if (keystoreProps.isNotEmpty()) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    buildFeatures {
        compose = true
        // Generates tv.onscreen.mobile.BuildConfig (BuildConfig.DEBUG,
        // BuildConfig.VERSION_NAME, …) so release-build gating (e.g.
        // WebView remote-debugging in the EPUB reader, future logging
        // verbosity) can live in code rather than gradle conditionals.
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
        // Module-wide opt-in for media3's UnstableApi — every player
        // surface we touch (ExoPlayer config, MediaSource, AudioSink)
        // is annotated with it, and per-callsite @OptIn doesn't
        // satisfy the lint UnsafeOptInUsageError check.
        freeCompilerArgs += listOf(
            "-opt-in=androidx.media3.common.util.UnstableApi",
        )
    }
    lint {
        // UnsafeOptInUsageError on media3 stays noisy even with
        // -opt-in above because the lint detector reads the Java
        // annotations, not Kotlin's. The compile-time opt-in is
        // genuine; ignore the lint signal so a real future error
        // doesn't get buried under 22 spurious media3 ones.
        disable += "UnsafeOptInUsageError"
    }
}

// JUnit forks its own JVM per test task; the default heap (256m on
// Windows) trips OOM once the suite grows past ~150 classes with
// MockK + coroutines instrumentation loaded. 2g matches the daemon
// JVM ceiling and gives plenty of headroom for the suite.
tasks.withType<Test>().configureEach {
    maxHeapSize = "2g"
}

dependencies {
    // Compose BOM keeps the runtime/UI/material/foundation versions
    // aligned without manual pinning. Material 3 = Material You.
    val composeBom = platform("androidx.compose:compose-bom:2024.09.02")
    implementation(composeBom)
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.navigation:navigation-compose:2.8.1")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.4")
    implementation("androidx.hilt:hilt-navigation-compose:1.2.0")
    debugImplementation("androidx.compose.ui:ui-tooling")

    // WorkManager + hilt-work: long-running background downloads
    // survive process death, and Hilt injects the worker so it can
    // share the same OkHttp + repo singletons the rest of the app
    // uses (no second auth interceptor stack).
    implementation("androidx.work:work-runtime-ktx:2.9.1")
    implementation("androidx.hilt:hilt-work:1.2.0")
    ksp("androidx.hilt:hilt-compiler:1.2.0")

    // Media3 / ExoPlayer — same versions as the TV client so the
    // transcode + HLS path stays identical.
    implementation("androidx.media3:media3-exoplayer:1.3.1")
    implementation("androidx.media3:media3-exoplayer-hls:1.3.1")
    implementation("androidx.media3:media3-ui:1.3.1")
    // media3-session powers the background-audio MediaSessionService:
    // it owns the audio player, publishes a MediaSession (lockscreen /
    // Bluetooth / Android Auto controls + the OS now-playing widget),
    // and lets the UI drive playback through a MediaController. Media3
    // manages the foreground-service promotion itself — the service goes
    // foreground while the UI is foreground (playback starts on a tap),
    // avoiding the start-FGS-on-teardown crash the old handoff hit.
    implementation("androidx.media3:media3-session:1.3.1")

    // Google Cast SDK — `MediaRouteButton` for the Cast picker, plus
    // `CastContext` / `CastSession` for sending LOAD requests to the
    // Default Media Receiver. We don't ship a custom receiver app
    // (that needs a Google Cast Developer Console registration); the
    // Default Receiver handles the MP4 / HLS direct-play set we serve
    // from /media/stream/{id}.
    //
    // androidx.mediarouter brings the route-discovery UI; cast-framework
    // is the sender glue. Both pin to versions known stable on Android
    // 7+ (our minSdk).
    implementation("androidx.mediarouter:mediarouter:1.7.0")
    implementation("com.google.android.gms:play-services-cast-framework:21.5.0")
    // mediarouter's theme helper reads `?attr/colorBackground` (the
    // AppCompat-namespaced attribute, not `?android:attr/colorBackground`)
    // at MediaRouteButton construction and throws
    // IllegalArgumentException when it's translucent. The attribute is
    // defined by appcompat; without it on the classpath the lookup
    // falls through to the framework value (#0 under our Compose-only
    // host theme). The AppCompat dep also lets us wrap with a
    // Theme.AppCompat.* theme where the attribute is set to an opaque
    // value, fixing the crash without changing the app shell.
    implementation("androidx.appcompat:appcompat:1.7.0")

    // Chrome Custom Tabs — used by the SSO bridge to open the
    // server's web /pair page in an in-app browser tab. The user
    // signs in via OIDC / SAML / LDAP / local through the full web
    // login UI, the tab auto-claims the pair PIN, and the app polls
    // the existing pair-poll endpoint to receive the token pair.
    // No deep-link callback or token-in-URL leakage — the server
    // handshake stays in HTTPS land.
    implementation("androidx.browser:browser:1.8.0")

    // androidx.webkit — pulls in WebViewAssetLoader so the EPUB
    // reader can serve a bundled epub.js + the pre-fetched .epub
    // bytes through one same-origin "https://appassets.androidplatform.net"
    // namespace. Avoids file:// (which Chromium sandboxes hard) and
    // skips the manual shouldInterceptRequest plumbing.
    implementation("androidx.webkit:webkit:1.12.1")

    // Networking
    implementation("com.squareup.retrofit2:retrofit:2.11.0")
    implementation("com.squareup.retrofit2:converter-moshi:2.11.0")
    implementation("com.squareup.moshi:moshi:1.15.2")
    ksp("com.squareup.moshi:moshi-kotlin-codegen:1.15.2")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.squareup.okhttp3:logging-interceptor:4.12.0")
    implementation("com.squareup.okhttp3:okhttp-sse:4.12.0")

    // Image loading — Coil's compose integration.
    implementation("io.coil-kt:coil-compose:2.6.0")

    // OSMDroid — OpenStreetMap tile renderer for the photo-map view.
    // No API key, no Google Play Services dependency, no billing
    // setup — just pulls public OSM raster tiles. Embedded in a
    // Compose tree via AndroidView; the lifecycle integration lives
    // in OsmMap.kt. Picked over MapLibre + Google Maps because it
    // avoids an account/key handshake the user shouldn't have to
    // configure for a basic geotagged-photo map.
    implementation("org.osmdroid:osmdroid-android:6.1.20")

    // Dependency injection
    implementation("com.google.dagger:hilt-android:2.56.2")
    ksp("com.google.dagger:hilt-android-compiler:2.56.2")

    // AndroidX
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.8.4")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.4")
    implementation("androidx.datastore:datastore-preferences:1.1.1")

    // Coroutines
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // Unit testing
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
    testImplementation("io.mockk:mockk:1.13.11")
    testImplementation("com.google.truth:truth:1.4.4")
}
