package tv.onscreen.mobile

import android.app.Application
import androidx.hilt.work.HiltWorkerFactory
import androidx.work.Configuration
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.disk.DiskCache
import coil.memory.MemoryCache
import dagger.hilt.android.HiltAndroidApp
import okhttp3.OkHttpClient
import javax.inject.Inject

@HiltAndroidApp
class OnScreenApp : Application(), ImageLoaderFactory, Configuration.Provider {

    @Inject lateinit var workerFactory: HiltWorkerFactory

    /** Involuntary sign-out teardown (background audio, stream credentials,
     *  identity caches). Process-level so it runs even with no activity alive
     *  — mirrors the TV client's OnScreenApp. */
    @Inject lateinit var signOutTeardown: tv.onscreen.mobile.playback.SignOutTeardown

    override val workManagerConfiguration: Configuration
        get() = Configuration.Builder()
            .setWorkerFactory(workerFactory)
            .build()

    override fun onCreate() {
        super.onCreate()
        // OSMDroid one-time init. OSM's tile usage policy wants a
        // User-Agent that names the app and a way to reach its
        // maintainer (osmdroid won't fetch MAPNIK tiles under its own
        // "osmdroid" default, and a bare package name says neither
        // which build nor who to contact). Configuration.load() is
        // never called, so osmdroid's "<package>/<versionCode>" agent
        // stays unset and this is the value the tile server sees.
        // Tiles go in the app's private cache dir — no
        // external-storage permission needed on API 29+.
        org.osmdroid.config.Configuration.getInstance().apply {
            userAgentValue = "OnScreen/${BuildConfig.VERSION_NAME} " +
                "(+https://github.com/CollinJAycock/OnScreen)"
            osmdroidBasePath = cacheDir.resolve("osmdroid")
            osmdroidTileCache = cacheDir.resolve("osmdroid/tiles")
        }
        signOutTeardown.start()
    }


    // Coil shares the OkHttp client with the Retrofit stack so the
    // AuthInterceptor + token-refresh authenticator apply uniformly
    // to artwork URLs (which are gated behind a stream token on the
    // server). Without this, posters fall back to anonymous fetches
    // and 401 against private libraries.
    @Inject lateinit var okHttpClient: OkHttpClient

    override fun newImageLoader(): ImageLoader =
        ImageLoader.Builder(this)
            .okHttpClient(okHttpClient)
            .memoryCache {
                MemoryCache.Builder(this)
                    .maxSizePercent(0.20)
                    .build()
            }
            .diskCache {
                DiskCache.Builder()
                    .directory(cacheDir.resolve("artwork"))
                    .maxSizeBytes(96L * 1024 * 1024)
                    .build()
            }
            .build()
}
