# ── Moshi ────────────────────────────────────────────────────────────────────
# Data classes serialised over the wire. R8 would otherwise strip
# unused getters / property names that Moshi reflects on at runtime.
-keep class tv.onscreen.android.data.model.** { *; }
-keep class tv.onscreen.android.data.api.ApiEnvelope* { *; }
# Generated JsonAdapters from @JsonClass(generateAdapter = true). KSP
# emits these in the same package as the data class with a "JsonAdapter"
# suffix; their constructors read class metadata reflectively.
-keep class **JsonAdapter { *; }
-keepclassmembers class **JsonAdapter {
    <init>(...);
    <fields>;
}
# Moshi runtime + annotation lookup paths.
-keepclassmembers class kotlin.Metadata { *; }
-keep @com.squareup.moshi.JsonClass class *
-keepclasseswithmembers class * {
    @com.squareup.moshi.* <methods>;
}
-keepclasseswithmembers class * {
    @com.squareup.moshi.* <fields>;
}

# ── Retrofit ─────────────────────────────────────────────────────────────────
-keepattributes Signature, *Annotation*, Exceptions, InnerClasses, EnclosingMethod
-keep,allowobfuscation,allowshrinking interface retrofit2.Call
-keep,allowobfuscation,allowshrinking class retrofit2.Response
# Service interfaces — annotation values referenced via reflection.
-keep,allowobfuscation interface * {
    @retrofit2.http.* <methods>;
}
-dontwarn retrofit2.**
-dontwarn org.codehaus.mojo.animal_sniffer.IgnoreJRERequirement

# ── OkHttp / Okio ────────────────────────────────────────────────────────────
-dontwarn okhttp3.**
-dontwarn okio.**
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**

# ── Hilt / Dagger ────────────────────────────────────────────────────────────
# Hilt's gradle plugin contributes most rules itself, but the previous
# config used `allowobfuscation` on @AndroidEntryPoint / @HiltAndroidApp,
# which let R8 rename the entry-point classes. Hilt's generated
# Hilt_MainActivity / Hilt_OnScreenApp parents look up the original
# class name reflectively via a generated `componentManager()` bridge,
# so the rename produced a silent break: the activity loaded but the
# Hilt graph wasn't wired, leaving `@Inject lateinit var prefs` null
# and MainActivity rendering a blank window. Keeping the entry-point
# classes verbatim (no `allowobfuscation`) costs ~few KB and removes
# that failure mode.
-keep class dagger.hilt.** { *; }
-keep @dagger.hilt.android.AndroidEntryPoint class * { *; }
-keep @dagger.hilt.android.HiltAndroidApp class * { *; }
-keep @dagger.hilt.android.lifecycle.HiltViewModel class * { *; }
-keep class **_HiltModules** { *; }
-keep class **_HiltModules$* { *; }
-keep class **_Factory { *; }
-keep class **_GeneratedInjector { *; }
-keep class **_HiltComponents** { *; }
-keep class hilt_aggregated_deps.** { *; }

# ── DataStore (preferences) ──────────────────────────────────────────────────
# ServerPrefs is a Preferences DataStore singleton injected via Hilt at
# the activity level. The first thing MainActivity does is await
# `prefs.hasServer.first()` before deciding which fragment to show. R8
# was stripping the synthetic accessor bridges DataStore's flow
# extensions generate (Context.preferencesDataStore is a property
# delegate with reified types), which left that `.first()` call waiting
# on a flow that never emitted — same blank-window symptom as the Hilt
# rename. Keeping the whole datastore package is heavy-handed but the
# library is small (~80 KB) and the precision isn't worth the risk of
# a subtler regression on the next R8 version bump.
-keep class androidx.datastore.** { *; }
-keep class androidx.datastore.preferences.** { *; }

# ── App-side prefs + DI graph ────────────────────────────────────────────────
# Belt-and-braces: keep our own DataStore-touching classes and the Hilt
# modules that wire them. These are the precise bits whose stripping
# produced the blank-window soak failure.
-keep class tv.onscreen.android.data.prefs.** { *; }
-keep class tv.onscreen.android.di.** { *; }

# ── Coroutines ───────────────────────────────────────────────────────────────
# Suspend-function continuations carry generic-type info; without
# attributes preserved, R8 can collapse the parametric signature
# Retrofit relies on for response-body deserialisation.
-keepclassmembers class kotlinx.coroutines.** {
    volatile <fields>;
}
-dontwarn kotlinx.coroutines.flow.**

# ── Media3 / ExoPlayer ───────────────────────────────────────────────────────
# Media3 ships consumer rules, but the @UnstableApi entry points the
# Leanback adapter hits aren't always covered. Belt-and-braces.
-keep class androidx.media3.** { *; }
-dontwarn androidx.media3.**

# ── Coil ─────────────────────────────────────────────────────────────────────
-dontwarn coil.**

# ── App entry points ─────────────────────────────────────────────────────────
# OnScreenApp + MainActivity launch via the manifest, so they need to
# survive even when nothing else references them statically.
-keep class tv.onscreen.android.OnScreenApp { *; }
-keep class tv.onscreen.android.ui.MainActivity { *; }
# Hilt-generated entry-point + module classes referenced reflectively.
-keep class tv.onscreen.android.**_GeneratedInjector { *; }
-keep class tv.onscreen.android.**HiltModules** { *; }

# Fragments are instantiated reflectively by FragmentManager. AndroidX
# Fragment isn't kept by default and the no-arg constructor R8 can
# trivially preserve isn't enough — the @AndroidEntryPoint annotation
# subclass relationship needs the original class to survive too.
-keep class tv.onscreen.android.ui.**Fragment { *; }

# Foreground services are instantiated by the system from the manifest
# action filter (androidx.media3.session.MediaSessionService).
-keep class tv.onscreen.android.playback.OnScreenMediaSessionService { *; }

# ── Source-line preservation for crash reports ───────────────────────────────
-keepattributes SourceFile, LineNumberTable
# Map-file-only obfuscation: stack traces in Play Console are
# de-obfuscated automatically when we upload the mapping file.
-renamesourcefileattribute SourceFile
