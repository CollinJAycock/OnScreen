# Moshi reflective adapter generation relies on @JsonClass class
# names being preserved. Codegen-generated adapters (the typical
# path) survive R8 fine, but kept here as a safety net in case a
# model slips through without @JsonClass(generateAdapter = true).
-keepclasseswithmembers class tv.onscreen.mobile.data.model.** { *; }

# OkHttp pulls in a few platform-specific bits that R8 conservatively
# flags as missing on the desktop classpath.
-dontwarn okhttp3.internal.platform.**
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
-dontwarn org.openjsse.**
