# R8 rules for the googletv flavor only, added to proguard-rules.pro by
# productFlavors in build.gradle.kts. proguard-rules.pro relaxes a few keep
# rules so the firetv flavor, which builds with BuildConfig.LIVE_TV and
# ONLINE_SUBTITLE_SEARCH = false, can drop the code behind them. Each rule
# here puts the original back, so googletv's R8 keep set is unchanged.

# The Live TV screens (tv.onscreen.android.ui.livetv). proguard-rules.pro
# keeps every @AndroidEntryPoint class, every *_GeneratedInjector and every
# ui Fragment verbatim, and every Hilt component in full (see the notes
# there), but leaves this package out of those rules and keeps the
# components with `allowshrinking`, so firetv, which never opens these
# screens, can drop them.
-keep @dagger.hilt.android.AndroidEntryPoint class tv.onscreen.android.ui.livetv.** { *; }
-keep class tv.onscreen.android.ui.livetv.**_GeneratedInjector { *; }
-keep class tv.onscreen.android.ui.livetv.**Fragment { *; }
-keep class **_HiltComponents** { *; }

# Retrofit service interfaces. proguard-rules.pro keeps every interface but
# their @retrofit2.http methods only with `allowshrinking`, so firetv drops
# the API methods nothing calls there (among them online subtitle search and
# download). This is the original rule, which keeps every one of them.
-keep,allowobfuscation interface * {
    @retrofit2.http.* <methods>;
}
