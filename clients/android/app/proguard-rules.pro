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
# Hilt's gradle plugin contributes most of the rules itself, but the
# generated _HiltModules / _Factory classes survive minification more
# reliably with an explicit keep on the generated marker classes.
-keep class dagger.hilt.** { *; }
-keep,allowobfuscation @dagger.hilt.android.AndroidEntryPoint class *
-keep,allowobfuscation @dagger.hilt.android.HiltAndroidApp class *
-keep class **_HiltModules** { *; }
-keep class **_Factory { *; }

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

# ── Source-line preservation for crash reports ───────────────────────────────
-keepattributes SourceFile, LineNumberTable
# Map-file-only obfuscation: stack traces in Play Console are
# de-obfuscated automatically when we upload the mapping file.
-renamesourcefileattribute SourceFile
