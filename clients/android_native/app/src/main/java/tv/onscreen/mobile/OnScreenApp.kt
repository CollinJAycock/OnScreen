package tv.onscreen.mobile

import android.app.Application
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.disk.DiskCache
import coil.memory.MemoryCache
import dagger.hilt.android.HiltAndroidApp
import okhttp3.OkHttpClient
import javax.inject.Inject

@HiltAndroidApp
class OnScreenApp : Application(), ImageLoaderFactory {

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
