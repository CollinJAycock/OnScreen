# Moshi
-keep class tv.onscreen.android.data.model.** { *; }
-keep class tv.onscreen.android.data.api.ApiEnvelope* { *; }

# Retrofit
-keepattributes Signature
-keepattributes *Annotation*
-keep,allowobfuscation interface * { @retrofit2.http.* <methods>; }
-dontwarn retrofit2.**

# OkHttp
-dontwarn okhttp3.**
-dontwarn okio.**

# Coil
-dontwarn coil.**
